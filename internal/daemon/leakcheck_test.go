package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/model"
)

// fakeLeakChecker scripts leak counts and records Dump calls.
type fakeLeakChecker struct {
	mu        sync.Mutex
	leaks     int
	err       error
	leakCalls int
	dumped    []string
	dumpErr   error
}

func (f *fakeLeakChecker) Leaked() (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leakCalls++
	return f.leaks, f.err
}

func (f *fakeLeakChecker) Dump(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dumped = append(f.dumped, path)
	if f.dumpErr != nil {
		return f.dumpErr
	}
	return os.WriteFile(path, []byte("fake-leak-profile"), 0o600)
}

func (f *fakeLeakChecker) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leakCalls
}

func (f *fakeLeakChecker) dumpPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dumped...)
}

// syncBuffer is a mutex-guarded buffer for capturing daemon logs written
// from the Run goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func runLeakDaemon(t *testing.T, opts daemon.Options) context.CancelFunc {
	t.Helper()
	opts.SocketPath = "" // no socket: keeps the leak-check loop the only timer
	if opts.Interval == 0 {
		opts.Interval = time.Hour // poll loop never ticks
	}
	d := daemon.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = d.Run(ctx) }()
	return cancel
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemon_LeakCheck_DetectsAndDumps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dumpDir := t.TempDir()
		checker := &fakeLeakChecker{leaks: 3}
		var logs syncBuffer

		cancel := runLeakDaemon(t, daemon.Options{
			Source:            &scriptedSource{results: []fetchResult{{queue: model.Queue{Viewer: "kalverra"}}}},
			LeakCheckInterval: 20 * time.Millisecond,
			LeakChecker:       checker,
			LeakDumpDir:       dumpDir,
			Logger:            zerolog.New(&logs),
		})
		defer cancel()

		time.Sleep(25 * time.Millisecond)

		entries, err := os.ReadDir(dumpDir)
		require.NoError(t, err)
		require.NotEmpty(t, entries, "a leak profile dump must be written")
		for _, e := range entries {
			assert.Regexp(t, `^goroutineleak-\d{8}-\d{6}\.pprof$`, e.Name(), "dump file name must be timestamped")
		}

		assert.Contains(t, logs.String(), "goroutine leak detected", "leak must be logged as a warning")
	})
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemon_LeakCheck_NoLeaksNoDump(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dumpDir := t.TempDir()
		checker := &fakeLeakChecker{leaks: 0}

		cancel := runLeakDaemon(t, daemon.Options{
			Source:            &scriptedSource{results: []fetchResult{{queue: model.Queue{Viewer: "kalverra"}}}},
			LeakCheckInterval: 20 * time.Millisecond,
			LeakChecker:       checker,
			LeakDumpDir:       dumpDir,
			Logger:            zerolog.Nop(),
		})
		defer cancel()

		time.Sleep(65 * time.Millisecond)

		assert.GreaterOrEqual(t, checker.calls(), 3, "leak check must run on its interval")

		entries, err := os.ReadDir(dumpDir)
		require.NoError(t, err)
		assert.Empty(t, entries, "no leaks must mean no dump files")
	})
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemon_LeakCheck_DisabledWhenIntervalZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		checker := &fakeLeakChecker{leaks: 3}

		cancel := runLeakDaemon(t, daemon.Options{
			Source:            &scriptedSource{results: []fetchResult{{queue: model.Queue{Viewer: "kalverra"}}}},
			LeakCheckInterval: 0,
			LeakChecker:       checker,
			LeakDumpDir:       t.TempDir(),
			Logger:            zerolog.Nop(),
		})
		defer cancel()

		time.Sleep(150 * time.Millisecond)
		assert.Zero(t, checker.calls(), "interval 0 must disable leak checks entirely")
	})
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemon_LeakCheck_PrunesOldDumps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dumpDir := t.TempDir()
		stale := filepath.Join(dumpDir, "goroutineleak-19991201-000000.pprof")
		require.NoError(t, os.WriteFile(stale, []byte("old"), 0o600))
		oldTime := time.Now().Add(-8 * 24 * time.Hour)
		require.NoError(t, os.Chtimes(stale, oldTime, oldTime))

		fresh := filepath.Join(dumpDir, "unrelated.txt")
		require.NoError(t, os.WriteFile(fresh, []byte("keep"), 0o600))

		checker := &fakeLeakChecker{leaks: 1}

		cancel := runLeakDaemon(t, daemon.Options{
			Source:            &scriptedSource{results: []fetchResult{{queue: model.Queue{Viewer: "kalverra"}}}},
			LeakCheckInterval: 20 * time.Millisecond,
			LeakChecker:       checker,
			LeakDumpDir:       dumpDir,
			Logger:            zerolog.Nop(),
		})
		defer cancel()

		time.Sleep(25 * time.Millisecond)

		assert.NotEmpty(t, checker.dumpPaths(), "leak dump must be written")

		_, statErr := os.Stat(stale)
		assert.True(t, os.IsNotExist(statErr), "dumps older than the retention window must be pruned")

		_, err := os.Stat(fresh)
		require.NoError(t, err, "unrelated files must never be pruned")
		assert.NoFileExists(t, stale)
	})
}
