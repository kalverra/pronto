// Package profiling exposes the pprof and goroutine leak profile machinery
// (Go 1.27+) behind one small API so the serve command, the daemon, and
// package TestMains all share a single wiring surface.
package profiling

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	//nolint:gosec // the pprof endpoint is opt-in via server.pprof_addr and documented as loopback-only
	_ "net/http/pprof"
	"os"
	"runtime/pprof"
	"testing"

	"go.uber.org/goleak"
)

// StartHTTP launches a pprof HTTP endpoint (including /debug/pprof/goroutineleak)
// on addr. It returns the bound listener — addr may be "localhost:0" — and a
// stop function that shuts the server down. addr must be a loopback address;
// pronto is a local tool and the endpoint is unauthenticated.
func StartHTTP(addr string) (ln net.Listener, stop func() error, err error) {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen pprof: %w", err)
	}
	srv := &http.Server{Handler: http.DefaultServeMux}
	go func() { _ = srv.Serve(l) }()
	return l, srv.Close, nil
}

// LeakCount reports the number of leaked goroutines found by the runtime
// goroutine leak profiler (Go 1.27+). Detection runs as a GC cycle, and only
// profile collection triggers it, so a discarded profile write primes the
// count before it is read.
func LeakCount() (int, error) {
	prof := pprof.Lookup("goroutineleak")
	if prof == nil {
		return 0, errors.New("goroutineleak profile unavailable")
	}
	if err := prof.WriteTo(io.Discard, 0); err != nil {
		return 0, fmt.Errorf("collect goroutine leak profile: %w", err)
	}
	return prof.Count(), nil
}

// WriteLeakProfile writes the goroutine leak profile to path in pprof format.
func WriteLeakProfile(path string) error {
	prof := pprof.Lookup("goroutineleak")
	if prof == nil {
		return errors.New("goroutineleak profile unavailable")
	}
	f, err := os.Create(path) //nolint:gosec // path is a dump path computed by the caller
	if err != nil {
		return fmt.Errorf("create leak profile %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return prof.WriteTo(f, 0)
}

// StartCPUProfile begins CPU profiling to w; StopCPUProfile finishes it.
func StartCPUProfile(w *os.File) error {
	return pprof.StartCPUProfile(w)
}

// StopCPUProfile finishes a CPU profile started by StartCPUProfile.
func StopCPUProfile() {
	pprof.StopCPUProfile()
}

// WriteHeapProfile writes a heap profile to w in pprof format.
func WriteHeapProfile(w *os.File) error {
	return pprof.WriteHeapProfile(w)
}

// LeakCheckMain wraps testing.Main with two complementary leak checks:
//
//  1. goleak's suite-level find, which diffs goroutines against a baseline
//     taken before the suite and also catches goroutines blocked on IO (which
//     the runtime profiler cannot see).
//  2. the Go 1.27 runtime goroutine leak profile, which reports goroutines
//     provably leaked on channels/sync primitives with no false positives on
//     by-design background goroutines.
//
// Both run after every test has finished, so t.Parallel tests are safe.
// Options are forwarded to goleak (e.g. goleak.IgnoreTopFunction for known
// background goroutines).
func LeakCheckMain(m *testing.M, opts ...goleak.Option) {
	code := m.Run()
	if code == 0 {
		if err := goleak.Find(opts...); err != nil {
			fmt.Fprintf(os.Stderr, "profiling: leaked goroutines after test suite:\n%v\n", err)
			code = 1
		} else if n, err := LeakCount(); err != nil {
			fmt.Fprintf(os.Stderr, "profiling: leak check failed: %v\n", err)
			code = 1
		} else if n > 0 {
			fmt.Fprintf(os.Stderr, "profiling: %d goroutine(s) leaked after test suite\n", n)
			if prof := pprof.Lookup("goroutineleak"); prof != nil {
				_ = prof.WriteTo(os.Stderr, 1)
			}
			code = 1
		}
	}
	os.Exit(code)
}

// RuntimeLeakChecker implements the daemon.LeakChecker seam using the Go 1.27
// runtime goroutine leak profiler.
type RuntimeLeakChecker struct{}

// Leaked reports the number of leaked goroutines.
func (RuntimeLeakChecker) Leaked() (int, error) {
	return LeakCount()
}

// Dump writes the goroutine leak profile to path in pprof format.
func (RuntimeLeakChecker) Dump(path string) error {
	return WriteLeakProfile(path)
}
