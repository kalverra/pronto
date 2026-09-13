package events

import (
	"testing"
	"time"
)

func TestBus_CancelDoesNotDeadlockWithClose(t *testing.T) {
	t.Parallel()

	b := NewBus()
	_, cancel := b.Subscribe()

	b.mu.Lock()
	done := make(chan struct{})
	go func() {
		cancel()
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)

	s := b.subs[0]
	s.close()
	b.mu.Unlock()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel deadlocked")
	}
}
