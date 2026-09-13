package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
)

func TestRun_Serve_PProfEndpoint(t *testing.T) {
	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-pprof")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("PRONTO_PPROF_ADDR", "localhost:0")

	socketPath := filepath.Join(dir, "pronto.sock")
	var stdout, stderr syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{"serve", "--socket", socketPath, "--interval", "10s"}, &scriptedSource{
			results: []fetchResult{{queue: testQueue()}},
		}, &stdout, &stderr)
	}()

	addrRe := regexp.MustCompile(`pprof listening on (http://\S+)`)
	var pprofURL string
	require.Eventually(t, func() bool {
		if m := addrRe.FindStringSubmatch(stderr.String()); m != nil {
			pprofURL = m[1]
			return true
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "serve must log the bound pprof URL")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, pprofURL+"/debug/pprof/goroutineleak", nil)
	require.NoError(t, err)
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "goroutineleak endpoint must answer")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not exit after context cancel")
	}
}

func TestRun_Serve_ProfileFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-prof")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("PRONTO_CONFIG_DIR", dir)

	socketPath := filepath.Join(dir, "pronto.sock")
	cpuPath := filepath.Join(dir, "cpu.pprof")
	memPath := filepath.Join(dir, "mem.pprof")

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{
			"serve", "--socket", socketPath, "--interval", "10s",
			"--cpu-profile", cpuPath,
			"--mem-profile", memPath,
		}, &scriptedSource{
			results: []fetchResult{{queue: testQueue()}},
		}, &stdout, &stderr)
	}()

	dialOK := waitForSocket(t, socketPath)
	require.True(t, dialOK, "serve socket must accept connections")
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not exit after context cancel")
	}

	for _, path := range []string{cpuPath, memPath} {
		data, err := os.ReadFile(path) //nolint:gosec // test-owned temp paths
		require.NoError(t, err, "%s must exist after serve exits", path)
		require.NotEmpty(t, data, "%s must not be empty", path)
		assert.Equal(t, []byte{0x1f, 0x8b}, data[:2], "%s must be gzip (pprof format)", path)
	}
}

func testQueue() model.Queue {
	return model.Queue{Viewer: "kalverra"}
}

// waitForSocket polls until the unix socket at path accepts a connection.
func waitForSocket(t *testing.T, path string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	dialer := &net.Dialer{Timeout: 250 * time.Millisecond}
	for time.Now().Before(deadline) {
		conn, err := dialer.DialContext(t.Context(), "unix", path)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
