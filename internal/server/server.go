// Package server exposes the pronto daemon state and event stream over a
// local NDJSON socket.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
)

// Error codes returned by the protocol.
const (
	CodeInvalidRequest    = events.CodeInvalidRequest
	CodeInvalidParams     = events.CodeInvalidParams
	CodeUnsupportedMethod = events.CodeUnsupportedMethod
	CodeNotFound          = events.CodeNotFound
)

// Socket API method names. The method enum in internal/events/schema.json
// must stay in sync with these; a drift test enforces it.
const (
	MethodPing            = "ping"
	MethodQueueSnapshot   = "queue.snapshot"
	MethodQueueRefresh    = "queue.refresh"
	MethodGetPR           = "pr.get"
	MethodEventsSubscribe = "events.subscribe"
)

// Methods lists all supported socket API methods.
var Methods = []string{
	MethodPing,
	MethodQueueSnapshot,
	MethodQueueRefresh,
	MethodGetPR,
	MethodEventsSubscribe,
}

// Snapshot is the result of the queue.snapshot method: the last fetched queue
// plus freshness metadata.
type Snapshot struct {
	Queue         model.Queue `json:"queue"`
	FetchedAt     time.Time   `json:"fetched_at"`
	AgeSeconds    float64     `json:"age_seconds"`
	Refreshing    bool        `json:"refreshing"`
	LastError     string      `json:"last_error,omitempty"`
	DroppedEvents uint64      `json:"dropped_events"`
}

// State supplies live daemon state to socket methods.
type State interface {
	Snapshot() Snapshot
	FindPR(ref string) (*model.PullRequest, error)
	Refresh() Snapshot
}

// ErrAlreadyListening is returned when another server is already listening on the socket path.
var ErrAlreadyListening = errors.New("another server is already listening")

// Options configures a Server.
type Options struct {
	SocketPath string
	Version    string
	Ready      chan struct{} // optional; closed when listener is successfully bound
}

// subscribeParams are the params of events.subscribe.
type subscribeParams struct {
	Subscriptions []events.Subscription `json:"subscriptions"`
}

// prGetParams are the params of pr.get.
type prGetParams struct {
	Ref string `json:"ref"`
}

// Server serves protocol connections over a Unix domain socket.
type Server struct {
	bus   *events.Bus
	state State
	opts  Options

	listener net.Listener
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
	// shutdown is closed by the first Close call so the ctx-watcher
	// goroutine in Listen exits even when ctx is never canceled (e.g.
	// context.Background).
	shutdown chan struct{}
}

// New creates a Server. Call Listen to start accepting connections.
func New(bus *events.Bus, state State, opts Options) *Server {
	return &Server{
		bus:      bus,
		state:    state,
		opts:     opts,
		conns:    make(map[net.Conn]struct{}),
		shutdown: make(chan struct{}),
	}
}

// Addr returns the socket path the server listens on.
func (s *Server) Addr() string {
	return s.opts.SocketPath
}

// Listen creates the socket and serves connections until ctx is canceled or
// Close is called. It blocks.
func (s *Server) Listen(ctx context.Context) error {
	if err := prepareSocket(s.opts.SocketPath); err != nil {
		return fmt.Errorf("prepare socket: %w", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", s.opts.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.opts.SocketPath, err)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return errors.New("server closed")
	}
	s.listener = ln
	if s.opts.Ready != nil {
		close(s.opts.Ready)
	}
	s.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
		case <-s.shutdown:
		}
		s.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return nil
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.serveConn(conn)
	}
}

// Close stops the listener and closes all client connections.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.shutdown)
	if s.listener != nil {
		_ = s.listener.Close()
	}
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()
}

func prepareSocket(path string) error {
	probe := &net.Dialer{Timeout: 500 * time.Millisecond}
	if conn, err := probe.DialContext(context.Background(), "unix", path); err == nil {
		_ = conn.Close()
		return fmt.Errorf("%w on %s", ErrAlreadyListening, path)
	}
	// Stale socket from a dead server: remove it.
	_ = os.Remove(path)
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create socket dir: %w", err)
		}
	}
	return nil
}

type connWriter struct {
	mu   sync.Mutex
	conn net.Conn
}

func (w *connWriter) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.conn.Write(data)
	return err
}

func (w *connWriter) writeResponse(id string, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return w.writeJSON(events.Response{ID: id, Result: raw})
}

func (w *connWriter) writeError(id string, werr *events.WireError) error {
	return w.writeJSON(events.Response{ID: id, Error: werr})
}

func (s *Server) serveConn(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	w := &connWriter{conn: conn}
	var cancelSub func()
	defer func() {
		if cancelSub != nil {
			cancelSub()
		}
	}()

	decoder := json.NewDecoder(conn)
	for {
		var req events.Request
		if err := decoder.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			_ = w.writeError("", &events.WireError{Code: CodeInvalidRequest, Message: err.Error()})
			return
		}

		if req.Method == MethodEventsSubscribe {
			cancelSub = s.handleSubscribe(w, req, cancelSub)
			continue
		}

		result, werr := s.handleMethod(req)
		if werr != nil {
			_ = w.writeError(req.ID, werr)
			continue
		}
		if err := w.writeResponse(req.ID, result); err != nil {
			return
		}
	}
}

// withDrops fills the snapshot's drop counter from the bus, the single source
// of truth for dropped events.
func (s *Server) withDrops(snap Snapshot) Snapshot {
	if s.bus != nil {
		snap.DroppedEvents = s.bus.Dropped()
	}
	return snap
}

func (s *Server) handleMethod(req events.Request) (any, *events.WireError) {
	switch req.Method {
	case MethodPing:
		return pingResult{Type: "pong", Version: s.opts.Version}, nil
	case MethodQueueSnapshot:
		return s.withDrops(s.state.Snapshot()), nil
	case MethodQueueRefresh:
		return s.withDrops(s.state.Refresh()), nil
	case MethodGetPR:
		var params prGetParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return nil, &events.WireError{Code: CodeInvalidParams, Message: err.Error()}
			}
		}
		if params.Ref == "" {
			return nil, &events.WireError{Code: CodeInvalidParams, Message: "pr.get requires a \"ref\" param"}
		}
		pr, err := s.state.FindPR(params.Ref)
		if err != nil {
			return nil, &events.WireError{Code: CodeNotFound, Message: err.Error()}
		}
		return prGetResult{Type: "pr", PR: pr}, nil
	default:
		return nil, &events.WireError{
			Code:    CodeUnsupportedMethod,
			Message: fmt.Sprintf("unknown method %q", req.Method),
		}
	}
}

type pingResult struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

type prGetResult struct {
	Type string             `json:"type"`
	PR   *model.PullRequest `json:"pr"`
}

// handleSubscribe acknowledges the subscription and wires each filter into
// the event bus, pushing matching events onto the connection. Any prior
// subscription on the connection is cancelled to avoid stacking writer goroutines.
func (s *Server) handleSubscribe(w *connWriter, req events.Request, prevCancel func()) func() {
	var params subscribeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			_ = w.writeError(req.ID, &events.WireError{Code: CodeInvalidParams, Message: err.Error()})
			return prevCancel
		}
	}
	if len(params.Subscriptions) == 0 {
		_ = w.writeError(
			req.ID,
			&events.WireError{Code: CodeInvalidParams, Message: "events.subscribe requires at least one subscription"},
		)
		return prevCancel
	}
	for _, sub := range params.Subscriptions {
		for _, typ := range sub.Types {
			if !events.ValidTypes[typ] {
				_ = w.writeError(req.ID, &events.WireError{
					Code:    CodeInvalidParams,
					Message: fmt.Sprintf("unknown event type %q", string(typ)),
				})
				return prevCancel
			}
		}
	}

	// Cancel prior subscription on this connection before establishing the new one.
	if prevCancel != nil {
		prevCancel()
	}

	// One bus subscription per connection: a single channel and writer
	// goroutine keep pushed events in emit order.
	// Subscribe before writing the ack so emitted events immediately following
	// the ack cannot be dropped.
	ch, cancel := s.bus.Subscribe(params.Subscriptions...)
	go func(ch <-chan events.Event) {
		for ev := range ch {
			if err := w.writeJSON(ev); err != nil {
				return
			}
		}
	}(ch)

	_ = w.writeResponse(req.ID, subscribeResult{
		Type:          "subscribed",
		Subscriptions: len(params.Subscriptions),
	})

	return cancel
}

type subscribeResult struct {
	Type          string `json:"type"`
	Subscriptions int    `json:"subscriptions"`
}
