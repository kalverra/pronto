package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/server"
)

// scriptedSource returns queued fetch results in order, then repeats the
// last result forever. If advance is non-nil, it holds at the first result
// until advance is closed.
type scriptedSource struct {
	mu      sync.Mutex
	results []fetchResult
	calls   int
	advance <-chan struct{}
}

type fetchResult struct {
	queue model.Queue
	err   error
}

func (s *scriptedSource) Fetch(context.Context) (model.Queue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.results) == 0 {
		return model.Queue{}, nil
	}
	if len(s.results) == 1 {
		return s.results[0].queue, s.results[0].err
	}
	if s.advance != nil {
		select {
		case <-s.advance:
		default:
			return s.results[0].queue, s.results[0].err
		}
	}
	idx := s.calls
	if idx >= len(s.results) {
		idx = len(s.results) - 1
	} else {
		s.calls++
	}
	r := s.results[idx]
	return r.queue, r.err
}

func testPR(num int, title string) model.PullRequest {
	return model.PullRequest{Number: num, Title: title, RepoNameWithOwner: "kalverra/pronto", Author: "kalverra"}
}

func ciRunning() model.ChecksSummary {
	return model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}
}

func ciFailed() model.ChecksSummary {
	return model.ChecksSummary{
		Total:             2,
		Failed:            1,
		Done:              2,
		ReqTotal:          2,
		ReqFailed:         1,
		ReqDone:           2,
		HasRequiredChecks: true,
	}
}

// syncBuffer is a concurrency-safe bytes.Buffer for capturing CLI output
// written from a goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
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

// startDaemonHarness runs a daemon with the given scripted source on a temp socket
// and returns the running daemon and socket path.
func startDaemonHarness(t *testing.T, src *scriptedSource) (*daemon.Daemon, string) {
	t.Helper()
	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp(
		"",
		"pronto",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "pronto.sock")

	d := daemon.New(daemon.Options{
		Source:     src,
		Interval:   10 * time.Millisecond,
		SocketPath: socket,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = d.Run(ctx) }()

	// Wait for the socket to accept connections, signaled by d.Ready().
	select {
	case <-d.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("daemon socket never became ready")
	}

	// Wait for the initial refresh to populate the queue snapshot.
	require.Eventually(t, func() bool {
		return !d.Snapshot().FetchedAt.IsZero()
	}, 3*time.Second, 5*time.Millisecond, "daemon initial refresh never completed")

	return d, socket
}

// startDaemon runs a daemon with the given scripted source on a temp socket
// and returns the socket path.
func startDaemon(t *testing.T, src *scriptedSource) string {
	t.Helper()
	_, socket := startDaemonHarness(t, src)
	return socket
}

func TestRun_APISchema(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"api", "schema"}, nil, &stdout, &stderr)
	require.NoError(t, err)

	// The command must print the embedded schema verbatim; its enums are
	// derived from the same code constants the drift tests pin, so this
	// guards the CLI against drifting from the protocol.
	var doc struct {
		Defs struct {
			Request struct {
				Properties struct {
					Method struct {
						Enum []string `json:"enum"`
					} `json:"method"`
				} `json:"properties"`
			} `json:"request"`
			Event struct {
				Properties struct {
					Type struct {
						Enum []string `json:"enum"`
					} `json:"type"`
				} `json:"properties"`
			} `json:"event"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &doc), "api schema must print the JSON Schema document")

	assert.ElementsMatch(t, server.Methods, doc.Defs.Request.Properties.Method.Enum,
		"schema method enum must match server.Methods")
	wantTypes := make([]string, 0, len(events.ValidTypes))
	for typ := range events.ValidTypes {
		wantTypes = append(wantTypes, string(typ))
	}
	assert.ElementsMatch(t, wantTypes, doc.Defs.Event.Properties.Type.Enum,
		"schema event type enum must match events.ValidTypes")
}

func TestRun_WaitUntilEvent(t *testing.T) {
	t.Parallel()

	running := testPR(42, "Feature A")
	running.Checks = ciRunning()
	failed := running
	failed.Checks = ciFailed()

	advance := make(chan struct{})
	src := &scriptedSource{
		results: []fetchResult{
			{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{running}}},
			{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{failed}}},
		},
		advance: advance,
	}
	d, socket := startDaemonHarness(t, src)

	done := make(chan error, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		done <- Run(
			context.Background(),
			[]string{"wait", "#42", "--until", "ci_failed", "--timeout", "10s", "--socket", socket},
			nil,
			&stdout,
			&stderr,
		)
	}()

	require.Eventually(t, func() bool {
		return d.Bus().SubscriberCount() > 0
	}, 3*time.Second, 5*time.Millisecond, "wait client never subscribed")

	close(advance)
	d.Refresh()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not exit after event emitted")
	}
	assert.Contains(t, stdout.String(), "ci_failed")
}

func TestRun_WaitUntilAlias(t *testing.T) {
	t.Parallel()

	running := testPR(42, "Feature A")
	running.Checks = ciRunning()
	failed := running
	failed.Checks = ciFailed()

	advance := make(chan struct{})
	src := &scriptedSource{
		results: []fetchResult{
			{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{running}}},
			{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{failed}}},
		},
		advance: advance,
	}
	d, socket := startDaemonHarness(t, src)

	done := make(chan error, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		done <- Run(
			context.Background(),
			[]string{"wait", "#42", "--until", "ci_settled", "--timeout", "10s", "--socket", socket},
			nil,
			&stdout,
			&stderr,
		)
	}()

	require.Eventually(t, func() bool {
		return d.Bus().SubscriberCount() > 0
	}, 3*time.Second, 5*time.Millisecond, "wait client never subscribed")

	close(advance)
	d.Refresh()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not exit after event emitted")
	}
	assert.Contains(t, stdout.String(), "ci_failed")
}

func TestRun_WaitTimeoutReturnsDistinctError(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	stable := testPR(42, "Feature A")
	stable.Checks = ciRunning()
	socket := startDaemon(t, &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{stable}}},
	}})

	var stdout, stderr bytes.Buffer
	err := Run(
		context.Background(),
		[]string{"wait", "#42", "--until", "pr_merged", "--timeout", "50ms", "--socket", socket},
		nil,
		&stdout,
		&stderr,
	)
	require.ErrorIs(t, err, ErrWaitTimeout)
}

func TestRun_WaitWithoutServer(t *testing.T) {
	t.Parallel()

	nonexistent := filepath.Join(t.TempDir(), "nonexistent.sock")

	var stdout, stderr bytes.Buffer
	err := Run(
		context.Background(),
		[]string{"wait", "#42", "--until", "merged", "--timeout", "1s", "--socket", nonexistent},
		nil,
		&stdout,
		&stderr,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pronto serve")
}

func TestRun_WaitInvalidUntil(t *testing.T) {
	t.Parallel()

	running := testPR(42, "Feature A")
	running.Checks = ciRunning()
	socket := startDaemon(t, &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{running}}},
	}})

	var stdout, stderr bytes.Buffer
	err := Run(
		context.Background(),
		[]string{"wait", "#42", "--until", "bogus", "--timeout", "1s", "--socket", socket},
		nil,
		&stdout,
		&stderr,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
}

func TestRun_WatchJSONStream(t *testing.T) {
	t.Parallel()

	running := testPR(42, "Feature A")
	running.Checks = ciRunning()
	failed := running
	failed.Checks = ciFailed()

	advance := make(chan struct{})
	src := &scriptedSource{
		results: []fetchResult{
			{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{running}}},
			{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{failed}}},
		},
		advance: advance,
	}
	d, socket := startDaemonHarness(t, src)

	var stdout syncBuffer
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{"watch", "--json", "--socket", socket}, nil, &stdout, &stderr)
	}()

	require.Eventually(t, func() bool {
		return d.Bus().SubscriberCount() > 0
	}, 3*time.Second, 5*time.Millisecond, "watch client never subscribed")

	close(advance)
	d.Refresh()

	require.Eventually(t, func() bool {
		return strings.Contains(stdout.String(), `"type":"ci_failed"`)
	}, 5*time.Second, 5*time.Millisecond, "watch must stream the ci_failed event as JSONL")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not exit after context cancel")
	}

	// Every output line is valid JSON.
	for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
		if line == "" {
			continue
		}
		var ev events.Event
		assert.NoError(t, json.Unmarshal([]byte(line), &ev), "line must be a JSON event: %s", line)
	}
}

func TestRun_WatchRefFilter(t *testing.T) {
	t.Parallel()

	a, b := testPR(42, "Feature A"), testPR(43, "Feature B")
	aRunning, bRunning := a, b
	aRunning.Checks, bRunning.Checks = ciRunning(), ciRunning()
	aFailed, bFailed := aRunning, bRunning
	aFailed.Checks, bFailed.Checks = ciFailed(), ciFailed()

	advance := make(chan struct{})
	src := &scriptedSource{
		results: []fetchResult{
			{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{aRunning, bRunning}}},
			{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{aFailed, bFailed}}},
		},
		advance: advance,
	}
	d, socket := startDaemonHarness(t, src)

	var stdout syncBuffer
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, []string{"watch", "#42", "--json", "--socket", socket}, nil, &stdout, &stderr)
	}()

	require.Eventually(t, func() bool {
		return d.Bus().SubscriberCount() > 0
	}, 3*time.Second, 5*time.Millisecond, "watch client never subscribed")

	close(advance)
	d.Refresh()

	require.Eventually(t, func() bool {
		return strings.Contains(stdout.String(), `"type":"ci_failed"`)
	}, 5*time.Second, 5*time.Millisecond, "watch must stream the filtered PR event")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not exit after context cancel")
	}

	// Output must contain only events for #42, never #43.
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	assert.NotEmpty(t, lines)
	for _, line := range lines {
		if line == "" {
			continue
		}
		var ev events.Event
		require.NoError(t, json.Unmarshal([]byte(line), &ev))
		assert.Equal(t, 42, ev.PR, "event must be for PR #42")
	}
}

//nolint:paralleltest // helper process executed as subprocess
func TestHelperProcessServe(_ *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	socketPath := os.Getenv("TEST_SOCKET_PATH")
	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra"}},
	}}
	_ = Run(
		context.Background(),
		[]string{"serve", "--socket", socketPath, "--interval", "10s"},
		src,
		os.Stdout,
		os.Stderr,
	)
	os.Exit(0)
}

func TestRun_ServeSIGTERM(t *testing.T) {
	t.Parallel()

	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-sig")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "pronto.sock")

	//nolint:gosec // helper test subprocess executes test binary itself
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestHelperProcessServe")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "TEST_SOCKET_PATH="+socketPath)
	require.NoError(t, cmd.Start())

	// Wait for socket file to exist and accept connections.
	dialer := &net.Dialer{Timeout: 250 * time.Millisecond}
	require.Eventually(t, func() bool {
		conn, err := dialer.DialContext(context.Background(), "unix", socketPath)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond)

	// Send SIGTERM to the process.
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))

	// Wait for process to exit cleanly.
	err = cmd.Wait()
	require.NoError(t, err)

	// Socket file must be removed.
	_, statErr := os.Stat(socketPath)
	assert.True(t, os.IsNotExist(statErr), "socket file should be removed on SIGTERM")
}

func TestRun_List_UsesDaemonWhenRunning(t *testing.T) {
	t.Parallel()

	pr := testPR(42, "Daemon Sourced PR")
	src := &scriptedSource{
		results: []fetchResult{
			{queue: model.Queue{Authored: []model.PullRequest{pr}}},
		},
	}
	socket := startDaemon(t, src)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"list", "--json", "--socket", socket}, nil, &stdout, &stderr)
	require.NoError(t, err)

	var q model.Queue
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &q))
	require.Len(t, q.Authored, 1)
	assert.Equal(t, 42, q.Authored[0].Number)
	assert.Equal(t, "Daemon Sourced PR", q.Authored[0].Title)
}

func TestRun_Why_UsesDaemonWhenRunning(t *testing.T) {
	t.Parallel()

	pr := testPR(42, "Daemon Sourced PR")
	src := &scriptedSource{
		results: []fetchResult{
			{queue: model.Queue{Authored: []model.PullRequest{pr}}},
		},
	}
	socket := startDaemon(t, src)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"why", "42", "--json", "--socket", socket}, nil, &stdout, &stderr)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &doc))
	assert.Contains(t, doc, "total")
}
