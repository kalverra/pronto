package daemon_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/source"
)

var _ source.Source = (*daemon.Source)(nil)

type latchSource struct {
	queue        model.Queue
	fetchStarted chan struct{}
	releaseFetch chan struct{}
}

func (b *latchSource) Fetch(ctx context.Context) (model.Queue, error) {
	select {
	case b.fetchStarted <- struct{}{}:
	default:
	}
	select {
	case <-b.releaseFetch:
		return b.queue, nil
	case <-ctx.Done():
		return model.Queue{}, ctx.Err()
	}
}

func TestDaemonSource_IsDaemon(t *testing.T) {
	t.Parallel()

	d := daemon.New(daemon.Options{})
	src := daemon.NewSource(d)
	require.NotNil(t, src)
	assert.True(t, src.IsDaemon(), "daemon.Source must report IsDaemon() == true")
	assert.Equal(t, d, src.Daemon())
}

func TestDaemonSource_Fetch_Warm(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Viewer:   "kalverra",
		Authored: []model.PullRequest{{Number: 1, Title: "PR 1", RepoNameWithOwner: "kalverra/pronto"}},
	}
	src := &scriptedSource{results: []fetchResult{{queue: q}}}
	d := daemon.New(daemon.Options{
		Source:     src,
		SocketPath: "",
		Interval:   10 * time.Minute,
	})
	c := collect(t, d.Bus())
	ctx := t.Context()
	go func() { _ = d.Run(ctx) }()

	// Wait for daemon first fetch to complete
	_ = c.waitFor(t, events.TypeQueueRefreshed)

	daemonSrc := daemon.NewSource(d)
	require.NotNil(t, daemonSrc)
	fetched, err := daemonSrc.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, q.Authored, fetched.Authored)
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemonSource_Fetch_Cold_WaitsForFirstRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := model.Queue{
			Viewer:   "kalverra",
			Authored: []model.PullRequest{{Number: 42, Title: "Cold Start PR", RepoNameWithOwner: "kalverra/pronto"}},
		}
		fetchStarted := make(chan struct{}, 1)
		releaseFetch := make(chan struct{})
		delaySrc := &latchSource{
			queue:        q,
			fetchStarted: fetchStarted,
			releaseFetch: releaseFetch,
		}

		d := daemon.New(daemon.Options{
			Source:     delaySrc,
			SocketPath: "",
			Interval:   10 * time.Minute,
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = d.Run(ctx) }()

		// Wait until daemon enters refresh
		<-fetchStarted

		daemonSrc := daemon.NewSource(d)
		require.NotNil(t, daemonSrc)

		// Fetch concurrently while refresh is in progress
		type fetchRes struct {
			queue model.Queue
			err   error
		}
		resCh := make(chan fetchRes, 1)
		go func() {
			resQueue, err := daemonSrc.Fetch(context.Background())
			resCh <- fetchRes{queue: resQueue, err: err}
		}()

		// Ensure Fetch is waiting and has not returned prematurely with empty queue
		synctest.Wait()
		select {
		case res := <-resCh:
			t.Fatalf("Fetch returned prematurely before first refresh finished: %+v", res)
		default:
		}

		// Release the refresh
		close(releaseFetch)

		synctest.Wait()
		select {
		case res := <-resCh:
			require.NoError(t, res.err)
			assert.Equal(t, q.Authored, res.queue.Authored)
		default:
			t.Fatal("Fetch did not complete after release")
		}
	})
}

func TestDaemonSource_Fetch_ReportsLastError(t *testing.T) {
	t.Parallel()

	fetchErr := errors.New("rate limit exceeded")
	src := &scriptedSource{results: []fetchResult{{err: fetchErr}}}
	d := daemon.New(daemon.Options{
		Source:     src,
		SocketPath: "",
		Interval:   10 * time.Minute,
	})
	c := collect(t, d.Bus())
	ctx := t.Context()
	go func() { _ = d.Run(ctx) }()

	_ = c.waitFor(t, events.TypeQueueRefreshed)

	daemonSrc := daemon.NewSource(d)
	require.NotNil(t, daemonSrc)
	_, err := daemonSrc.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit exceeded")
}

func TestDaemonSource_Refresh(t *testing.T) {
	t.Parallel()

	q1 := model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{{Number: 1, Title: "PR 1"}}}
	q2 := model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{{Number: 2, Title: "PR 2"}}}
	src := &scriptedSource{results: []fetchResult{
		{queue: q1},
		{queue: q2},
	}}
	d := daemon.New(daemon.Options{
		Source:     src,
		SocketPath: "",
		Interval:   10 * time.Minute,
	})
	c := collect(t, d.Bus())
	ctx := t.Context()
	go func() { _ = d.Run(ctx) }()

	_ = c.waitFor(t, events.TypeQueueRefreshed)

	daemonSrc := daemon.NewSource(d)
	require.NotNil(t, daemonSrc)

	snap, err := daemonSrc.Refresh(context.Background())
	require.NoError(t, err)
	assert.True(t, snap.Refreshing)

	// Wait for refresh to complete and emit queue_refreshed
	_ = c.waitFor(t, events.TypeQueueRefreshed)

	q, err := daemonSrc.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, q.Authored, 1)
	assert.Equal(t, 2, q.Authored[0].Number)
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemonSource_Subscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := daemon.New(daemon.Options{})
		daemonSrc := daemon.NewSource(d)
		require.NotNil(t, daemonSrc)

		subCtx, subCancel := context.WithCancel(context.Background())
		defer subCancel()
		ch, err := daemonSrc.Subscribe(subCtx)
		require.NoError(t, err)

		d.Bus().Emit(events.Event{
			Type:  events.TypeCIPassed,
			Repo:  "kalverra/pronto",
			PR:    42,
			Title: "Test Event",
		})

		synctest.Wait()
		select {
		case ev := <-ch:
			assert.Equal(t, events.TypeCIPassed, ev.Type)
			assert.Equal(t, 42, ev.PR)
		default:
			t.Fatal("timed out waiting for emitted event")
		}

		subCancel()
		synctest.Wait()
		// Channel should close after subCtx cancel
		select {
		case _, ok := <-ch:
			assert.False(t, ok, "subscription channel should close on cancel")
		default:
			t.Fatal("subscription channel did not close")
		}
	})
}
