package events

import (
	"sync"
	"sync/atomic"
	"time"
)

// SubscriberBuffer is the per-subscriber channel buffer size. A subscriber
// that falls further behind has events dropped rather than blocking emitters.
const SubscriberBuffer = 256

type subscriber struct {
	subs   []Subscription
	ch     chan Event
	cancel chan struct{}
	once   sync.Once
}

// matches reports whether the event matches any of the subscriber's filters.
func (s *subscriber) matches(e Event) bool {
	for _, sub := range s.subs {
		if sub.Matches(e) {
			return true
		}
	}
	return false
}

// Bus fans emitted events out to registered subscribers.
type Bus struct {
	mu      sync.Mutex
	subs    []*subscriber
	nextSeq atomic.Uint64
	dropped atomic.Uint64
	closed  bool
}

// NewBus creates an empty event bus.
func NewBus() *Bus {
	return &Bus{}
}

// Subscribe registers one subscriber that receives events matching any of the
// given filters, and returns its event channel plus a cancel func that stops
// delivery and closes the channel. A single subscriber sees events in emit
// order. Zero filters match nothing.
func (b *Bus) Subscribe(subs ...Subscription) (<-chan Event, func()) {
	s := &subscriber{
		subs:   subs,
		ch:     make(chan Event, SubscriberBuffer),
		cancel: make(chan struct{}),
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(s.ch)
		return s.ch, func() {}
	}
	b.subs = append(b.subs, s)
	b.mu.Unlock()

	cancel := func() {
		b.remove(s)
		s.close()
	}
	return s.ch, cancel
}

// SubscriberCount returns the number of active subscribers on the bus.
func (b *Bus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

func (s *subscriber) close() {
	s.once.Do(func() {
		close(s.cancel)
		close(s.ch)
	})
}

func (b *Bus) remove(target *subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subs {
		if s == target {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			return
		}
	}
}

// Emit finalizes the event (assigning Seq and TS when unset), fans it out to
// matching subscribers, and returns the finalized event.
func (b *Bus) Emit(e Event) Event {
	if e.Seq == 0 {
		e.Seq = b.nextSeq.Add(1)
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return e
	}
	for _, s := range b.subs {
		if !s.matches(e) {
			continue
		}
		select {
		case s.ch <- e:
		case <-s.cancel:
		default: // subscriber is full or gone: drop rather than block
			b.dropped.Add(1)
		}
	}
	return e
}

// Close removes and closes all subscriber channels. Later Emit calls are
// no-ops.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, s := range b.subs {
		s.close()
	}
	b.subs = nil
}

// Dropped returns the total number of events dropped due to slow or saturated subscribers.
func (b *Bus) Dropped() uint64 {
	return b.dropped.Load()
}
