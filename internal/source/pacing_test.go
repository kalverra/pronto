package source_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/source"
)

// hotSpec is PR #1 with CI running on a fresh push.
func hotSpec() prSpec {
	return prSpec{
		num: 1, title: "Hot PR", repo: "org/repo", author: "alice", oid: "oid-hot",
		updatedAt: time.Now(),
		mergeable: "MERGEABLE", mergeState: "BLOCKED",
		checkContexts: []checkSpec{{name: "ci", status: "IN_PROGRESS"}},
		rollupState:   "PENDING",
	}
}

// quietSpec is a settled PR last touched age ago.
func quietSpec(num int, age time.Duration) prSpec {
	return prSpec{
		num: num, title: "Quiet PR", repo: "org/repo", author: "bob", oid: "oid-quiet",
		updatedAt: time.Now().Add(-age),
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}
}

// pacedFake serves specs from review-requested:@me and returns a source on
// a manual clock.
func pacedFake(t *testing.T, specs ...prSpec) (*fakeGitHub, *testClock, *source.GraphQLSource) {
	t.Helper()
	prs := map[int]string{}
	for _, s := range specs {
		prs[s.num] = s.hydrateJSON()
	}
	fake := &fakeGitHub{
		login:    "kalverra",
		searches: map[string][]discoveryPage{"review-requested:@me": discoverPRs(specs)},
		prs:      map[string]map[int]string{"org/repo": prs},
	}
	clk := newTestClock()
	src := source.NewGraphQLSource(
		newTestGraphQLClient(t, fake.handler()),
		source.WithLogger(discardLogger()),
		source.WithClock(clk.Now),
	)
	return fake, clk, src
}

// hydratedIDs returns the node IDs requested by hydrate calls since the last
// reset.
func (f *fakeGitHub) hydratedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, vars := range f.hydrateVars {
		raw, _ := vars["ids"].([]any)
		for _, id := range raw {
			ids = append(ids, id.(string))
		}
	}
	return ids
}

func (f *fakeGitHub) resetHydrated() {
	f.mu.Lock()
	f.hydrateVars = nil
	f.mu.Unlock()
}

// Between discoveries, a hot tick re-hydrates only PRs whose state is moving,
// without searching.
func TestFetch_HotTickSkipsDiscovery(t *testing.T) {
	t.Parallel()

	fake, clk, src := pacedFake(t, hotSpec(), quietSpec(2, 48*time.Hour))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.WithinDuration(t, clk.Now().Add(source.DefaultHotInterval), src.NextFetch(), 0,
		"a hot PR schedules the next tick one hot interval out")
	fake.resetHydrated()

	clk.Advance(source.DefaultHotInterval)
	q, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Len(t, q.Inbox, 2, "the queue still carries every tracked PR")
	assert.Equal(t, int32(1), fake.discoveryCalls.Load(), "a hot tick must not search")
	assert.Equal(t, []string{"org/repo#1"}, fake.hydratedIDs(), "only the hot PR is re-hydrated")
}

// A hot tick that finds a PR merged drops it from the queue at once.
func TestFetch_HotTickDropsClosedPR(t *testing.T) {
	t.Parallel()

	fake, clk, src := pacedFake(t, hotSpec())

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	fake.prs["org/repo"][1] = strings.Replace(hotSpec().hydrateJSON(), "{", `{"state": "MERGED",`, 1)
	clk.Advance(source.DefaultHotInterval)
	q, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Empty(t, q.Inbox)
	assert.Equal(t, int32(1), fake.discoveryCalls.Load())
}

// CI finishing does not bump updatedAt; the discovery fingerprint still
// catches it, so a quiet PR re-hydrates on the next discovery instead of
// waiting out its reuse age.
func TestFetch_FingerprintChangeRehydratesQuietPR(t *testing.T) {
	t.Parallel()

	spec := quietSpec(1, 48*time.Hour)
	spec.rollupState = "FAILURE"
	fake, clk, src := pacedFake(t, spec)

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(1), fake.hydrateCalls.Load())

	clk.Advance(source.DefaultDiscoveryInterval)
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "an unchanged quiet PR is reused")

	spec.rollupState = "SUCCESS"
	fake.searches["review-requested:@me"] = discoverPRs([]prSpec{spec})
	fake.prs["org/repo"][1] = spec.hydrateJSON()

	clk.Advance(source.DefaultDiscoveryInterval)
	q, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "a moved fingerprint forces re-hydration")
	require.Len(t, q.Inbox, 1)
	assert.Equal(t, "SUCCESS", q.Inbox[0].Checks.State)
}

// Discovery backs off toward the idle interval while nothing changes, and a
// forced refresh snaps it back to the base interval.
func TestFetch_DiscoveryBacksOffWhenIdle(t *testing.T) {
	t.Parallel()

	fake, clk, src := pacedFake(t, quietSpec(1, 40*24*time.Hour))

	var waits []time.Duration
	for range 7 {
		_, err := src.Fetch(context.Background())
		require.NoError(t, err)
		wait := src.NextFetch().Sub(clk.Now())
		waits = append(waits, wait)
		clk.Advance(wait)
	}
	m := time.Minute
	assert.Equal(t, []time.Duration{m, m, m, m, 2 * m, 3 * m, 3 * m}, waits)
	assert.Equal(t, int32(7), fake.discoveryCalls.Load())

	_, err := src.Fetch(forced())
	require.NoError(t, err)
	assert.Equal(t, m, src.NextFetch().Sub(clk.Now()), "a forced refresh resets idle backoff")
}

// While the hourly budget runs low, every cadence stretches.
func TestFetch_LowBudgetStretchesCadence(t *testing.T) {
	t.Parallel()

	fake, clk, src := pacedFake(t, hotSpec())
	fake.rateLimitRemaining = 100 // 2% of 5000
	fake.rateLimitResetAt = clk.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	clk.Advance(source.DefaultHotInterval)
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3*source.DefaultHotInterval, src.NextFetch().Sub(clk.Now()),
		"the hot lane slows threefold on a low budget")
}

// Focused and Priority PRs (boosted) refresh a tier sooner than their
// activity alone warrants.
func TestFetch_BoostedPRRefreshesSooner(t *testing.T) {
	t.Parallel()

	boosted := quietSpec(1, 3*time.Hour)
	plain := quietSpec(2, 3*time.Hour)
	fake, clk, src := pacedFake(t, boosted, plain)

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	fake.resetHydrated()

	clk.Advance(3 * time.Minute)
	ctx := source.WithBoosted(context.Background(), map[model.PRKey]bool{{Repo: "org/repo", Number: 1}: true})
	_, err = src.Fetch(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"org/repo#1"}, fake.hydratedIDs())
}

// Vanished-PR state lookups batch many PRs into few requests; PRs GitHub no
// longer returns are absent.
func TestPRStates_Batches(t *testing.T) {
	t.Parallel()

	fake, _, src := pacedFake(t, quietSpec(2, time.Hour))
	fake.prStates = map[string]string{"org/repo#1": "MERGED"}

	states, err := src.PRStates(context.Background(), []model.PRKey{
		{Repo: "org/repo", Number: 1},
		{Repo: "org/repo", Number: 2},
		{Repo: "org/repo", Number: 3},
	})
	require.NoError(t, err)
	assert.Equal(t, map[model.PRKey]string{
		{Repo: "org/repo", Number: 1}: "MERGED",
		{Repo: "org/repo", Number: 2}: "OPEN",
	}, states)
	assert.Equal(t, int32(1), fake.statesCalls.Load())

	keys := make([]model.PRKey, 51)
	for i := range keys {
		keys[i] = model.PRKey{Repo: "org/repo", Number: 100 + i}
	}
	_, err = src.PRStates(context.Background(), keys)
	require.NoError(t, err)
	assert.Equal(t, int32(3), fake.statesCalls.Load(), "51 lookups take two requests")
}
