package client

import (
	"context"
	"errors"
	"sync"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/server"
)

// QueueSource adapts a Client to source.Source by retrieving the daemon's
// latest queue snapshot.
type QueueSource struct {
	client    *Client
	subClient *Client
	mu        sync.Mutex
}

// NewQueueSource creates a QueueSource backed by c.
func NewQueueSource(c *Client) *QueueSource {
	return &QueueSource{client: c}
}

// Client returns the underlying Client.
func (s *QueueSource) Client() *Client {
	return s.client
}

// IsDaemon reports whether this source is backed by a daemon.
func (s *QueueSource) IsDaemon() bool {
	return true
}

// Refresh requests an immediate queue refresh from the daemon.
func (s *QueueSource) Refresh(ctx context.Context) (server.Snapshot, error) {
	if s.client == nil {
		return server.Snapshot{}, errors.New("client is nil")
	}
	return s.client.Refresh(ctx)
}

// Subscribe opens an event subscription on a dedicated client connection.
func (s *QueueSource) Subscribe(ctx context.Context, subs ...events.Subscription) (<-chan events.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, errors.New("client is nil")
	}
	path := s.client.Path()
	if path == "" {
		path = SocketPath()
	}
	subClient, err := Dial(path,
		WithOnGap(s.client.onGap),
		WithBackoff(s.client.baseDelay, s.client.maxDelay),
		WithRetrySleep(s.client.sleepFn),
	)
	if err != nil {
		return nil, err
	}
	// Close any prior subscription client so resubscribing does not leak
	// its connection and reader goroutines.
	if s.subClient != nil {
		s.subClient.Close()
	}
	s.subClient = subClient
	if len(subs) == 0 {
		subs = []events.Subscription{{}}
	}
	return subClient.Subscribe(ctx, subs...)
}

// Close closes the underlying Client connections.
func (s *QueueSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
	}
	if s.subClient != nil {
		s.subClient.Close()
	}
	return nil
}

// Fetch retrieves the latest queue from the daemon. If the daemon's last poll
// failed, Fetch returns the last known queue along with an error reporting
// the daemon's failure.
func (s *QueueSource) Fetch(ctx context.Context) (model.Queue, error) {
	snap, err := s.client.Snapshot(ctx)
	if err != nil {
		return model.Queue{}, err
	}
	if snap.LastError != "" {
		return snap.Queue, errors.New(snap.LastError)
	}
	return snap.Queue, nil
}

// SeqGaps returns the total number of sequence gaps detected by the subscription client.
func (s *QueueSource) SeqGaps() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subClient == nil {
		return 0
	}
	return s.subClient.SeqGaps()
}

// SubClient returns the dedicated subscription client, if subscribed.
func (s *QueueSource) SubClient() *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subClient
}
