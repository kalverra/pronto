package daemon

import (
	"context"
	"errors"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/internal/source"
)

var _ source.Source = (*Source)(nil)

// Source adapts a Daemon to source.Source for in-process consumers like the TUI.
type Source struct {
	daemon *Daemon
}

// NewSource creates a Source backed by d.
func NewSource(d *Daemon) *Source {
	return &Source{daemon: d}
}

// Daemon returns the underlying Daemon.
func (s *Source) Daemon() *Daemon {
	return s.daemon
}

// IsDaemon reports whether this source is backed by a daemon.
func (s *Source) IsDaemon() bool {
	return true
}

// Snapshot returns the daemon's current snapshot.
func (s *Source) Snapshot() server.Snapshot {
	if s.daemon == nil {
		return server.Snapshot{}
	}
	return s.daemon.Snapshot()
}

// Refresh requests an immediate queue refresh from the daemon.
func (s *Source) Refresh(_ context.Context) (server.Snapshot, error) {
	if s.daemon == nil {
		return server.Snapshot{}, errors.New("daemon is nil")
	}
	return s.daemon.Refresh(), nil
}

// Subscribe opens an in-process subscription on the daemon's event bus.
// The subscription is canceled when ctx is canceled.
func (s *Source) Subscribe(ctx context.Context, subs ...events.Subscription) (<-chan events.Event, error) {
	if s.daemon == nil {
		return nil, errors.New("daemon is nil")
	}
	bus := s.daemon.Bus()
	if bus == nil {
		return nil, errors.New("daemon bus is nil")
	}
	if len(subs) == 0 {
		subs = []events.Subscription{{}}
	}
	ch, cancel := bus.Subscribe(subs...)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ch, nil
}

// Fetch retrieves the latest queue from the daemon snapshot.
// If the daemon's initial fetch has not completed yet, Fetch blocks until the
// first queue_refreshed event or ctx cancellation.
// If the daemon's last poll failed, Fetch returns the last known queue along
// with an error reporting the daemon's failure.
func (s *Source) Fetch(ctx context.Context) (model.Queue, error) {
	if s.daemon == nil {
		return model.Queue{}, errors.New("daemon is nil")
	}
	snap := s.daemon.Snapshot()
	if snap.FetchedAt.IsZero() && snap.LastError == "" {
		// Cold start: wait for initial refresh to complete
		ch, cancel := s.daemon.Bus().Subscribe(events.Subscription{
			Types: []events.Type{events.TypeQueueRefreshed},
		})
		defer cancel()

		snap = s.daemon.Snapshot()
		if snap.FetchedAt.IsZero() && snap.LastError == "" {
			select {
			case <-ch:
				snap = s.daemon.Snapshot()
			case <-ctx.Done():
				return model.Queue{}, ctx.Err()
			}
		}
	}
	if snap.LastError != "" {
		return snap.Queue, errors.New(snap.LastError)
	}
	return snap.Queue, nil
}
