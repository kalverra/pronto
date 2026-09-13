package source

import (
	"context"

	"github.com/kalverra/pronto/internal/model"
)

// Source defines the central seam for fetching pull request queues.
type Source interface {
	Fetch(ctx context.Context) (model.Queue, error)
}

// ProgressFunc receives progress updates as (loaded, total).
type ProgressFunc func(loaded, total int)

type progressKey struct{}

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
