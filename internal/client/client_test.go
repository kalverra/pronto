package client_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/client"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/server"
)

func startScriptedServer(t *testing.T, handler func(net.Conn)) string {
	t.Helper()

	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp("", "pronto-client")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "test.sock")
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				handler(c)
			}(conn)
		}
	}()

	return path
}

func TestClient_Do_IDMismatch(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		err = json.Unmarshal([]byte(line), &req)
		require.NoError(t, err)

		// Send an out-of-band / mismatched frame first.
		_, err = fmt.Fprintln(conn, `{"id":"c999","result":{"status":"other"}}`)
		require.NoError(t, err)
		// Then send the matching frame.
		_, err = fmt.Fprintf(conn, "{\"id\":%q,\"result\":{\"status\":\"ok\"}}\n", req.ID)
		require.NoError(t, err)
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	res, err := c.Do(context.Background(), "test", nil)
	require.NoError(t, err)

	var parsed struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(res, &parsed))
	assert.Equal(t, "ok", parsed.Status, "must receive response matching request ID, not the earlier frame")
}

func TestClient_Do_Timeout(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		// Read the request, but never respond: stay blocked on the socket
		// until the client hangs up, so the handler goroutine exits with the
		// test instead of outliving it.
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
		}
		require.NoError(t, scanner.Err(), "scanner must not error")
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = c.Do(ctx, "hang", nil)
	elapsed := time.Since(start)

	require.Error(t, err, "Do must fail when context times out")
	assert.True(
		t,
		errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err),
		"error should be timeout/deadline: %v",
		err,
	)
	assert.Less(t, elapsed, 300*time.Millisecond, "Do must return promptly on context timeout")
}

func TestClient_Do_Concurrent(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			var req struct {
				ID     string `json:"id"`
				Params struct {
					Tag int `json:"tag"`
				} `json:"params"`
			}
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				return
			}
			// Respond with the tag matching request ID
			resp, err := json.Marshal(map[string]any{
				"id": req.ID,
				"result": map[string]any{
					"tag": req.Params.Tag,
				},
			})
			require.NoError(t, err)
			time.Sleep(5 * time.Millisecond)
			_, err = fmt.Fprintln(conn, string(resp))
			require.NoError(t, err)
		}
		require.NoError(t, scanner.Err())
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	const workers = 10
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := range workers {
		wg.Add(1)
		go func(tag int) {
			defer wg.Done()
			raw, err := c.Do(ctx, "echo", map[string]int{"tag": tag})
			if err != nil {
				errs <- fmt.Errorf("worker %d: %w", tag, err)
				return
			}
			var res struct {
				Tag int `json:"tag"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				errs <- fmt.Errorf("worker %d unmarshal: %w", tag, err)
				return
			}
			if res.Tag != tag {
				errs <- fmt.Errorf("worker %d got tag %d", tag, res.Tag)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}
}

func TestClient_Ping(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			var req struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			if req.Method == "ping" {
				resp, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"type":    "pong",
						"version": "1.2.3",
					},
				})
				_, _ = fmt.Fprintln(conn, string(resp))
			}
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	version, err := c.Ping(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "1.2.3", version)
}

func TestClient_Ping_Error(t *testing.T) {
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
					"code":    "unsupported_method",
					"message": "no ping",
				},
			})
			_, _ = fmt.Fprintln(conn, string(resp))
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Ping(context.Background())
	require.Error(t, err)
	code, ok := client.WireError(err)
	require.True(t, ok)
	assert.Equal(t, "unsupported_method", code)
}

func TestClient_Snapshot(t *testing.T) {
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
								{Number: 42, Title: "PR 42"},
							},
						},
						AgeSeconds: 15.5,
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

	snap, err := c.Snapshot(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Queue.Authored, 1)
	assert.Equal(t, 42, snap.Queue.Authored[0].Number)
	assert.Equal(t, "PR 42", snap.Queue.Authored[0].Title)
	assert.InDelta(t, 15.5, snap.AgeSeconds, 0.001)
}

func TestClient_Snapshot_Error(t *testing.T) {
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
					"code":    "invalid_request",
					"message": "snapshot failed",
				},
			})
			_, _ = fmt.Fprintln(conn, string(resp))
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Snapshot(context.Background())
	require.Error(t, err)
	code, ok := client.WireError(err)
	require.True(t, ok)
	assert.Equal(t, "invalid_request", code)
}

func TestClient_Refresh(t *testing.T) {
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
						Queue: model.Queue{
							Authored: []model.PullRequest{
								{Number: 42, Title: "PR 42"},
							},
						},
						AgeSeconds: 0.1,
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

	snap, err := c.Refresh(context.Background())
	require.NoError(t, err)
	require.Len(t, snap.Queue.Authored, 1)
	assert.Equal(t, 42, snap.Queue.Authored[0].Number)
	assert.True(t, snap.Refreshing)
}

func TestClient_Refresh_Error(t *testing.T) {
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
					"code":    "unsupported_method",
					"message": "refresh failed",
				},
			})
			_, _ = fmt.Fprintln(conn, string(resp))
		}
	})

	c, err := client.Dial(socketPath)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Refresh(context.Background())
	require.Error(t, err)
	code, ok := client.WireError(err)
	require.True(t, ok)
	assert.Equal(t, "unsupported_method", code)
}

func TestClient_Subscribe_Reconnect(t *testing.T) {
	t.Parallel()

	var connCount atomic.Int32
	socketPath := startScriptedServer(t, func(conn net.Conn) {
		count := connCount.Add(1)
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

				if count == 1 {
					// Send first event and drop connection
					ev, _ := json.Marshal(events.Event{
						Seq:  1,
						Type: events.TypeQueueRefreshed,
						TS:   time.Now().UTC(),
					})
					_, _ = fmt.Fprintln(conn, string(ev))
					_ = conn.Close()
					return
				}

				// On reconnect: send second event
				ev, _ := json.Marshal(events.Event{
					Seq:  2,
					Type: events.TypeQueueRefreshed,
					TS:   time.Now().UTC(),
				})
				_, _ = fmt.Fprintln(conn, string(ev))
			}
		}
	})

	c, err := client.Dial(
		socketPath,
		client.WithRetrySleep(func(ctx context.Context, _ time.Duration) error { return ctx.Err() }),
	)
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := c.Subscribe(ctx, events.Subscription{})
	require.NoError(t, err)

	var received []events.Event
	for ev := range ch {
		received = append(received, ev)
		if len(received) == 2 {
			cancel()
			break
		}
	}

	require.Len(t, received, 2, "must receive events across reconnect")
	assert.Equal(t, uint64(1), received[0].Seq)
	assert.Equal(t, uint64(2), received[1].Seq)
	assert.GreaterOrEqual(t, connCount.Load(), int32(2), "must have connected at least twice")
}

func TestClient_Subscribe_SeqGapDetection(t *testing.T) {
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

				// Send seq 1
				ev1, _ := json.Marshal(events.Event{Seq: 1, Type: events.TypeQueueRefreshed, TS: time.Now().UTC()})
				_, _ = fmt.Fprintln(conn, string(ev1))

				// Send seq 4 (gap: missed 2 and 3)
				ev4, _ := json.Marshal(events.Event{Seq: 4, Type: events.TypeQueueRefreshed, TS: time.Now().UTC()})
				_, _ = fmt.Fprintln(conn, string(ev4))

				// Send seq 1 (server restart / sequence reset)
				evReset, _ := json.Marshal(events.Event{Seq: 1, Type: events.TypeQueueRefreshed, TS: time.Now().UTC()})
				_, _ = fmt.Fprintln(conn, string(evReset))
			}
		}
	})

	var gaps [][2]uint64
	var mu sync.Mutex
	c, err := client.Dial(
		socketPath,
		client.WithOnGap(func(lastSeq, currSeq uint64) {
			mu.Lock()
			defer mu.Unlock()
			gaps = append(gaps, [2]uint64{lastSeq, currSeq})
		}),
	)
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := c.Subscribe(ctx, events.Subscription{})
	require.NoError(t, err)

	var count int
	for range ch {
		count++
		if count == 3 {
			cancel()
		}
	}

	assert.Equal(t, 3, count)
	assert.Equal(t, uint64(2), c.SeqGaps(), "must record 2 gaps")
	assert.Equal(t, uint64(2), c.MissedEvents(), "must record 2 missed events (from seq 1 to 4)")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, gaps, 2)
	assert.Equal(t, [2]uint64{1, 4}, gaps[0])
	assert.Equal(t, [2]uint64{4, 1}, gaps[1])
}

func TestClient_Subscribe_Reconnect_ContextCanceled(t *testing.T) {
	t.Parallel()

	socketPath := startScriptedServer(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			var req struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &req)
			ack, _ := json.Marshal(map[string]any{
				"id": req.ID,
				"result": map[string]any{
					"type":          "subscribed",
					"subscriptions": 1,
				},
			})
			_, _ = fmt.Fprintln(conn, string(ack))
			// Close connection immediately to trigger reconnect
			_ = conn.Close()
		}
	})

	inSleep := make(chan struct{}, 1)
	c, err := client.Dial(
		socketPath,
		client.WithRetrySleep(func(ctx context.Context, _ time.Duration) error {
			select {
			case inSleep <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		}),
	)
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := c.Subscribe(ctx, events.Subscription{})
	require.NoError(t, err)

	// Cancel context during reconnect
	<-inSleep
	cancel()

	// Channel must close promptly
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel must be closed after context cancellation")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("channel not closed within timeout")
	}
}
