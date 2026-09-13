package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/server"
)

type fakeState struct {
	queue model.Queue
	at    time.Time
}

func (f fakeState) Snapshot() server.Snapshot {
	return server.Snapshot{Queue: f.queue, FetchedAt: f.at}
}

func (f fakeState) FindPR(ref string) (*model.PullRequest, error) {
	return f.queue.Find(ref)
}

func (f fakeState) Refresh() server.Snapshot {
	return server.Snapshot{Queue: f.queue, FetchedAt: f.at}
}

type refreshableState struct {
	fakeState
	onRefresh func() server.Snapshot
}

func (r *refreshableState) Refresh() server.Snapshot {
	if r.onRefresh != nil {
		return r.onRefresh()
	}
	return r.fakeState.Refresh()
}

func testQueue() model.Queue {
	return model.Queue{
		Viewer: "kalverra",
		Authored: []model.PullRequest{
			{Number: 42, Title: "Add socket API", RepoNameWithOwner: "kalverra/pronto", Author: "kalverra"},
		},
		Inbox: []model.PullRequest{
			{Number: 7, Title: "Fix flaky test", RepoNameWithOwner: "other/repo", Author: "alice"},
		},
	}
}

type client struct {
	conn   net.Conn
	reader *bufio.Reader
}

func dial(t *testing.T, addr string) *client {
	t.Helper()
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(context.Background(), "unix", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &client{conn: conn, reader: bufio.NewReader(conn)}
}

func (c *client) send(t *testing.T, line string) {
	t.Helper()
	_, err := fmt.Fprintln(c.conn, line)
	require.NoError(t, err)
}

func (c *client) request(t *testing.T, id, method, params string) map[string]any {
	t.Helper()
	c.send(t, fmt.Sprintf(`{"id":%q,"method":%q,"params":%s}`, id, method, params))
	return c.readLine(t)
}

func (c *client) readLine(t *testing.T) map[string]any {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()
	line, err := c.reader.ReadString('\n')
	require.NoError(t, err, "expected a response line")
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &doc), "response line must be JSON: %s", line)
	return doc
}

func startServer(t *testing.T, st server.State, bus *events.Bus) *server.Server {
	t.Helper()

	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp(
		"",
		"pronto",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "pronto.sock")
	if bus == nil {
		bus = events.NewBus()
	}
	if st == nil {
		st = fakeState{queue: testQueue(), at: time.Now()}
	}
	ready := make(chan struct{})
	srv := server.New(bus, st, server.Options{
		SocketPath: path,
		Version:    "test-version",
		Ready:      ready,
	})
	go func() {
		_ = srv.Listen(context.Background())
	}()
	t.Cleanup(srv.Close)
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("server socket never became ready")
	}
	return srv
}

func TestServer_Ping(t *testing.T) {
	t.Parallel()

	srv := startServer(t, nil, nil)
	c := dial(t, srv.Addr())

	resp := c.request(t, "req_1", "ping", `{}`)
	assert.Equal(t, "req_1", resp["id"])
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "ping must return a result object: %v", resp)
	assert.Equal(t, "pong", result["type"])
	assert.Equal(t, "test-version", result["version"])
	assert.NotContains(t, resp, "error")
}

func TestServer_QueueSnapshot(t *testing.T) {
	t.Parallel()

	at := time.Now().UTC().Truncate(time.Second)
	srv := startServer(t, fakeState{queue: testQueue(), at: at}, nil)
	c := dial(t, srv.Addr())

	resp := c.request(t, "snap", "queue.snapshot", `{}`)
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "snapshot must return a result object: %v", resp)

	queueDoc, ok := result["queue"].(map[string]any)
	require.True(t, ok, "snapshot result must embed the queue")
	assert.Equal(t, "kalverra", queueDoc["viewer"])
	authored, ok := queueDoc["authored"].([]any)
	require.True(t, ok)
	require.Len(t, authored, 1)
	prDoc, ok := authored[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 42, prDoc["number"], 0.01)

	assert.Equal(t, at.Format(time.RFC3339), fmt.Sprintf("%v", result["fetched_at"]))
	assert.Contains(t, result, "age_seconds", "age_seconds must be set")
	assert.Contains(t, result, "dropped_events", "dropped_events must be present in queue.snapshot result")
}

func TestServer_QueueSnapshot_DroppedEvents(t *testing.T) {
	t.Parallel()

	at := time.Now().UTC().Truncate(time.Second)
	bus := events.NewBus()
	_, cancel := bus.Subscribe(events.Subscription{})
	defer cancel()
	for range events.SubscriberBuffer + 10 {
		bus.Emit(events.Event{Type: events.TypeQueueRefreshed})
	}
	srv := startServer(t, fakeState{queue: testQueue(), at: at}, bus)
	c := dial(t, srv.Addr())

	resp := c.request(t, "snap", "queue.snapshot", `{}`)
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 10, result["dropped_events"], 0.01)
}

func TestServer_QueueRefresh(t *testing.T) {
	t.Parallel()

	refreshed := false
	at := time.Now().UTC().Truncate(time.Second)
	st := &refreshableState{
		queue: testQueue(), at: at,
		onRefresh: func() server.Snapshot {
			refreshed = true
			return server.Snapshot{Queue: testQueue(), FetchedAt: at, Refreshing: true}
		},
	}
	srv := startServer(t, st, nil)
	c := dial(t, srv.Addr())

	resp := c.request(t, "ref_1", "queue.refresh", `{}`)
	assert.Equal(t, "ref_1", resp["id"])
	assert.True(t, refreshed, "state.Refresh must be called")
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "queue.refresh must return a result object: %v", resp)
	assert.Equal(t, true, result["refreshing"])
}

func TestServer_PRGet(t *testing.T) {
	t.Parallel()

	srv := startServer(t, nil, nil)

	t.Run("resolves ref", func(t *testing.T) {
		t.Parallel()

		c := dial(t, srv.Addr())
		resp := c.request(t, "get1", "pr.get", `{"ref":"kalverra/pronto#42"}`)
		result, ok := resp["result"].(map[string]any)
		require.True(t, ok, "pr.get must return a result object: %v", resp)
		prDoc, ok := result["pr"].(map[string]any)
		require.True(t, ok, "pr.get result must embed the PR")
		assert.InDelta(t, 42, prDoc["number"], 0.01)
		assert.Equal(t, "Add socket API", prDoc["title"])
	})

	t.Run("unknown ref returns not_found", func(t *testing.T) {
		t.Parallel()

		c := dial(t, srv.Addr())
		resp := c.request(t, "get2", "pr.get", `{"ref":"#999"}`)
		errDoc, ok := resp["error"].(map[string]any)
		require.True(t, ok, "unknown ref must return an error object: %v", resp)
		assert.Equal(t, "not_found", errDoc["code"])
		assert.NotContains(t, resp, "result")
	})

	t.Run("missing ref returns invalid_params", func(t *testing.T) {
		t.Parallel()

		c := dial(t, srv.Addr())
		resp := c.request(t, "get3", "pr.get", `{}`)
		errDoc, ok := resp["error"].(map[string]any)
		require.True(t, ok, "missing ref must return an error object: %v", resp)
		assert.Equal(t, "invalid_params", errDoc["code"])
	})
}

func TestServer_UnknownMethod(t *testing.T) {
	t.Parallel()

	srv := startServer(t, nil, nil)
	c := dial(t, srv.Addr())

	resp := c.request(t, "req_x", "pane.split", `{}`)
	assert.Equal(t, "req_x", resp["id"])
	errDoc, ok := resp["error"].(map[string]any)
	require.True(t, ok, "unknown method must return an error object: %v", resp)
	assert.Equal(t, "unsupported_method", errDoc["code"])
	assert.NotContains(t, resp, "result")
}

func TestServer_MalformedJSON(t *testing.T) {
	t.Parallel()

	srv := startServer(t, nil, nil)
	c := dial(t, srv.Addr())

	c.send(t, `{not json`)
	resp := c.readLine(t)
	errDoc, ok := resp["error"].(map[string]any)
	require.True(t, ok, "malformed JSON must return an error object: %v", resp)
	assert.Equal(t, "invalid_request", errDoc["code"])
}

func TestServer_EventsSubscribeStream(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	srv := startServer(t, nil, bus)
	c := dial(t, srv.Addr())

	resp := c.request(t, "sub_1", "events.subscribe", `{"subscriptions":[{"types":["ci_failed"]}]}`)
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "subscribe must return an ack result: %v", resp)
	assert.Equal(t, "subscribed", result["type"])

	// Non-matching event must not arrive; matching event must.
	emitted := bus.Emit(events.Event{Type: events.TypeCIPassed, Repo: "kalverra/pronto", PR: 1})
	_ = emitted
	delivered := bus.Emit(
		events.Event{Type: events.TypeCIFailed, Repo: "kalverra/pronto", PR: 2, Title: "Broken build"},
	)

	line, err := c.reader.ReadString('\n')
	require.NoError(t, err)
	var ev map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &ev))
	assert.Equal(t, "ci_failed", ev["type"])
	assert.InDelta(t, 2, ev["pr"], 0.01)
	assert.InDelta(t, delivered.Seq, ev["seq"], 0.01)

	// No further lines pending.
	assert.False(t, func() bool {
		_ = c.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()
		_, err := c.reader.ReadString('\n')
		return err == nil
	}(), "non-matching event must not be pushed")
}

func TestServer_ResubscribeReplacesPriorSubscription(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	srv := startServer(t, nil, bus)
	c := dial(t, srv.Addr())

	// First subscription: only ci_failed.
	resp1 := c.request(t, "sub_1", "events.subscribe", `{"subscriptions":[{"types":["ci_failed"]}]}`)
	require.Equal(t, "subscribed", resp1["result"].(map[string]any)["type"])

	// Second subscription on same conn: replaces first subscription with ci_passed.
	resp2 := c.request(t, "sub_2", "events.subscribe", `{"subscriptions":[{"types":["ci_passed"]}]}`)
	require.Equal(t, "subscribed", resp2["result"].(map[string]any)["type"])

	// Emit ci_failed: prior subscription was replaced, so this must NOT be delivered.
	bus.Emit(events.Event{Type: events.TypeCIFailed, Repo: "kalverra/pronto", PR: 1})

	// Emit ci_passed: active subscription must receive this.
	delivered := bus.Emit(events.Event{Type: events.TypeCIPassed, Repo: "kalverra/pronto", PR: 2})

	line, err := c.reader.ReadString('\n')
	require.NoError(t, err)
	var ev map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &ev))
	assert.Equal(
		t,
		"ci_passed",
		ev["type"],
		"prior subscription's ci_failed must have been dropped, receiving ci_passed first",
	)
	assert.InDelta(t, delivered.Seq, ev["seq"], 0.01)

	// Assert no extra event frames arrive.
	_ = c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()
	_, err = c.reader.ReadString('\n')
	assert.Error(t, err, "no extra event frames should arrive")
}

func TestServer_EventsSubscribeMultipleFilters(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	srv := startServer(t, nil, bus)
	c := dial(t, srv.Addr())

	resp := c.request(
		t,
		"sub_2",
		"events.subscribe",
		`{"subscriptions":[{"types":["ci_failed"]},{"repo":"other/repo"}]}`,
	)
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 2, result["subscriptions"], 0.01)

	bus.Emit(events.Event{Type: events.TypeCIFailed, Repo: "kalverra/pronto", PR: 1})
	bus.Emit(events.Event{Type: events.TypeReviewReceived, Repo: "other/repo", PR: 9})

	first := c.readLine(t)
	assert.Equal(t, "ci_failed", first["type"])
	second := c.readLine(t)
	assert.Equal(t, "review_received", second["type"])
	assert.InDelta(t, 9, second["pr"], 0.01)
}

func TestServer_EventsSubscribeInvalidParams(t *testing.T) {
	t.Parallel()

	srv := startServer(t, nil, nil)
	c := dial(t, srv.Addr())

	resp := c.request(t, "sub_bad", "events.subscribe", `{"subscriptions":[{"types":["bogus_event"]}]}`)
	errDoc, ok := resp["error"].(map[string]any)
	require.True(t, ok, "unknown event type must return an error object: %v", resp)
	assert.Equal(t, "invalid_params", errDoc["code"])
}

func TestServer_EventsSubscribeEmptySubscriptions(t *testing.T) {
	t.Parallel()

	srv := startServer(t, nil, nil)
	c := dial(t, srv.Addr())

	resp := c.request(t, "sub_none", "events.subscribe", `{"subscriptions":[]}`)
	errDoc, ok := resp["error"].(map[string]any)
	require.True(t, ok, "empty subscription list must return an error object: %v", resp)
	assert.Equal(t, "invalid_params", errDoc["code"])
}

func TestServer_ClientDisconnectKeepsServerHealthy(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	srv := startServer(t, nil, bus)

	subConn := dial(t, srv.Addr())
	_ = subConn.request(t, "sub_1", "events.subscribe", `{"subscriptions":[{}]}`)
	require.NoError(t, subConn.conn.Close())

	// Let the server observe the disconnect, then emit through the dead
	// subscriber's registration.
	time.Sleep(100 * time.Millisecond)
	bus.Emit(events.Event{Type: events.TypeQueueRefreshed})
	bus.Emit(events.Event{Type: events.TypeCIFailed, Repo: "kalverra/pronto", PR: 1})

	fresh := dial(t, srv.Addr())
	resp := fresh.request(t, "after", "ping", `{}`)
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "server must keep serving after a subscriber disconnect: %v", resp)
	assert.Equal(t, "pong", result["type"])
}

func TestServer_ErrorsEchoRequestID(t *testing.T) {
	t.Parallel()

	srv := startServer(t, nil, nil)
	c := dial(t, srv.Addr())

	c.send(t, `{"id":"err_1","method":"nope","params":{}}`)
	resp := c.readLine(t)
	assert.Equal(t, "err_1", resp["id"])
}

func TestServer_StateFindPRErrorPropagation(t *testing.T) {
	t.Parallel()

	st := failingState{}
	srv := startServer(t, st, nil)
	c := dial(t, srv.Addr())

	resp := c.request(t, "get_err", "pr.get", `{"ref":"#1"}`)
	errDoc, ok := resp["error"].(map[string]any)
	require.True(t, ok, "state errors must surface as error objects: %v", resp)
	assert.Equal(t, "not_found", errDoc["code"])
}

type failingState struct{}

func (failingState) Snapshot() server.Snapshot { return server.Snapshot{} }

func (failingState) FindPR(string) (*model.PullRequest, error) {
	return nil, errors.New("boom")
}

func (failingState) Refresh() server.Snapshot { return server.Snapshot{} }

func TestServer_CloseAfterFailedListenLeavesForeignSocket(t *testing.T) {
	t.Parallel()

	srvA := startServer(t, nil, nil)
	socketPath := srvA.Addr()

	// Verify server A is answering.
	cA := dial(t, socketPath)
	respA := cA.request(t, "ping_a", "ping", `{}`)
	resA, ok := respA["result"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "pong", resA["type"])

	// Create server B targeting the same socket path.
	srvB := server.New(events.NewBus(), fakeState{queue: testQueue(), at: time.Now()}, server.Options{
		SocketPath: socketPath,
		Version:    "test-b",
	})
	err := srvB.Listen(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another server is already listening")

	// Close server B. This must NOT unlink server A's socket!
	srvB.Close()

	// Verify socket still exists and server A still answers.
	_, statErr := os.Stat(socketPath)
	require.NoError(t, statErr, "socket file should still exist")

	cA2 := dial(t, socketPath)
	respA2 := cA2.request(t, "ping_a_after", "ping", `{}`)
	resA2, ok := respA2["result"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "pong", resA2["type"])
}
