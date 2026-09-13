package profiling

import (
	"testing"

	"go.uber.org/goleak"
)

// DeferNoLeaks registers a cleanup that fails t when goroutines created during
// the test (or its subtests) outlive it. Unlike the runtime goroutine leak
// profiler used by LeakCheckMain, goleak also catches goroutines blocked on
// IO, at the cost of possible false positives on by-design background
// goroutines — pass goleak.IgnoreTopFunction / goleak.IgnoreAnyFunction options
// for those.
func DeferNoLeaks(t *testing.T, opts ...goleak.Option) {
	t.Helper()
	t.Cleanup(func() {
		goleak.VerifyNone(t, opts...)
	})
}
