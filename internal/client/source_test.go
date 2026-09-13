package client_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/client"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/internal/source"
)

var _ source.Source = (*client.QueueSource)(nil)

func TestQueueSource_Fetch_Success(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			var req struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if req.Method == "queue.snapshot" {
				resp, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": server.Snapshot{
						Queue: model.Queue{
							Authored: []model.PullRequest{
								{Number: 10, Title: "Authored PR"},
							},
						},
						FetchedAt:  time.Now(),
						AgeSeconds: 5.0,
						Refreshing: false,
					},
				})
				_, _ = fmt.Fprintln(conn, string(resp))
			}
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	src := client.NewQueueSource(c)
	require.NotNil(t, src)
	assert.Equal(t, c, src.Client())

	q, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, q.Authored, 1)
	assert.Equal(t, 10, q.Authored[0].Number)
	assert.Equal(t, "Authored PR", q.Authored[0].Title)
}

func TestQueueSource_Fetch_ReportsLastError(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			var req struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if req.Method == "queue.snapshot" {
				resp, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": server.Snapshot{
						Queue: model.Queue{
							Authored: []model.PullRequest{
								{Number: 10, Title: "Cached PR"},
							},
						},
						FetchedAt:  time.Now().Add(-2 * time.Minute),
						AgeSeconds: 120.0,
						Refreshing: false,
						LastError:  "rate limit exceeded",
					},
				})
				_, _ = fmt.Fprintln(conn, string(resp))
			}
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	src := client.NewQueueSource(c)
	q, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit exceeded")
	require.Len(t, q.Authored, 1)
	assert.Equal(t, 10, q.Authored[0].Number)
}

func TestQueueSource_Fetch_RPCError(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			var req struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			resp, _ := json.Marshal(map[string]any{
				"id": req.ID,
				"error": map[string]any{
					"code":    "internal_error",
					"message": "server failed",
				},
			})
			_, _ = fmt.Fprintln(conn, string(resp))
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	src := client.NewQueueSource(c)
	q, err := src.Fetch(context.Background())
	require.Error(t, err)
	assert.Empty(t, q.Authored)
}

func TestQueueSource_Refresh(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			var req struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if req.Method == "queue.refresh" {
				resp, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": server.Snapshot{
						Refreshing: true,
					},
				})
				_, _ = fmt.Fprintln(conn, string(resp))
			}
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	src := client.NewQueueSource(c)
	snap, err := src.Refresh(context.Background())
	require.NoError(t, err)
	assert.True(t, snap.Refreshing)
}

func TestQueueSource_Subscribe(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			var req struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if req.Method == "events.subscribe" {
				resp, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"type":          "subscribed",
						"subscriptions": 1,
					},
				})
				_, _ = fmt.Fprintln(conn, string(resp))

				ev, _ := json.Marshal(events.Event{
					Seq:  1,
					Type: events.TypeQueueRefreshed,
				})
				_, _ = fmt.Fprintln(conn, string(ev))
			}
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	src := client.NewQueueSource(c)
	// Subscribe dials a dedicated sub-client whose goroutines only stop on
	// ctx cancel or src.Close, so the ctx must be cancelable here.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := src.Subscribe(ctx)
	require.NoError(t, err)
	require.NotNil(t, ch)

	select {
	case ev, ok := <-ch:
		require.True(t, ok)
		assert.Equal(t, events.TypeQueueRefreshed, ev.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
	cancel()
}

func TestQueueSource_ResubscribeClosesPriorClient(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			var req struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if req.Method == "events.subscribe" {
				ack, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"type":          "subscribed",
						"subscriptions": 1,
					},
				})
				_, _ = fmt.Fprintln(conn, string(ack))
			}
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	src := client.NewQueueSource(c)
	ctx := t.Context()

	ch1, err := src.Subscribe(ctx)
	require.NoError(t, err)

	_, err = src.Subscribe(ctx)
	require.NoError(t, err)

	// The prior subscription client must be closed, not leaked: its event
	// channel closes once its connection is torn down.
	select {
	case _, ok := <-ch1:
		assert.False(t, ok, "prior subscription channel must be closed on resubscribe")
	case <-time.After(2 * time.Second):
		t.Fatal("prior subscription channel was never closed; prior client leaked")
	}
}

func TestQueueSource_Close(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)

	src := client.NewQueueSource(c)
	err = src.Close()
	require.NoError(t, err)

	_, err = src.Fetch(context.Background())
	require.Error(t, err)
}

func TestQueueSource_SeqGaps(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			var req struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if req.Method == "events.subscribe" {
				ack, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"type":          "subscribed",
						"subscriptions": 1,
					},
				})
				_, _ = fmt.Fprintln(conn, string(ack))

				ev1, _ := json.Marshal(events.Event{Seq: 1, Type: events.TypeQueueRefreshed, TS: time.Now().UTC()})
				_, _ = fmt.Fprintln(conn, string(ev1))

				ev3, _ := json.Marshal(events.Event{Seq: 3, Type: events.TypeQueueRefreshed, TS: time.Now().UTC()})
				_, _ = fmt.Fprintln(conn, string(ev3))
			}
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	src := client.NewQueueSource(c)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := src.Subscribe(ctx)
	require.NoError(t, err)

	var count int
	for range ch {
		count++
		if count == 2 {
			cancel()
		}
	}

	assert.Equal(t, uint64(1), src.SeqGaps(), "QueueSource must report 1 seq gap")
}
