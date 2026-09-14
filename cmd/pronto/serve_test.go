package main

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/model"
)

// newServeFloorDir returns a short-lived temp dir suitable for a unix socket
// path (t.TempDir paths exceed macOS's 104-byte limit).
func newServeFloorDir(t *testing.T) string {
	t.Helper()
	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-serve-floor")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// An --interval below the floor must fail fast: a 1s poller exhausts GitHub's
// 5,000 points/hour GraphQL budget within minutes.
func TestRun_Serve_RejectsIntervalBelowFloor(t *testing.T) {
	t.Setenv("PRONTO_CONFIG_DIR", newServeFloorDir(t))

	err := Run(context.Background(), []string{"serve", "--interval", "1s"}, &scriptedSource{
		results: []fetchResult{{queue: model.Queue{Viewer: "kalverra"}}},
	}, &bytes.Buffer{}, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "10s")
}

// server.poll_interval below the floor must fail the same way the flag does.
func TestRun_Serve_RejectsConfigPollIntervalBelowFloor(t *testing.T) {
	t.Setenv("PRONTO_CONFIG_DIR", newServeFloorDir(t))
	t.Setenv("PRONTO_POLL_INTERVAL", "1s")

	err := Run(context.Background(), []string{"serve"}, &scriptedSource{
		results: []fetchResult{{queue: model.Queue{Viewer: "kalverra"}}},
	}, &bytes.Buffer{}, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "10s")
}

// clampPollInterval lifts sub-floor intervals to the floor for the embedded
// TUI daemon, where failing to start is worse than polling slightly faster
// than intended.
func TestClampPollInterval(t *testing.T) {
	t.Parallel()

	got, clamped := clampPollInterval(time.Second)
	assert.Equal(t, daemon.MinInterval, got)
	assert.True(t, clamped)

	got, clamped = clampPollInterval(daemon.MinInterval)
	assert.Equal(t, daemon.MinInterval, got)
	assert.False(t, clamped)

	got, clamped = clampPollInterval(5 * time.Minute)
	assert.Equal(t, 5*time.Minute, got)
	assert.False(t, clamped)
}
