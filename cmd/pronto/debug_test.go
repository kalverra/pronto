package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/logging"
	"github.com/kalverra/pronto/internal/source"
	"github.com/kalverra/pronto/internal/tui"
)

func TestRun_Debug_ActivatesAllProfilingAndPrintsInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-debug")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	logPath := filepath.Join(dir, "pronto.jsonl")
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("PRONTO_CACHE_DIR", dir)
	t.Setenv("PRONTO_LOG_FILE", logPath)
	t.Setenv("PRONTO_PPROF_ADDR", "localhost:0")
	require.Equal(t, logPath, logging.LogPath())

	socketPath := filepath.Join(dir, "pronto.sock")
	var stdout, stderr syncBuffer

	oldRunTUI := runTUI
	runTUI = func(ctx context.Context, _ source.Source, _ cache.Store, _ ...tui.Option) error {
		<-ctx.Done()
		return nil
	}
	t.Cleanup(func() { runTUI = oldRunTUI })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{"--socket", socketPath, "--debug"}, &scriptedSource{
			results: []fetchResult{{queue: testQueue()}},
		}, &stdout, &stderr)
	}()

	// 1. Verify debug info printed to stderr includes logs, endpoints, and profile instructions
	addrRe := regexp.MustCompile(`(http://\S+)/debug/pprof/heap`)
	var pprofBase string
	require.Eventually(t, func() bool {
		out := stderr.String()
		if m := addrRe.FindStringSubmatch(out); m != nil {
			pprofBase = m[1]
			return true
		}
		return false
	}, 5*time.Second, 5*time.Millisecond, "debug output must contain pprof endpoint info; got: %s", stderr.String())

	out := stderr.String()
	assert.Contains(t, out, logPath, "must display log file path")
	assert.Contains(t, out, "DEBUG", "must indicate log level is DEBUG")
	assert.Contains(t, out, "/debug/pprof/goroutineleak", "must display goroutineleak endpoint")
	assert.Contains(t, out, "/debug/pprof/heap", "must display heap endpoint")
	assert.Contains(t, out, "/debug/pprof/profile", "must display profile endpoint")
	assert.Contains(t, out, "/debug/pprof/goroutine", "must display goroutine endpoint")
	assert.Contains(t, out, "go tool pprof", "must display how to read profiles")
	assert.Contains(t, out, "cpu.pprof", "must display cpu profile path")
	assert.Contains(t, out, "mem.pprof", "must display mem profile path")

	// 2. Verify live HTTP pprof endpoints are answering
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, pprofBase+"/debug/pprof/goroutineleak", nil)
	require.NoError(t, err)
	req.Close = true
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "goroutineleak endpoint must answer")

	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, pprofBase+"/debug/pprof/heap", nil)
	require.NoError(t, err)
	req.Close = true
	resp, err = (&http.Client{}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "heap endpoint must answer")

	// 3. Verify logs written at DEBUG level
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("pronto did not exit after context cancel")
	}

	assert.FileExists(t, logPath, "log file must exist")
	logContent, err := os.ReadFile(logPath) //nolint:gosec // test-owned temp paths
	require.NoError(t, err)
	assert.NotEmpty(t, logContent, "log file must contain logs")

	// 4. Verify exit profiles were written
	cpuPath := filepath.Join(dir, "cpu.pprof")
	memPath := filepath.Join(dir, "mem.pprof")
	for _, path := range []string{cpuPath, memPath} {
		data, err := os.ReadFile(path) //nolint:gosec // test-owned temp paths
		require.NoError(t, err, "%s must exist after exit", path)
		require.NotEmpty(t, data, "%s must not be empty", path)
		assert.Equal(t, []byte{0x1f, 0x8b}, data[:2], "%s must be gzip (pprof format)", path)
	}
}

func TestRun_Serve_Debug(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-serve-debug")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("PRONTO_CACHE_DIR", dir)
	t.Setenv("PRONTO_PPROF_ADDR", "localhost:0")

	socketPath := filepath.Join(dir, "pronto.sock")
	var stdout, stderr syncBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{"serve", "--socket", socketPath, "--interval", "10s", "--debug"}, &scriptedSource{
			results: []fetchResult{{queue: testQueue()}},
		}, &stdout, &stderr)
	}()

	addrRe := regexp.MustCompile(`(http://\S+)/debug/pprof/heap`)
	var pprofBase string
	require.Eventually(t, func() bool {
		out := stderr.String()
		if m := addrRe.FindStringSubmatch(out); m != nil {
			pprofBase = m[1]
			return true
		}
		return false
	}, 5*time.Second, 5*time.Millisecond, "serve --debug must output pprof info")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, pprofBase+"/debug/pprof/heap", nil)
	require.NoError(t, err)
	req.Close = true
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "heap endpoint must answer")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not exit after context cancel")
	}

	cpuPath := filepath.Join(dir, "cpu.pprof")
	memPath := filepath.Join(dir, "mem.pprof")
	for _, path := range []string{cpuPath, memPath} {
		data, err := os.ReadFile(path) //nolint:gosec // test-owned temp paths
		require.NoError(t, err, "%s must exist after serve exits", path)
		assert.Equal(t, []byte{0x1f, 0x8b}, data[:2], "%s must be gzip format", path)
	}
}

func TestRun_Debug_CustomProfilePaths(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-custom-prof")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("PRONTO_CACHE_DIR", dir)
	t.Setenv("PRONTO_PPROF_ADDR", "localhost:0")

	socketPath := filepath.Join(dir, "pronto.sock")
	customCPU := filepath.Join(dir, "my_cpu.pprof")
	customMem := filepath.Join(dir, "my_mem.pprof")
	var stdout, stderr syncBuffer

	oldRunTUI := runTUI
	runTUI = func(ctx context.Context, _ source.Source, _ cache.Store, _ ...tui.Option) error {
		<-ctx.Done()
		return nil
	}
	t.Cleanup(func() { runTUI = oldRunTUI })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{
			"--socket", socketPath,
			"--debug",
			"--cpu-profile", customCPU,
			"--mem-profile", customMem,
		}, &scriptedSource{
			results: []fetchResult{{queue: testQueue()}},
		}, &stdout, &stderr)
	}()

	require.Eventually(t, func() bool {
		return strings.Contains(stderr.String(), customCPU) && strings.Contains(stderr.String(), customMem)
	}, 5*time.Second, 5*time.Millisecond, "debug output must show custom profile paths")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("pronto did not exit after context cancel")
	}

	assert.FileExists(t, customCPU)
	assert.FileExists(t, customMem)
}
