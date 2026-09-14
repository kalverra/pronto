package events_test

import (
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/events"
)

func TestFetchProgressTypeIsValidAndNotTrigger(t *testing.T) {
	t.Parallel()

	assert.True(t, events.ValidTypes[events.TypeFetchProgress],
		"fetch_progress must be a valid event type")
	assert.NotContains(t, events.TriggerTypes, events.TypeFetchProgress,
		"fetch_progress is not a notification trigger")
}

func TestValidCodesListsAllWireErrorCodes(t *testing.T) {
	t.Parallel()

	assert.Len(t, events.ValidCodes, 4)
	assert.Contains(t, events.ValidCodes, events.CodeInvalidRequest)
	assert.Contains(t, events.ValidCodes, events.CodeInvalidParams)
	assert.Contains(t, events.ValidCodes, events.CodeUnsupportedMethod)
	assert.Contains(t, events.ValidCodes, events.CodeNotFound)
}

func TestSubscription_Matches(t *testing.T) {
	t.Parallel()

	reviewEvent := events.Event{
		Type:  events.TypeReviewReceived,
		Repo:  "kalverra/pronto",
		PR:    42,
		Title: "Add socket API",
	}
	queueEvent := events.Event{Type: events.TypeQueueRefreshed}

	tests := []struct {
		name string
		sub  events.Subscription
		ev   events.Event
		want bool
	}{
		{
			name: "empty subscription matches everything",
			sub:  events.Subscription{},
			ev:   reviewEvent,
			want: true,
		},
		{
			name: "empty subscription matches queue events",
			sub:  events.Subscription{},
			ev:   queueEvent,
			want: true,
		},
		{
			name: "type filter matches",
			sub:  events.Subscription{Types: []events.Type{events.TypeReviewReceived}},
			ev:   reviewEvent,
			want: true,
		},
		{
			name: "type filter rejects other type",
			sub:  events.Subscription{Types: []events.Type{events.TypeCIFailed}},
			ev:   reviewEvent,
			want: false,
		},
		{
			name: "multiple types match any",
			sub:  events.Subscription{Types: []events.Type{events.TypeCIFailed, events.TypeReviewReceived}},
			ev:   reviewEvent,
			want: true,
		},
		{
			name: "repo filter matches",
			sub:  events.Subscription{Repo: "kalverra/pronto"},
			ev:   reviewEvent,
			want: true,
		},
		{
			name: "repo filter rejects other repo",
			sub:  events.Subscription{Repo: "other/repo"},
			ev:   reviewEvent,
			want: false,
		},
		{
			name: "repo filter rejects repo-less event",
			sub:  events.Subscription{Repo: "kalverra/pronto"},
			ev:   queueEvent,
			want: false,
		},
		{
			name: "pr filter matches",
			sub:  events.Subscription{PR: 42},
			ev:   reviewEvent,
			want: true,
		},
		{
			name: "pr filter rejects other pr",
			sub:  events.Subscription{PR: 43},
			ev:   reviewEvent,
			want: false,
		},
		{
			name: "combined filters require all",
			sub:  events.Subscription{Types: []events.Type{events.TypeReviewReceived}, Repo: "kalverra/pronto", PR: 42},
			ev:   reviewEvent,
			want: true,
		},
		{
			name: "combined filters reject partial",
			sub:  events.Subscription{Types: []events.Type{events.TypeReviewReceived}, Repo: "other/repo", PR: 42},
			ev:   reviewEvent,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.sub.Matches(tt.ev))
		})
	}
}

func TestEvent_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	ts := time.Now().UTC().Truncate(time.Second)
	ev := events.Event{
		Seq:   7,
		Type:  events.TypeReviewReceived,
		TS:    ts,
		Repo:  "kalverra/pronto",
		PR:    42,
		Title: "Add socket API",
		Payload: events.ReviewPayload{
			Author:      "alice",
			State:       "APPROVED",
			SubmittedAt: ts,
		},
	}

	data, err := json.Marshal(ev)
	require.NoError(t, err)

	var back events.Event
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, ev.Seq, back.Seq)
	assert.Equal(t, ev.Type, back.Type)
	assert.True(t, ev.TS.Equal(back.TS))
	assert.Equal(t, ev.Repo, back.Repo)
	assert.Equal(t, ev.PR, back.PR)
	assert.Equal(t, ev.Title, back.Title)

	require.NotNil(t, back.Payload)
	payloadJSON, err := json.Marshal(back.Payload)
	require.NoError(t, err)
	var payload events.ReviewPayload
	require.NoError(t, json.Unmarshal(payloadJSON, &payload))
	assert.Equal(t, "alice", payload.Author)
	assert.Equal(t, "APPROVED", payload.State)
}

func TestEvent_OmitsEmptyFields(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(events.Event{Seq: 1, Type: events.TypeQueueRefreshed, TS: time.Now()})
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.NotContains(t, raw, "repo")
	assert.NotContains(t, raw, "pr")
	assert.NotContains(t, raw, "title")
	assert.NotContains(t, raw, "payload")
}

func TestParseTypes(t *testing.T) {
	t.Parallel()

	t.Run("parses comma separated list", func(t *testing.T) {
		t.Parallel()
		got, err := events.ParseTypes("ci_failed,review_received")
		require.NoError(t, err)
		assert.Equal(t, []events.Type{events.TypeCIFailed, events.TypeReviewReceived}, got)
	})

	t.Run("parses single type", func(t *testing.T) {
		t.Parallel()
		got, err := events.ParseTypes("pr_merged")
		require.NoError(t, err)
		assert.Equal(t, []events.Type{events.TypePRMerged}, got)
	})

	t.Run("empty string yields empty slice", func(t *testing.T) {
		t.Parallel()
		got, err := events.ParseTypes("")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("unknown type errors", func(t *testing.T) {
		t.Parallel()
		_, err := events.ParseTypes("ci_failed,bogus_event")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus_event")
	})
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestBus_MultipleFiltersMatchAnyInOrder(
	t *testing.T,
) {
	synctest.Test(t, func(t *testing.T) {
		bus := events.NewBus()
		defer bus.Close()

		ch, cancel := bus.Subscribe(
			events.Subscription{Types: []events.Type{events.TypeCIFailed}},
			events.Subscription{Repo: "other/repo"},
		)
		defer cancel()

		first := bus.Emit(events.Event{Type: events.TypeCIFailed, Repo: "kalverra/pronto", PR: 1})
		second := bus.Emit(events.Event{Type: events.TypeReviewReceived, Repo: "other/repo", PR: 9})
		bus.Emit(events.Event{Type: events.TypeCIPassed, Repo: "kalverra/pronto", PR: 2})

		want := []events.Event{first, second}
		for i, expected := range want {
			select {
			case got := <-ch:
				assert.Equal(t, expected, got, "event %d out of order", i+1)
			default:
				t.Fatalf("event %d not delivered", i+1)
			}
		}
		select {
		case got := <-ch:
			t.Fatalf("non-matching event delivered: %+v", got)
		default:
		}
	})
}

func TestBus_ZeroFiltersMatchNothing(t *testing.T) { //nolint:paralleltest // synctest bubbles cannot run in parallel
	synctest.Test(t, func(t *testing.T) {
		bus := events.NewBus()
		defer bus.Close()

		ch, cancel := bus.Subscribe()
		defer cancel()

		bus.Emit(events.Event{Type: events.TypeQueueRefreshed})

		select {
		case got := <-ch:
			t.Fatalf("zero-filter subscriber must not receive events, got %+v", got)
		default:
		}
	})
}

func TestBus_EmitAssignsSeqAndTimestamp(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	defer bus.Close()

	first := bus.Emit(events.Event{Type: events.TypeQueueRefreshed})
	second := bus.Emit(events.Event{Type: events.TypeQueueRefreshed})

	assert.Equal(t, uint64(1), first.Seq)
	assert.Equal(t, uint64(2), second.Seq)
	assert.False(t, first.TS.IsZero())
	assert.False(t, second.TS.IsZero())
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestBus_SubscriberReceivesOnlyMatchingEvents(
	t *testing.T,
) {
	synctest.Test(t, func(t *testing.T) {
		bus := events.NewBus()
		defer bus.Close()

		ch, cancel := bus.Subscribe(events.Subscription{Types: []events.Type{events.TypeCIFailed}})
		defer cancel()

		bus.Emit(events.Event{Type: events.TypeCIPassed, Repo: "kalverra/pronto", PR: 1})
		delivered := bus.Emit(
			events.Event{Type: events.TypeCIFailed, Repo: "kalverra/pronto", PR: 2, Title: "Broken build"},
		)

		select {
		case got := <-ch:
			assert.Equal(t, delivered, got)
		default:
			t.Fatal("matching event was not delivered")
		}

		select {
		case got := <-ch:
			t.Fatalf("non-matching event should not be delivered, got %+v", got)
		default:
		}
	})
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestBus_UnsubscribeStopsDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := events.NewBus()
		defer bus.Close()

		ch, cancel := bus.Subscribe(events.Subscription{})
		cancel()

		bus.Emit(events.Event{Type: events.TypeQueueRefreshed})

		select {
		case got, ok := <-ch:
			if ok {
				t.Fatalf("event delivered after unsubscribe: %+v", got)
			}
		default:
			select {
			case _, ok := <-ch:
				assert.False(t, ok, "channel should be closed after unsubscribe")
			case <-time.After(100 * time.Millisecond):
				t.Fatal("channel should be closed after unsubscribe")
			}
		}
	})
}

func TestBus_CloseClosesAllSubscribers(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	ch1, _ := bus.Subscribe(events.Subscription{})
	ch2, _ := bus.Subscribe(events.Subscription{Repo: "other/repo"})

	bus.Close()

	for i, ch := range []<-chan events.Event{ch1, ch2} {
		select {
		case _, ok := <-ch:
			assert.False(t, ok, "subscriber %d channel should be closed", i+1)
		default:
			t.Fatalf("subscriber %d channel should be closed and drained", i+1)
		}
	}
}

func TestBus_EmitAfterCloseDoesNotPanic(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	bus.Close()

	assert.NotPanics(t, func() {
		bus.Emit(events.Event{Type: events.TypeQueueRefreshed})
	})
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestBus_SlowConsumerDoesNotBlockEmitters(
	t *testing.T,
) {
	synctest.Test(t, func(_ *testing.T) {
		bus := events.NewBus()
		defer bus.Close()

		_, cancel := bus.Subscribe(events.Subscription{})
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			for range events.SubscriberBuffer * 4 {
				bus.Emit(events.Event{Type: events.TypeQueueRefreshed})
			}
		}()

		// If Emit ever blocked on the subscriber that never reads, the
		// bubble would deadlock and fail the test here.
		<-done

		assert.Equal(t, uint64(events.SubscriberBuffer*3), bus.Dropped(),
			"dropped events counter must match saturated drops")
	})
}

func TestBus_ConcurrentCancelAndCloseDoNotDeadlock(t *testing.T) {
	t.Parallel()

	for range 500 {
		b := events.NewBus()
		_, cancel := b.Subscribe()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			cancel()
		}()
		go func() {
			defer wg.Done()
			b.Close()
		}()
		wg.Wait()
	}
}
