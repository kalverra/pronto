package source

import (
	"context"
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// Source defines the central seam for fetching pull request queues.
type Source interface {
	Fetch(ctx context.Context) (model.Queue, error)
}

// Pacer is implemented by sources that schedule their own refresh work.
// NextFetch reports when the next Fetch will have work to do; a poller should
// sleep until then rather than use a fixed interval.
type Pacer interface {
	NextFetch() time.Time
}

// ProgressFunc receives progress updates as (loaded, total).
type ProgressFunc func(loaded, total int)

type (
	progressKey struct{}
	forceKey    struct{}
	boostedKey  struct{}
)

// WithProgress returns a Context carrying a ProgressFunc.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, progressKey{}, fn)
}

// ProgressFromContext retrieves the ProgressFunc from ctx, or nil if not present.
func ProgressFromContext(ctx context.Context) ProgressFunc {
	if fn, ok := ctx.Value(progressKey{}).(ProgressFunc); ok {
		return fn
	}
	return nil
}

// WithForceRefresh marks a Fetch as user-initiated: a pacing source searches
// for new and changed PRs immediately, re-hydrates its hot PRs, and resets
// any idle backoff.
func WithForceRefresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceKey{}, true)
}

// ForceRefreshFromContext reports whether ctx carries WithForceRefresh.
func ForceRefreshFromContext(ctx context.Context) bool {
	force, _ := ctx.Value(forceKey{}).(bool)
	return force
}

// WithBoosted carries the keys of PRs the user cares most about (focused or
// in the Priority tab); a pacing source refreshes them one tier sooner than
// their activity alone warrants.
func WithBoosted(ctx context.Context, keys map[model.PRKey]bool) context.Context {
	if len(keys) == 0 {
		return ctx
	}
	return context.WithValue(ctx, boostedKey{}, keys)
}

// BoostedFromContext returns the keys carried by WithBoosted, or nil.
func BoostedFromContext(ctx context.Context) map[model.PRKey]bool {
	keys, _ := ctx.Value(boostedKey{}).(map[model.PRKey]bool)
	return keys
}
