package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/client"
	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/model"
)

func TestResolveServeSource(t *testing.T) {
	t.Parallel()

	// Injected source wins: tests and embedded wiring depend on it.
	injected := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra"}},
	}}
	s, _, err := resolveServeSource(injected, false, zerolog.Nop())
	require.NoError(t, err)
	assert.Same(t, injected, s)
}

func TestResolveServeSource_NeverProxiesDaemon(t *testing.T) {
	t.Parallel()

	// serve must fetch directly from GitHub; a second serve fails the bind
	// rather than silently becoming a proxy of the first.
	s, _, err := resolveServeSource(nil, false, zerolog.Nop())
	if err != nil {
		t.Skipf("cannot construct GraphQL client (no gh auth?): %v", err)
	}
	_, isProxy := s.(*client.QueueSource)
	assert.False(t, isProxy, "serve must never resolve to a daemon-backed source")
}

func TestResolveTUISource_ExistingDaemon_UsesClientMode(t *testing.T) {
	t.Parallel()

	socket := startDaemon(t, &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra"}},
	}})

	cmd := NewRootCmd(nil, nil, nil)
	require.NoError(t, cmd.PersistentFlags().Set("socket", socket))
	src, _, cleanup, err := resolveTUISource(context.Background(), cmd, nil, config.Config{})
	require.NoError(t, err)
	if cleanup != nil {
		defer cleanup()
	}

	require.NotNil(t, src)
	_, ok := src.(*client.QueueSource)
	assert.True(t, ok, "existing daemon must resolve to *client.QueueSource")
}

func TestResolveTUISource_NoDaemon_StartsEmbeddedDaemon(t *testing.T) {
	t.Parallel()

	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-emb")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "pronto.sock")

	scripted := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{testPR(1, "Embedded PR")}}},
	}}

	ctx := t.Context()

	cmd := NewRootCmd(nil, nil, nil)
	require.NoError(t, cmd.PersistentFlags().Set("socket", socket))
	src, _, cleanup, err := resolveTUISource(ctx, cmd, scripted, config.Config{})
	require.NoError(t, err)
	if cleanup != nil {
		defer cleanup()
	}

	require.NotNil(t, src)
	daemonSrc, ok := src.(*daemon.Source)
	assert.True(t, ok, "embedded daemon must resolve to *daemon.Source")
	assert.NotNil(t, daemonSrc.Daemon())

	// Socket must be bound and accepting connections from other clients
	conn, dialErr := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	require.NoError(t, dialErr, "embedded daemon must bind the socket")
	_ = conn.Close()

	// External client can query snapshot over socket
	c, dialErr := client.Dial(socket)
	require.NoError(t, dialErr)
	defer c.Close()

	snap, snapErr := c.Snapshot(context.Background())
	require.NoError(t, snapErr)
	assert.Equal(t, "kalverra", snap.Queue.Viewer)
}

func TestResolveTUISource_BindRace_FallsBackToClientMode(t *testing.T) {
	t.Parallel()

	// Start an existing daemon that already owns the socket
	socket := startDaemon(t, &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra"}},
	}})

	// Even if an injected source is provided (as if trying to start embedded),
	// detecting that another server owns the socket must fall back to client mode.
	scripted := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "other"}},
	}}

	cmd := NewRootCmd(nil, nil, nil)
	require.NoError(t, cmd.PersistentFlags().Set("socket", socket))
	src, _, cleanup, err := resolveTUISource(context.Background(), cmd, scripted, config.Config{})
	require.NoError(t, err)
	if cleanup != nil {
		defer cleanup()
	}

	require.NotNil(t, src)
	_, ok := src.(*client.QueueSource)
	assert.True(t, ok, "occupied socket must fall back to *client.QueueSource")
}

func TestResolveTUISource_Lifecycle_EmbeddedDaemonStopsWithContext(t *testing.T) {
	t.Parallel()

	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-emb-life")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "pronto.sock")

	scripted := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra"}},
	}}

	ctx, cancel := context.WithCancel(context.Background())

	cmd := NewRootCmd(nil, nil, nil)
	require.NoError(t, cmd.PersistentFlags().Set("socket", socket))
	src, _, cleanup, err := resolveTUISource(ctx, cmd, scripted, config.Config{})
	require.NoError(t, err)
	if cleanup != nil {
		defer cleanup()
	}
	require.NotNil(t, src)

	// Cancel context to simulate TUI exiting
	cancel()

	// Socket must be closed and removed cleanly
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(socket)
		return os.IsNotExist(statErr)
	}, 3*time.Second, 10*time.Millisecond, "socket file should be cleaned up on context cancel")
}
