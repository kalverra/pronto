package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// defaultFallbackRetryAfter is how long a FallbackNotifier keeps bypassing a
// degraded primary before trying it again, so a mid-session
// `pronto notify setup` is picked up without restarting pronto.
const defaultFallbackRetryAfter = 5 * time.Minute

// FallbackNotifier delivers through primary and, when primary reports a
// setup problem (ErrHelperNotFound, ErrNotAuthorized, ErrDenied), through
// fallback instead. Other primary errors are returned as-is: they may have
// posted a banner already, and a duplicate is worse than a logged failure.
//
// After a fallback, primary is skipped until the retry window passes. Health
// reports why, so callers can tell the user how to fix native delivery.
type FallbackNotifier struct {
	primary    Notifier
	fallback   Notifier
	retryAfter time.Duration
	now        func() time.Time

	mu       sync.Mutex
	reason   error
	since    time.Time
	advisory error
}

// FallbackOption configures a FallbackNotifier.
type FallbackOption func(*FallbackNotifier)

// WithFallbackRetryAfter sets how long a degraded primary is bypassed.
func WithFallbackRetryAfter(d time.Duration) FallbackOption {
	return func(f *FallbackNotifier) {
		f.retryAfter = d
	}
}

// WithFallbackClock overrides the clock for testing.
func WithFallbackClock(now func() time.Time) FallbackOption {
	return func(f *FallbackNotifier) {
		f.now = now
	}
}

// WithFallbackReason starts the notifier degraded (e.g. the helper is known
// to be missing), skipping primary for the first retry window.
func WithFallbackReason(err error) FallbackOption {
	return func(f *FallbackNotifier) {
		f.reason = err
	}
}

// WithFallbackAdvisory sets a non-blocking Health problem (e.g. a stale
// helper that still delivers) reported while primary is healthy.
func WithFallbackAdvisory(err error) FallbackOption {
	return func(f *FallbackNotifier) {
		f.advisory = err
	}
}

// NewFallbackNotifier creates a FallbackNotifier over primary and fallback.
func NewFallbackNotifier(primary, fallback Notifier, opts ...FallbackOption) *FallbackNotifier {
	f := &FallbackNotifier{
		primary:    primary,
		fallback:   fallback,
		retryAfter: defaultFallbackRetryAfter,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(f)
	}
	if f.reason != nil {
		f.since = f.now()
	}
	return f
}

// Notify delivers n through primary, or fallback when primary is degraded.
func (f *FallbackNotifier) Notify(ctx context.Context, n Notification) error {
	if reason := f.activeReason(); reason != nil {
		return f.deliverFallback(ctx, n, reason)
	}

	err := f.primary.Notify(ctx, n)
	if err == nil {
		f.setReason(nil)
		return nil
	}
	if !fallbackWorthy(err) {
		return err
	}
	f.setReason(err)
	return f.deliverFallback(ctx, n, err)
}

// Health reports why primary is currently bypassed, else the advisory, else
// nil.
func (f *FallbackNotifier) Health() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reason != nil {
		return f.reason
	}
	return f.advisory
}

// activeReason returns the degraded reason while inside the retry window.
func (f *FallbackNotifier) activeReason() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reason == nil || f.now().Sub(f.since) >= f.retryAfter {
		return nil
	}
	return f.reason
}

func (f *FallbackNotifier) setReason(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reason = err
	f.since = f.now()
}

func (f *FallbackNotifier) deliverFallback(ctx context.Context, n Notification, reason error) error {
	if err := f.fallback.Notify(ctx, n); err != nil {
		return fmt.Errorf("%w; fallback delivery failed: %w", reason, err)
	}
	return nil
}

// fallbackWorthy reports whether err means primary is not set up, as opposed
// to a delivery that may or may not have posted.
func fallbackWorthy(err error) bool {
	return errors.Is(err, ErrHelperNotFound) || errors.Is(err, ErrNotAuthorized) || errors.Is(err, ErrDenied)
}
