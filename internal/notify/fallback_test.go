package notify_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/notify"
)

// recordingNotifier counts deliveries and returns err (if set) from Notify.
type recordingNotifier struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (r *recordingNotifier) Notify(context.Context, notify.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.err
}

func (r *recordingNotifier) setErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *recordingNotifier) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestFallbackNotifier_PrimarySuccess(t *testing.T) {
	t.Parallel()

	primary, fallback := &recordingNotifier{}, &recordingNotifier{}
	f := notify.NewFallbackNotifier(primary, fallback)

	require.NoError(t, f.Notify(context.Background(), notify.Notification{}))
	assert.Equal(t, 1, primary.count())
	assert.Equal(t, 0, fallback.count())
	assert.NoError(t, f.Health())
}

func TestFallbackNotifier_FallsBackOnAuthorizationErrors(t *testing.T) {
	t.Parallel()

	for _, sentinel := range []error{notify.ErrNotAuthorized, notify.ErrDenied, notify.ErrHelperNotFound} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			t.Parallel()

			primary := &recordingNotifier{err: fmt.Errorf("native helper: %w", sentinel)}
			fallback := &recordingNotifier{}
			f := notify.NewFallbackNotifier(primary, fallback)

			require.NoError(t, f.Notify(context.Background(), notify.Notification{}),
				"a delivered fallback banner is a successful delivery")
			assert.Equal(t, 1, fallback.count())
			assert.ErrorIs(t, f.Health(), sentinel, "Health must explain why native was bypassed")
		})
	}
}

func TestFallbackNotifier_OtherErrorsDoNotFallBack(t *testing.T) {
	t.Parallel()

	boom := errors.New("helper crashed")
	primary, fallback := &recordingNotifier{err: boom}, &recordingNotifier{}
	f := notify.NewFallbackNotifier(primary, fallback)

	require.ErrorIs(t, f.Notify(context.Background(), notify.Notification{}), boom)
	assert.Equal(t, 0, fallback.count(), "an unclassified failure may have posted; never risk a duplicate banner")
	assert.NoError(t, f.Health())
}

func TestFallbackNotifier_FallbackErrorSurfaces(t *testing.T) {
	t.Parallel()

	primary := &recordingNotifier{err: notify.ErrDenied}
	fallback := &recordingNotifier{err: errors.New("terminal-notifier missing")}
	f := notify.NewFallbackNotifier(primary, fallback)

	err := f.Notify(context.Background(), notify.Notification{})
	require.Error(t, err)
	require.ErrorIs(t, err, notify.ErrDenied)
	assert.ErrorContains(t, err, "terminal-notifier missing")
}

func TestFallbackNotifier_StickyUntilRetryWindow(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	primary := &recordingNotifier{err: notify.ErrNotAuthorized}
	fallback := &recordingNotifier{}
	f := notify.NewFallbackNotifier(primary, fallback,
		notify.WithFallbackRetryAfter(time.Minute),
		notify.WithFallbackClock(func() time.Time { return now }),
	)
	ctx := context.Background()

	require.NoError(t, f.Notify(ctx, notify.Notification{}))
	require.NoError(t, f.Notify(ctx, notify.Notification{}))
	assert.Equal(t, 1, primary.count(), "degraded primary is skipped inside the retry window")
	assert.Equal(t, 2, fallback.count())

	// User runs `pronto notify setup` mid-session; next window retries native.
	primary.setErr(nil)
	now = now.Add(time.Minute)
	require.NoError(t, f.Notify(ctx, notify.Notification{}))
	assert.Equal(t, 2, primary.count())
	assert.Equal(t, 2, fallback.count())
	assert.NoError(t, f.Health(), "a successful native delivery clears the degraded state")
}

func TestFallbackNotifier_InitialReasonSkipsPrimary(t *testing.T) {
	t.Parallel()

	primary, fallback := &recordingNotifier{}, &recordingNotifier{}
	f := notify.NewFallbackNotifier(primary, fallback, notify.WithFallbackReason(notify.ErrHelperNotFound))

	require.ErrorIs(t, f.Health(), notify.ErrHelperNotFound)
	require.NoError(t, f.Notify(context.Background(), notify.Notification{}))
	assert.Equal(t, 0, primary.count())
	assert.Equal(t, 1, fallback.count())
}

func TestFallbackNotifier_AdvisoryDoesNotBypassPrimary(t *testing.T) {
	t.Parallel()

	primary, fallback := &recordingNotifier{}, &recordingNotifier{}
	f := notify.NewFallbackNotifier(primary, fallback, notify.WithFallbackAdvisory(notify.ErrHelperStale))

	require.NoError(t, f.Notify(context.Background(), notify.Notification{}))
	assert.Equal(t, 1, primary.count())
	require.ErrorIs(t, f.Health(), notify.ErrHelperStale, "stale helper still works but should be flagged")

	primary.setErr(notify.ErrDenied)
	require.NoError(t, f.Notify(context.Background(), notify.Notification{}))
	assert.ErrorIs(t, f.Health(), notify.ErrDenied, "a delivery-blocking reason outranks the advisory")
}
