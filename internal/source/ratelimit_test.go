package source_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/source"
)

// PRs that degrade to discovery-only data after a secondary rate limit must
// be marked Partial so consumers can render loading state instead of
// misleading zero-value badges; fully hydrated PRs must not be marked.
func TestFetch_SecondaryDegradationMarksPartialPRs(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 4)
	prMap := make(map[int]string, 4)
	for i := range specs {
		specs[i] = prSpec{
			num: i + 1, title: fmt.Sprintf("PR %d", i+1), repo: "org/repo",
			author: "alice", oid: fmt.Sprintf("oid%d", i+1),
			mergeable: "MERGEABLE", mergeState: "CLEAN",
			checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
		}
		prMap[i+1] = specs[i].hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: map[string]map[int]string{"org/repo": prMap},
	}
	// Fail on the second hydrate call with a secondary rate limit: batch 1
	// (PRs 1-2) succeeds, batch 2 (PRs 3-4) degrades to discovery data.
	fake.hydrateFailSecondaryAfter.Store(1)

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client,
		source.WithLogger(discardLogger()),
		source.WithHydrateBatchSize(2),
		source.WithHydrateConcurrency(1),
	)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 4)

	assert.False(t, queue.Inbox[0].Partial, "hydrated PRs must not be marked partial")
	assert.False(t, queue.Inbox[1].Partial, "hydrated PRs must not be marked partial")
	assert.True(t, queue.Inbox[2].Partial, "degraded PRs must be marked partial")
	assert.True(t, queue.Inbox[3].Partial, "degraded PRs must be marked partial")
}

// When GitHub's primary rate limit is exhausted mid-fetch, the source must
// remember the resetAt observed on the last successful response and refuse to
// touch the network again until that time passes — even across Fetch calls.
func TestFetch_PrimaryRateLimitBacksOffUntilObservedReset(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 13, 14, 0, 0, 0, time.UTC)
	now := base
	clock := func() time.Time { return now }

	fake := onePRFake(0, 0)
	fake.rateLimitResetAt = base.Add(2 * time.Minute).UTC().Format(time.RFC3339)

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client,
		source.WithLogger(discardLogger()),
		source.WithClock(clock),
	)

	// First fetch succeeds and observes the future resetAt.
	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	// Every subsequent discovery query is rejected with the primary limit 403.
	fake.discoveryFailPrimaryLimit.Store(true)
	_, err = src.Fetch(context.Background())
	require.ErrorIs(t, err, source.ErrPrimaryRateLimit)

	// While backed off, Fetch short-circuits without any network activity.
	discoveryCalls := fake.discoveryCalls.Load()
	loginCalls := fake.loginCalls.Load()
	_, err = src.Fetch(context.Background())
	require.ErrorIs(t, err, source.ErrPrimaryRateLimit)
	assert.Equal(t, discoveryCalls, fake.discoveryCalls.Load(),
		"backed-off fetch must not issue discovery queries")
	assert.Equal(t, loginCalls, fake.loginCalls.Load(),
		"backed-off fetch must not issue identity queries")

	// Once the observed reset time passes, fetching resumes.
	now = base.Add(2*time.Minute + time.Second)
	fake.discoveryFailPrimaryLimit.Store(false)
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
}

// When no successful response ever reported a resetAt, the source cannot know
// the true deadline and must fall back to a short wait: no network activity
// for at least 30s, and resumption within a minute.
func TestFetch_PrimaryRateLimitFallsBackWhenResetUnknown(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 13, 14, 0, 0, 0, time.UTC)
	now := base
	clock := func() time.Time { return now }

	fake := onePRFake(0, 0)
	fake.discoveryFailPrimaryLimit.Store(true)

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client,
		source.WithLogger(discardLogger()),
		source.WithClock(clock),
	)

	_, err := src.Fetch(context.Background())
	require.ErrorIs(t, err, source.ErrPrimaryRateLimit)

	// Immediately and 30s out: no network activity.
	discoveryCalls := fake.discoveryCalls.Load()
	_, err = src.Fetch(context.Background())
	require.ErrorIs(t, err, source.ErrPrimaryRateLimit)
	assert.Equal(t, discoveryCalls, fake.discoveryCalls.Load(),
		"must back off immediately after a primary rate limit")

	now = base.Add(30 * time.Second)
	_, err = src.Fetch(context.Background())
	require.ErrorIs(t, err, source.ErrPrimaryRateLimit)
	assert.Equal(t, discoveryCalls, fake.discoveryCalls.Load(),
		"fallback backoff must hold for at least 30s")

	// Within a minute of the error, network activity resumes.
	now = base.Add(time.Minute + time.Second)
	_, err = src.Fetch(context.Background())
	require.ErrorIs(t, err, source.ErrPrimaryRateLimit,
		"fetch still fails while GitHub rejects queries")
	assert.Greater(t, fake.discoveryCalls.Load(), discoveryCalls,
		"fallback backoff must release within a minute")
}

// A primary rate limit 403 on a hydrate batch must not be retried (even with a
// Retry-After header), must abort the fetch with ErrPrimaryRateLimit, and must
// arm the backoff so the next Fetch short-circuits.
func TestFetch_PrimaryRateLimitOnHydrateNotRetried(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 13, 14, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	fake := onePRFake(100, 403)
	fake.hydrateFailRetryAft.Store(true)
	fake.hydrateFailPrimaryLimit.Store(true)

	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(client, source.WithRetryBaseDelay(time.Millisecond))
	src := source.NewGraphQLSource(retrying,
		source.WithLogger(discardLogger()),
		source.WithClock(clock),
	)

	_, err := src.Fetch(context.Background())
	require.ErrorIs(t, err, source.ErrPrimaryRateLimit)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(),
		"primary rate limit must not be retried")

	hydrateCalls := fake.hydrateCalls.Load()
	_, err = src.Fetch(context.Background())
	require.ErrorIs(t, err, source.ErrPrimaryRateLimit)
	assert.Equal(t, hydrateCalls, fake.hydrateCalls.Load(),
		"backed-off fetch must not issue hydrate queries")
}
