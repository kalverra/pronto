// Package client provides a thin client for the pronto daemon socket API.
package client

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
	"time"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/server"
)

const defaultRequestTimeout = 5 * time.Second

// SocketPath resolves the daemon socket path: PRONTO_SOCKET_PATH overrides,
// otherwise the pronto config directory.
func SocketPath() string {
	if p := os.Getenv("PRONTO_SOCKET_PATH"); p != "" {
		return p
	}
	return filepath.Join(config.Dir(), "pronto.sock")
}

// WireError exposes the protocol error code of an error returned by Do.
func WireError(err error) (code string, ok bool) {
	if we, ok := errors.AsType[*events.WireError](err); ok {
		return we.Code, true
	}
	return "", false
}

// Client is a connection to a running pronto daemon.
type Client struct {
	path         string
	conn         net.Conn
	reader       *bufio.Reader
	nextID       atomic.Uint64
	mu           sync.Mutex
	closed       bool
	closeOnce    sync.Once
	closeCh      chan struct{}
	baseDelay    time.Duration
	maxDelay     time.Duration
	sleepFn      func(ctx context.Context, d time.Duration) error
	onGap        func(lastSeq, currSeq uint64)
	seqGaps      atomic.Uint64
	missedEvents atomic.Uint64
}

// DialOption configures Client options.
type DialOption func(*Client)

// WithOnGap registers a callback invoked when a sequence gap is detected in an event subscription stream.
func WithOnGap(fn func(lastSeq, currSeq uint64)) DialOption {
	return func(c *Client) {
		c.onGap = fn
	}
}

// WithBackoff configures retry backoff delays.
func WithBackoff(base, maxDelay time.Duration) DialOption {
	return func(c *Client) {
		if base > 0 {
			c.baseDelay = base
		}
		if maxDelay > 0 {
			c.maxDelay = maxDelay
		}
	}
}

// WithRetrySleep configures the sleep function used during backoff.
func WithRetrySleep(fn func(ctx context.Context, d time.Duration) error) DialOption {
	return func(c *Client) {
		if fn != nil {
			c.sleepFn = fn
		}
	}
}

// Dial connects to the daemon socket at path.
func Dial(path string, opts ...DialOption) (*Client, error) {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(context.Background(), "unix", path)
	if err != nil {
		return nil, fmt.Errorf("pronto server not reachable at %s (start it with `pronto serve`): %w", path, err)
	}
	c := &Client{
		path:      path,
		conn:      conn,
		reader:    bufio.NewReader(conn),
		closeCh:   make(chan struct{}),
		baseDelay: 50 * time.Millisecond,
		maxDelay:  2 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Path returns the socket path this client connected to.
func (c *Client) Path() string {
	return c.path
}

// Close closes the connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.closeOnce.Do(func() {
		close(c.closeCh)
	})
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.sleepFn != nil {
		return c.sleepFn(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closeCh:
		return net.ErrClosed
	case <-time.After(d):
		return nil
	}
}

// Do performs one request/response round trip.
func (c *Client) Do(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(defaultRequestTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok {
		deadline = ctxDeadline
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}
	defer func() {
		_ = c.conn.SetDeadline(time.Time{})
	}()

	stop := context.AfterFunc(ctx, func() {
		_ = c.conn.SetDeadline(time.Now())
	})
	defer stop()

	var rawParams json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("encode params: %w", err)
		}
		rawParams = encoded
	}
	reqID := fmt.Sprintf("c%d", c.nextID.Add(1))
	reqLine, err := json.Marshal(events.Request{
		ID:     reqID,
		Method: method,
		Params: rawParams,
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	if _, err := c.conn.Write(append(reqLine, '\n')); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			if _, ok := ctx.Deadline(); ok {
				return nil, context.DeadlineExceeded
			}
		}
		return nil, fmt.Errorf("send request: %w", err)
	}

	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				if _, ok := ctx.Deadline(); ok {
					return nil, context.DeadlineExceeded
				}
			}
			return nil, fmt.Errorf("read response: %w", err)
		}
		var resp events.Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		if resp.ID != reqID {
			continue
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// GetPR resolves a PR reference against the daemon's current queue.
func (c *Client) GetPR(ctx context.Context, ref string) (*model.PullRequest, error) {
	raw, err := c.Do(ctx, server.MethodGetPR, map[string]string{"ref": ref})
	if err != nil {
		return nil, err
	}
	var result struct {
		PR *model.PullRequest `json:"pr"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode pr.get result: %w", err)
	}
	if result.PR == nil {
		return nil, fmt.Errorf("pull request %q not found", ref)
	}
	return result.PR, nil
}

// Subscribe opens an event subscription. After it returns, the connection
// delivers pushed events on the returned channel until it is closed or ctx
// is canceled. No further Do calls are allowed.
func (c *Client) Subscribe(ctx context.Context, subs ...events.Subscription) (<-chan events.Event, error) {
	raw, err := c.Do(ctx, server.MethodEventsSubscribe, map[string]any{"subscriptions": subs})
	if err != nil {
		return nil, err
	}
	var ack struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil || ack.Type != "subscribed" {
		return nil, fmt.Errorf("unexpected subscribe acknowledgement: %s", string(raw))
	}

	ch := make(chan events.Event, 64)
	finished := make(chan struct{})
	go func() {
		defer close(ch)
		defer close(finished)

		c.mu.Lock()
		decoder := json.NewDecoder(c.reader)
		c.mu.Unlock()

		var lastSeq uint64
		for {
			var ev events.Event
			if err := decoder.Decode(&ev); err != nil {
				if ctx.Err() != nil || c.isClosed() {
					return
				}
				if err := c.reconnectSubscribe(ctx, subs); err != nil {
					return
				}
				c.mu.Lock()
				decoder = json.NewDecoder(c.reader)
				c.mu.Unlock()
				continue
			}

			c.checkSeqGap(ev.Seq, &lastSeq)

			select {
			case ch <- ev:
			case <-ctx.Done():
				c.Close()
				return
			case <-c.closeCh:
				return
			}
		}
	}()
	// Closing the connection unblocks a decoder stuck waiting on the socket.
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-c.closeCh:
		case <-finished:
		}
	}()
	return ch, nil
}

// Ping checks daemon liveness and returns the server version string.
func (c *Client) Ping(ctx context.Context) (string, error) {
	raw, err := c.Do(ctx, server.MethodPing, nil)
	if err != nil {
		return "", err
	}
	var res struct {
		Type    string `json:"type"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("decode ping result: %w", err)
	}
	return res.Version, nil
}

// snapshotMethod performs a snapshot-returning round trip for method.
func (c *Client) snapshotMethod(ctx context.Context, method string) (server.Snapshot, error) {
	raw, err := c.Do(ctx, method, nil)
	if err != nil {
		return server.Snapshot{}, err
	}
	var snap server.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return server.Snapshot{}, fmt.Errorf("decode %s result: %w", method, err)
	}
	return snap, nil
}

// Snapshot retrieves the daemon's current queue snapshot.
func (c *Client) Snapshot(ctx context.Context) (server.Snapshot, error) {
	return c.snapshotMethod(ctx, server.MethodQueueSnapshot)
}

// Refresh requests an immediate queue refresh from the daemon.
func (c *Client) Refresh(ctx context.Context) (server.Snapshot, error) {
	return c.snapshotMethod(ctx, server.MethodQueueRefresh)
}

// SeqGaps returns the total number of sequence gaps detected across subscription streams.
func (c *Client) SeqGaps() uint64 {
	return c.seqGaps.Load()
}

// MissedEvents returns the total number of events missed across sequence gaps.
func (c *Client) MissedEvents() uint64 {
	return c.missedEvents.Load()
}

func (c *Client) reconnectSubscribe(ctx context.Context, subs []events.Subscription) error {
	delay := c.baseDelay
	if delay <= 0 {
		delay = 50 * time.Millisecond
	}
	maxDelay := c.maxDelay
	if maxDelay <= 0 {
		maxDelay = 2 * time.Second
	}

	dialer := &net.Dialer{Timeout: 2 * time.Second}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.isClosed() {
			return net.ErrClosed
		}

		if err := c.sleep(ctx, delay); err != nil {
			return err
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}

		conn, err := dialer.DialContext(ctx, "unix", c.path)
		if err != nil {
			continue
		}

		reader := bufio.NewReader(conn)
		reqID := fmt.Sprintf("c%d", c.nextID.Add(1))
		rawParams, err := json.Marshal(map[string]any{"subscriptions": subs})
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("encode subscribe params: %w", err)
		}
		reqLine, err := json.Marshal(events.Request{
			ID:     reqID,
			Method: server.MethodEventsSubscribe,
			Params: rawParams,
		})
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("encode subscribe request: %w", err)
		}

		_ = conn.SetDeadline(time.Now().Add(defaultRequestTimeout))
		if _, err := conn.Write(append(reqLine, '\n')); err != nil {
			_ = conn.Close()
			continue
		}

		var ackOk bool
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			var resp events.Response
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				break
			}
			if resp.ID != reqID {
				continue
			}
			if resp.Error != nil {
				break
			}
			var ack struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(resp.Result, &ack); err == nil && ack.Type == "subscribed" {
				ackOk = true
			}
			break
		}
		_ = conn.SetDeadline(time.Time{})
		if !ackOk {
			_ = conn.Close()
			continue
		}

		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			_ = conn.Close()
			return net.ErrClosed
		}
		if c.conn != nil {
			_ = c.conn.Close()
		}
		c.conn = conn
		c.reader = reader
		c.mu.Unlock()
		return nil
	}
}

func (c *Client) checkSeqGap(currSeq uint64, lastSeq *uint64) {
	last := *lastSeq
	if last > 0 {
		if currSeq > last+1 {
			missed := currSeq - last - 1
			c.seqGaps.Add(1)
			c.missedEvents.Add(missed)
			if c.onGap != nil {
				c.onGap(last, currSeq)
			}
		} else if currSeq <= last {
			c.seqGaps.Add(1)
			if c.onGap != nil {
				c.onGap(last, currSeq)
			}
		}
	}
	*lastSeq = currSeq
}
