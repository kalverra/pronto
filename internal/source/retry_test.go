package source_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/source"
)

func onePRFake(failures, failCode int32) *fakeGitHub {
	spec := prSpec{
		num: 1, title: "Solo PR", repo: "org/repo", author: "alice", oid: "oid1",
		additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
		mergeable: "MERGEABLE", mergeState: "CLEAN",
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}
	fake.hydrateFailures.Store(failures)
	fake.hydrateFailCode.Store(failCode)
	return fake
}

// A transient 502 on a hydrate batch must be retried transparently: the fetch
// succeeds and the PR set is complete.
func TestRetry_Transient502OnHydrateRetries(t *testing.T) {
	t.Parallel()

	fake := onePRFake(1, 0)

	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(client, source.WithRetryBaseDelay(time.Millisecond))
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 1)
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "failed attempt must be retried exactly once")
}

// Persistent 5xx failures surface as an error after the attempt budget is
// spent, not an infinite retry loop.
func TestRetry_Exhausted502SurfacesError(t *testing.T) {
	t.Parallel()

	fake := onePRFake(100, 0)

	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(3),
		source.WithRetryBaseDelay(time.Millisecond),
	)
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hydrate PR batch")
	assert.Equal(t, int32(3), fake.hydrateCalls.Load(), "must stop after 3 total attempts")
}

// Non-retryable HTTP failures must surface immediately without burning retries.
func TestRetry_NonRetryableErrorSurfacesImmediately(t *testing.T) {
	t.Parallel()

	fake := onePRFake(100, http.StatusBadRequest)

	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(3),
		source.WithRetryBaseDelay(time.Millisecond),
	)
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hydrate PR batch")
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "4xx must not be retried")
}

// A 403 carrying a Retry-After header is a GitHub secondary rate limit, which
// is transient: it must be retried (honoring the header), unlike auth 403s.
func TestRetry_SecondaryRateLimit403Retries(t *testing.T) {
	t.Parallel()

	fake := onePRFake(1, http.StatusForbidden)
	fake.hydrateFailRetryAft.Store(true)

	var slept time.Duration
	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(3),
		source.WithRetryBaseDelay(time.Millisecond),
		source.WithRetrySleep(func(_ context.Context, d time.Duration) error {
			slept = d
			return nil
		}),
	)
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 1)
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "rate-limited attempt must be retried exactly once")
	assert.Equal(t, time.Second, slept, "must sleep for Retry-After duration")
}

// A bare 403 (e.g. bad token) must not be retried.
func TestRetry_Bare403SurfacesImmediately(t *testing.T) {
	t.Parallel()

	fake := onePRFake(100, http.StatusForbidden)

	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(3),
		source.WithRetryBaseDelay(time.Millisecond),
	)
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "403 without Retry-After must not be retried")
}

// Context cancellation during the backoff window must abort the retry loop.
func TestRetry_ContextCancellationAborts(t *testing.T) {
	t.Parallel()

	fake := onePRFake(100, 0)

	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(3),
		source.WithRetryBaseDelay(10*time.Second),
	)
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := src.Fetch(ctx)
	require.Error(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "must abort during backoff, not retry")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRetry_JitteredDelayStaysWithinBounds(t *testing.T) {
	t.Parallel()

	sawJittered := false
	base := 60 * time.Millisecond
	for range 8 {
		fake := onePRFake(1, 0)
		client := newTestGraphQLClient(t, fake.handler())
		var slept time.Duration
		retrying := source.NewRetryingGraphQLClient(
			client,
			source.WithRetryAttempts(2),
			source.WithRetryBaseDelay(base),
			source.WithRetryJitter(0.5),
			source.WithRetrySleep(func(_ context.Context, d time.Duration) error {
				slept = d
				return nil
			}),
		)
		src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

		queue, err := src.Fetch(context.Background())
		require.NoError(t, err)
		require.Len(t, queue.Inbox, 1)
		assert.GreaterOrEqual(t, slept, 30*time.Millisecond, "delay must not fall below (1 - fraction) * baseDelay")
		assert.LessOrEqual(t, slept, base, "delay must not exceed baseDelay")
		if slept < 55*time.Millisecond {
			sawJittered = true
		}
	}
	assert.True(t, sawJittered, "at least one jittered delay must be measurably below unjittered base delay")
}

func TestRetry_RetryAfterIsNeverShortenedByJitter(t *testing.T) {
	t.Parallel()

	fake := onePRFake(1, http.StatusForbidden)
	fake.hydrateFailRetryAft.Store(true)

	var slept time.Duration
	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(2),
		source.WithRetryBaseDelay(10*time.Millisecond),
		source.WithRetryJitter(0.9),
		source.WithRetrySleep(func(_ context.Context, d time.Duration) error {
			slept = d
			return nil
		}),
	)
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 1)
	assert.GreaterOrEqual(t, slept, 1*time.Second, "Retry-After of 1s must never be shortened by jitter")
}

func TestRetry_WithRetrySleepAbortsOnError(t *testing.T) {
	t.Parallel()

	fake := onePRFake(1, 0)
	client := newTestGraphQLClient(t, fake.handler())
	sleepErr := errors.New("sleep interrupted")
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(3),
		source.WithRetryBaseDelay(time.Millisecond),
		source.WithRetrySleep(func(_ context.Context, _ time.Duration) error {
			return sleepErr
		}),
	)
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.ErrorIs(t, err, sleepErr)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "sleep error must abort retry loop immediately")
}

func TestRetry_ContextOverridesAttempts(t *testing.T) {
	t.Parallel()

	fake := onePRFake(100, 0)
	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(5),
		source.WithRetryBaseDelay(time.Millisecond),
	)

	ctx := source.ContextWithRetryAttempts(context.Background(), 1)
	err := retrying.DoWithContext(ctx, "query Hydrate { viewer { login } }", nil, &struct{}{})
	require.Error(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "override attempts=1 must stop after 1 attempt")
}

// A 403 whose body names the secondary rate limit is account-global: the fetch
// must abort with a distinct error instead of retrying, bisecting, or dropping
// the PR, all of which burn more requests while limited.
func TestRetry_SecondaryRateLimitAbortsFetch(t *testing.T) {
	t.Parallel()

	fake := onePRFake(100, http.StatusForbidden)
	fake.hydrateFailSecondary.Store(true)

	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(3),
		source.WithRetryBaseDelay(time.Millisecond),
	)
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, source.ErrSecondaryRateLimit)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "must not retry while secondary-limited")
}

type syntheticTimeoutError struct{}

func (syntheticTimeoutError) Error() string   { return "i/o timeout" }
func (syntheticTimeoutError) Timeout() bool   { return true }
func (syntheticTimeoutError) Temporary() bool { return true }

type fakeSequenceClient struct {
	calls atomic.Int32
	errs  []error
}

func (f *fakeSequenceClient) DoWithContext(_ context.Context, _ string, _ map[string]any, _ any) error {
	idx := int(f.calls.Add(1)) - 1
	if idx < len(f.errs) {
		return f.errs[idx]
	}
	return nil
}

func TestRetry_NetworkErrorsRetried(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "net.Error timeout",
			err:  syntheticTimeoutError{},
		},
		{
			name: "connection reset",
			err:  &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET},
		},
		{
			name: "connection refused",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
		},
		{
			name: "unexpected EOF",
			err:  io.ErrUnexpectedEOF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeSequenceClient{
				errs: []error{tt.err},
			}
			retrying := source.NewRetryingGraphQLClient(
				fake,
				source.WithRetryAttempts(3),
				source.WithRetryBaseDelay(time.Millisecond),
			)

			err := retrying.DoWithContext(context.Background(), "query { viewer { login } }", nil, &struct{}{})
			require.NoError(t, err)
			assert.Equal(t, int32(2), fake.calls.Load(), "transient error must be retried and succeed on attempt 2")
		})
	}
}

func TestRetry_ExhaustedNetworkErrorSurfacesError(t *testing.T) {
	t.Parallel()

	fake := &fakeSequenceClient{
		errs: []error{
			syscall.ECONNRESET,
			syscall.ECONNRESET,
			syscall.ECONNRESET,
		},
	}
	retrying := source.NewRetryingGraphQLClient(
		fake,
		source.WithRetryAttempts(3),
		source.WithRetryBaseDelay(time.Millisecond),
	)

	err := retrying.DoWithContext(context.Background(), "query { viewer { login } }", nil, &struct{}{})
	require.Error(t, err)
	require.ErrorIs(t, err, syscall.ECONNRESET)
	assert.Equal(t, int32(3), fake.calls.Load(), "exhausted network error must stop after configured attempts")
}
