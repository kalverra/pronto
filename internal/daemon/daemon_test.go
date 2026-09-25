package daemon_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/internal/source"
)

// scriptedSource returns queued fetch results in order, then repeats the
// last result forever.
type scriptedSource struct {
	mu        sync.Mutex
	results   []fetchResult
	calls     int
	deadlines []time.Time
}

type fetchResult struct {
	queue model.Queue
	err   error
}

func (s *scriptedSource) Fetch(ctx context.Context) (model.Queue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		s.deadlines = append(s.deadlines, dl)
	}
	idx := s.calls
	if idx >= len(s.results) {
		idx = len(s.results) - 1
	}
	s.calls++
	r := s.results[idx]
	return r.queue, r.err
}

func (s *scriptedSource) getDeadlines() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	dls := make([]time.Time, len(s.deadlines))
	copy(dls, s.deadlines)
	return dls
}

// blockingSource blocks Fetch until released, then returns the queue.
type blockingSource struct {
	release chan struct{}
	queue   model.Queue
}

func newBlockingSource(q model.Queue) *blockingSource {
	return &blockingSource{release: make(chan struct{}), queue: q}
}

func (s *blockingSource) Fetch(ctx context.Context) (model.Queue, error) {
	select {
	case <-s.release:
		return s.queue, nil
	case <-ctx.Done():
		return model.Queue{}, ctx.Err()
	}
}

// fakeStore implements cache.Store for warm-start tests. Only Queue matters.
// saved is mutex-guarded because the daemon writes it from its poll goroutine
// while tests read it.
type fakeStore struct {
	queue   model.Queue
	savedAt time.Time
	ok      bool

	mu    sync.Mutex
	saved []model.Queue
}

var _ cache.Store = (*fakeStore)(nil)

func (f *fakeStore) Identity(context.Context) (cache.Identity, time.Time, bool) {
	return cache.Identity{}, time.Time{}, false
}
func (f *fakeStore) SaveIdentity(context.Context, cache.Identity) error { return nil }
func (f *fakeStore) PR(context.Context, string, int) (model.PullRequest, time.Time, bool) {
	return model.PullRequest{}, time.Time{}, false
}
func (f *fakeStore) SavePR(context.Context, string, int, model.PullRequest) error { return nil }
func (f *fakeStore) TouchPR(context.Context, string, int) error                   { return nil }
func (f *fakeStore) PrunePRs(context.Context, time.Duration) (int, error)         { return 0, nil }
func (f *fakeStore) Queue(context.Context) (model.Queue, time.Time, bool) {
	return f.queue, f.savedAt, f.ok
}

func (f *fakeStore) SaveQueue(ctx context.Context, q model.Queue) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	f.saved = append(f.saved, q)
	return nil
}

func (f *fakeStore) Focus(context.Context) ([]model.PRKey, time.Time, bool) {
	return nil, time.Time{}, false
}

func (f *fakeStore) SaveFocus(context.Context, []model.PRKey) error { return nil }

func (f *fakeStore) savedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

func pr(num int, title, repo string) model.PullRequest {
	return model.PullRequest{Number: num, Title: title, RepoNameWithOwner: repo, Author: "kalverra"}
}

func runningChecks() model.ChecksSummary {
	return model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}
}

func failedChecks() model.ChecksSummary {
	return model.ChecksSummary{
		Total:             2,
		Failed:            1,
		Done:              2,
		ReqTotal:          2,
		ReqFailed:         1,
		ReqDone:           2,
		HasRequiredChecks: true,
	}
}

func passingChecks() model.ChecksSummary {
	return model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}
}

func socketPath(t *testing.T) string {
	t.Helper()

	//nolint:usetesting // t.TempDir paths exceed macOS's 104-byte unix socket limit
	dir, err := os.MkdirTemp(
		"",
		"pronto",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "pronto.sock")
}

// eventCollector gathers all events from a bus subscription until stop.
type eventCollector struct {
	ch      <-chan events.Event
	timeout time.Duration
}

func collect(t *testing.T, bus *events.Bus) *eventCollector {
	t.Helper()
	ch, cancel := bus.Subscribe(events.Subscription{})
	t.Cleanup(cancel)
	return &eventCollector{ch: ch, timeout: 3 * time.Second}
}

func (c *eventCollector) drain(t *testing.T, d time.Duration) []events.Event {
	t.Helper()
	var got []events.Event
	deadline := time.After(d)
	for {
		select {
		case ev := <-c.ch:
			got = append(got, ev)
		case <-deadline:
			return got
		}
	}
}

func (c *eventCollector) waitFor(t *testing.T, typ events.Type) events.Event {
	t.Helper()
	deadline := time.After(c.timeout)
	for {
		select {
		case ev := <-c.ch:
			if ev.Type == typ {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s event", typ)
		}
	}
}

func runDaemon(t *testing.T, opts daemon.Options) *events.Bus {
	t.Helper()
	if opts.Interval == 0 {
		opts.Interval = 25 * time.Millisecond
	}
	bus := opts.Bus
	if bus == nil {
		bus = events.NewBus()
		opts.Bus = bus
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := daemon.New(opts)
	go func() { _ = d.Run(ctx) }()
	return bus
}

// progressSource reports fetch progress through the context callback, then
// returns a fixed queue.
type progressSource struct {
	queue model.Queue
}

func (s *progressSource) Fetch(ctx context.Context) (model.Queue, error) {
	progress := source.ProgressFromContext(ctx)
	if progress == nil {
		return model.Queue{}, errors.New("no progress callback in fetch context")
	}
	progress(3, 10)
	return s.queue, nil
}

func TestDaemon_RefreshEmitsFetchProgress(t *testing.T) {
	t.Parallel()

	src := &progressSource{queue: model.Queue{Viewer: "kalverra"}}
	bus := runDaemon(t, daemon.Options{Source: src})
	c := collect(t, bus)

	ev := c.waitFor(t, events.TypeFetchProgress)
	payload, ok := ev.Payload.(events.FetchProgressPayload)
	require.True(t, ok, "fetch_progress payload must be events.FetchProgressPayload, got %T", ev.Payload)
	assert.Equal(t, 3, payload.Loaded)
	assert.Equal(t, 10, payload.Total)
}

func TestDaemon_FirstFetchSeedsBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	t.Parallel()

	prWithCI := pr(42, "Feature A", "kalverra/pronto")
	prWithCI.Checks = passingChecks()
	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{prWithCI}}},
	}}

	bus := runDaemon(t, daemon.Options{Source: src})
	c := collect(t, bus)

	refreshed := c.waitFor(t, events.TypeQueueRefreshed)
	assert.GreaterOrEqual(t, refreshed.Seq, uint64(1))

	// Seeded baseline must not emit CI or add/remove events.
	time.Sleep(50 * time.Millisecond)
	for _, ev := range c.drain(t, 25*time.Millisecond) {
		assert.Equal(
			t,
			events.TypeQueueRefreshed,
			ev.Type,
			"first fetch must only emit queue_refreshed, got %s",
			ev.Type,
		)
	}
}

func TestDaemon_CIFailedEmitted(t *testing.T) {
	t.Parallel()

	running := pr(42, "Feature A", "kalverra/pronto")
	running.Checks = runningChecks()
	failed := running
	failed.Checks = failedChecks()

	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{running}}},
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{failed}}},
	}}

	bus := runDaemon(t, daemon.Options{Source: src})
	c := collect(t, bus)

	ev := c.waitFor(t, events.TypeCIFailed)
	assert.Equal(t, "kalverra/pronto", ev.Repo)
	assert.Equal(t, 42, ev.PR)
	assert.Equal(t, "Feature A", ev.Title)
}

func TestDaemon_PRAddedAndRemoved(t *testing.T) {
	t.Parallel()

	a := pr(1, "Feature A", "kalverra/pronto")
	b := pr(2, "Feature B", "kalverra/pronto")

	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{a}}},
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{a, b}}},
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{a}}},
	}}

	bus := runDaemon(t, daemon.Options{Source: src})
	c := collect(t, bus)

	added := c.waitFor(t, events.TypePRAdded)
	assert.Equal(t, 2, added.PR)
	assert.Equal(t, "Feature B", added.Title)

	removed := c.waitFor(t, events.TypePRRemoved)
	assert.Equal(t, 2, removed.PR)
	assert.Equal(t, "Feature B", removed.Title)
}

func TestDaemon_ReviewEventCarriesPayload(t *testing.T) {
	t.Parallel()

	base := pr(42, "Feature A", "kalverra/pronto")
	reviewed := base
	submitted := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	reviewed.LatestReviews = []model.Review{
		{Author: "alice", State: "APPROVED", CommitOID: "c1", SubmittedAt: submitted},
	}

	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{base}}},
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{reviewed}}},
	}}

	bus := runDaemon(t, daemon.Options{Source: src})
	c := collect(t, bus)

	ev := c.waitFor(t, events.TypeReviewReceived)
	assert.Equal(t, 42, ev.PR)

	payload, ok := ev.Payload.(events.ReviewPayload)
	require.True(t, ok, "review_received must carry a ReviewPayload, got %T", ev.Payload)
	assert.Equal(t, "alice", payload.Author)
	assert.Equal(t, "APPROVED", payload.State)
}

func TestDaemon_MergedPRCheckerEmitsMergedWithoutRemoved(t *testing.T) {
	t.Parallel()

	a := pr(42, "Feature A", "kalverra/pronto")
	checker := func(context.Context, string, int) (bool, error) { return true, nil }

	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{a}}},
		{queue: model.Queue{Viewer: "kalverra", Authored: nil}},
	}}

	bus := runDaemon(t, daemon.Options{Source: src, Checker: checker})
	c := collect(t, bus)

	merged := c.waitFor(t, events.TypePRMerged)
	assert.Equal(t, 42, merged.PR)

	eventsReceived := c.drain(t, 50*time.Millisecond)
	for _, ev := range eventsReceived {
		assert.NotEqual(t, events.TypePRRemoved, ev.Type, "merged PR must not also emit pr_removed")
	}
}

func TestDaemon_UnmergedVanishedPREmitsRemoved(t *testing.T) {
	t.Parallel()

	a := pr(42, "Feature A", "kalverra/pronto")
	checker := func(context.Context, string, int) (bool, error) { return false, nil }

	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{a}}},
		{queue: model.Queue{Viewer: "kalverra", Authored: nil}},
	}}

	bus := runDaemon(t, daemon.Options{Source: src, Checker: checker})
	c := collect(t, bus)

	removed := c.waitFor(t, events.TypePRRemoved)
	assert.Equal(t, 42, removed.PR)
}

func TestDaemon_FetchErrorKeepsLastQueue(t *testing.T) {
	t.Parallel()

	a := pr(42, "Feature A", "kalverra/pronto")
	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{a}}},
		{err: errors.New("github exploded")},
	}}

	bus := runDaemon(t, daemon.Options{Source: src})
	c := collect(t, bus)

	// First refresh is the successful one; wait for a failing one.
	c.waitFor(t, events.TypeQueueRefreshed)
	var failing *events.Event
	deadline := time.After(3 * time.Second)
	for failing == nil {
		select {
		case ev := <-c.ch:
			if ev.Type == events.TypeQueueRefreshed {
				payload, ok := ev.Payload.(events.QueueRefreshedPayload)
				if ok && !payload.OK {
					failing = &ev
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for failing queue_refreshed")
		}
	}
	require.NotNil(t, failing)
	payload, ok := failing.Payload.(events.QueueRefreshedPayload)
	require.True(t, ok)
	assert.False(t, payload.OK)
	assert.Contains(t, payload.Error, "github exploded")
}

func TestDaemon_SnapshotAndFindPR(t *testing.T) {
	t.Parallel()

	a := pr(42, "Feature A", "kalverra/pronto")
	src := &scriptedSource{results: []fetchResult{
		{
			queue: model.Queue{
				Viewer:   "kalverra",
				Authored: []model.PullRequest{a},
				Inbox:    []model.PullRequest{pr(7, "Inbox PR", "other/repo")},
			},
		},
	}}

	bus := events.NewBus()
	c := collect(t, bus)
	opts := daemon.Options{Source: src, Bus: bus}
	d := daemon.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = d.Run(ctx) }()

	_ = c.waitFor(t, events.TypeQueueRefreshed)

	snap := d.Snapshot()
	assert.Equal(t, "kalverra", snap.Queue.Viewer)
	assert.False(t, snap.FetchedAt.IsZero())
	assert.False(t, snap.Refreshing)

	got, err := d.FindPR("kalverra/pronto#42")
	require.NoError(t, err)
	assert.Equal(t, 42, got.Number)
	assert.Equal(t, "Feature A", got.Title)

	_, err = d.FindPR("#999")
	assert.Error(t, err)
}

func TestDaemon_WarmStartFromCache(t *testing.T) {
	t.Parallel()

	cached := pr(99, "Cached PR", "kalverra/pronto")
	store := &fakeStore{
		queue:   model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{cached}},
		savedAt: time.Now().Add(-time.Hour),
		ok:      true,
	}
	src := newBlockingSource(
		model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{pr(1, "Fresh", "kalverra/pronto")}},
	)

	bus := events.NewBus()
	c := collect(t, bus)
	opts := daemon.Options{Source: src, Store: store, Bus: bus}
	d := daemon.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = d.Run(ctx) }()

	// Wait for daemon to warmStart and signal Ready before releasing first fetch.
	select {
	case <-d.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("daemon never became ready")
	}

	snap := d.Snapshot()
	require.Len(t, snap.Queue.Authored, 1, "warm start must serve the cached queue")
	assert.Equal(t, 99, snap.Queue.Authored[0].Number)

	close(src.release)

	_ = c.waitFor(t, events.TypeQueueRefreshed)

	snap = d.Snapshot()
	require.Len(t, snap.Queue.Authored, 1, "snapshot must update after the first fetch")
	assert.Equal(t, 1, snap.Queue.Authored[0].Number)
	assert.Positive(t, store.savedCount(), "successful fetches must be saved back to the store")
}

func TestDaemon_StaleWarmStartDoesNotNotify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	t.Parallel()

	cached := pr(99, "Cached PR", "kalverra/pronto")
	// Saved 1 hour ago with 25ms interval.
	store := &fakeStore{
		queue:   model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{cached}},
		savedAt: time.Now().Add(-time.Hour),
		ok:      true,
	}
	src := &scriptedSource{
		results: []fetchResult{
			{
				queue: model.Queue{
					Viewer:   "kalverra",
					Authored: []model.PullRequest{pr(1, "Fresh PR", "kalverra/pronto")},
				},
			},
		},
	}

	bus := events.NewBus()
	c := collect(t, bus)

	runDaemon(t, daemon.Options{
		Source:   src,
		Store:    store,
		Bus:      bus,
		Interval: 25 * time.Millisecond,
	})

	var received []events.Event
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.ch:
			received = append(received, ev)
			if ev.Type == events.TypeQueueRefreshed {
				goto done
			}
		case <-deadline:
			t.Fatal("timed out waiting for queue_refreshed")
		}
	}
done:
	// Since warm start was stale, it must act as initial baseline:
	// Only queue_refreshed emitted; no pr_removed (for 99) or pr_added (for 1).
	for _, ev := range received {
		assert.NotEqual(t, events.TypePRAdded, ev.Type, "stale warm start must not emit pr_added on first refresh")
		assert.NotEqual(t, events.TypePRRemoved, ev.Type, "stale warm start must not emit pr_removed on first refresh")
	}
}

func TestDaemon_RunReturnsOnContextCancel(t *testing.T) {
	t.Parallel()

	src := newBlockingSource(model.Queue{})
	d := daemon.New(daemon.Options{
		Source:   src,
		Interval: time.Hour, // never ticks; only ctx cancel ends it
		Logger:   zerolog.Nop(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	select {
	case <-d.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("daemon never became ready")
	}
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not shut down promptly after context cancel")
	}
}

func TestDaemon_RunSavesFinalSnapshotOnContextCancel(t *testing.T) {
	t.Parallel()

	q := model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{pr(1, "PR 1", "kalverra/pronto")}}
	src := &scriptedSource{results: []fetchResult{{queue: q}}}
	store := &fakeStore{}
	bus := events.NewBus()
	c := collect(t, bus)
	d := daemon.New(daemon.Options{
		Source:   src,
		Store:    store,
		Bus:      bus,
		Interval: time.Hour,
		Logger:   zerolog.Nop(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	_ = c.waitFor(t, events.TypeQueueRefreshed)
	require.Equal(t, 1, store.savedCount())

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not shut down promptly after context cancel")
	}

	assert.Equal(t, 2, store.savedCount(), "daemon must save final snapshot on context cancellation")
}

func TestDaemon_SteadyStateRefreshesUseIntervalBudget(t *testing.T) {
	t.Parallel()

	q := model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{pr(1, "PR 1", "kalverra/pronto")}}
	src := &scriptedSource{results: []fetchResult{{queue: q}}}

	bus := events.NewBus()
	c := collect(t, bus)

	runDaemon(t, daemon.Options{
		Source:   src,
		Bus:      bus,
		Interval: 25 * time.Millisecond,
	})

	// Wait for first refresh (cold) and second refresh (steady-state).
	c.waitFor(t, events.TypeQueueRefreshed)
	c.waitFor(t, events.TypeQueueRefreshed)

	deadlines := src.getDeadlines()
	require.GreaterOrEqual(t, len(deadlines), 2)

	// First fetch on empty cache must use cold budget (> 3 minutes).
	assert.Greater(t, time.Until(deadlines[0]), 3*time.Minute, "cold fetch must have cold budget")

	// Second fetch must use interval budget (<= interval, not cold budget).
	assert.LessOrEqual(t, time.Until(deadlines[1]), 25*time.Millisecond, "steady-state fetch must use interval budget")
}

func TestDaemon_WarmStartRefreshesUseIntervalBudget(t *testing.T) {
	t.Parallel()

	q := model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{pr(1, "PR 1", "kalverra/pronto")}}
	src := &scriptedSource{results: []fetchResult{{queue: q}}}
	store := &fakeStore{queue: q, savedAt: time.Now(), ok: true}

	bus := events.NewBus()
	c := collect(t, bus)

	runDaemon(t, daemon.Options{
		Source:   src,
		Store:    store,
		Bus:      bus,
		Interval: 25 * time.Millisecond,
	})

	c.waitFor(t, events.TypeQueueRefreshed)

	deadlines := src.getDeadlines()
	require.GreaterOrEqual(t, len(deadlines), 1)

	// First fetch on warm cache must use interval budget, NOT cold budget.
	assert.LessOrEqual(t, time.Until(deadlines[0]), 25*time.Millisecond, "warm-start fetch must use interval budget")
}

type fnSource func() (model.Queue, error)

func (f fnSource) Fetch(context.Context) (model.Queue, error) {
	return f()
}

func TestDaemon_RefreshKicksPoll(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	t.Parallel()

	fetchCalled := make(chan struct{}, 5)
	src := fnSource(func() (model.Queue, error) {
		fetchCalled <- struct{}{}
		return model.Queue{Viewer: "kalverra"}, nil
	})

	bus := events.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	d := daemon.New(daemon.Options{
		Source:   src,
		Bus:      bus,
		Interval: 10 * time.Minute,
	})
	go func() { _ = d.Run(ctx) }()

	// Wait for initial fetch on startup.
	select {
	case <-fetchCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("initial fetch never happened")
	}

	// Trigger immediate refresh via d.Refresh().
	snap := d.Refresh()
	assert.True(t, snap.Refreshing, "snapshot immediately after Refresh should report refreshing")

	// Verify second fetch occurs promptly without waiting for the 10m interval.
	select {
	case <-fetchCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("second fetch was not triggered by d.Refresh()")
	}
}

func TestDaemon_RunWithoutSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	t.Parallel()

	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{pr(1, "Test PR", "kalverra/pronto")}}},
		{
			queue: model.Queue{
				Viewer: "kalverra",
				Authored: []model.PullRequest{
					pr(1, "Test PR", "kalverra/pronto"),
					pr(2, "Test PR 2", "kalverra/pronto"),
				},
			},
		},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := daemon.New(daemon.Options{
		Source:   src,
		Interval: 10 * time.Minute,
		// SocketPath is empty to run embedded without a socket server
	})

	require.NotNil(t, d.Bus(), "daemon should expose its bus")
	c := collect(t, d.Bus())

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Daemon must refresh and emit queue_refreshed without error.
	refreshed := c.waitFor(t, events.TypeQueueRefreshed)
	assert.Equal(t, events.TypeQueueRefreshed, refreshed.Type)

	// Snapshot must be available via public API.
	snap := d.Snapshot()
	assert.Len(t, snap.Queue.Authored, 1)
	assert.Equal(t, "Test PR", snap.Queue.Authored[0].Title)

	// Refresh must trigger a fetch without a socket.
	snapRefresh := d.Refresh()
	assert.True(t, snapRefresh.Refreshing)

	// PR added event should be emitted on the bus during diff.
	added := c.waitFor(t, events.TypePRAdded)
	assert.Equal(t, 2, added.PR)

	refreshed2 := c.waitFor(t, events.TypeQueueRefreshed)
	assert.Greater(t, refreshed2.Seq, refreshed.Seq)

	// Ensure background goroutines had time to surface any listen error before cancel.
	time.Sleep(50 * time.Millisecond)

	// Cancel ctx and verify clean shutdown with no error.
	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not shut down in time")
	}
}

func TestDaemon_RunWithoutSocket_DoesNotInterfereWithOccupiedSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	t.Parallel()

	// Create and bind a real unix socket.
	sock := socketPath(t)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Authored: []model.PullRequest{pr(1, "Test PR", "kalverra/pronto")}}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Embedded daemon runs with empty SocketPath while another process owns sock.
	d := daemon.New(daemon.Options{
		Source:     src,
		SocketPath: "",
		Interval:   10 * time.Minute,
	})

	c := collect(t, d.Bus())

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	refreshed := c.waitFor(t, events.TypeQueueRefreshed)
	assert.Equal(t, events.TypeQueueRefreshed, refreshed.Type)

	time.Sleep(50 * time.Millisecond)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not shut down in time")
	}
}

func TestDaemon_Ready_SignalsWhenBound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	t.Parallel()

	sock := socketPath(t)
	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra"}},
	}}

	ctx := t.Context()

	d := daemon.New(daemon.Options{
		Source:     src,
		SocketPath: sock,
		Interval:   10 * time.Minute,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	select {
	case <-d.Ready():
		// Socket should now be accepting connections
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sock)
		require.NoError(t, err)
		_ = conn.Close()
	case err := <-errCh:
		t.Fatalf("daemon.Run exited before becoming ready: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for d.Ready()")
	}
}

func TestDaemon_Ready_SignalsWithoutSocket(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra"}},
	}}

	ctx := t.Context()

	d := daemon.New(daemon.Options{
		Source:     src,
		SocketPath: "",
		Interval:   10 * time.Minute,
	})

	go func() { _ = d.Run(ctx) }()

	select {
	case <-d.Ready():
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for d.Ready() with empty socket")
	}
}

func TestDaemon_Run_ReturnsEarlyOnOccupiedSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	t.Parallel()

	sock := socketPath(t)
	// Bind real socket first
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	slowSrc := newBlockingSource(model.Queue{Viewer: "kalverra"})
	defer close(slowSrc.release)

	d := daemon.New(daemon.Options{
		Source:     slowSrc,
		SocketPath: sock,
		Interval:   10 * time.Minute,
	})

	start := time.Now()
	err = d.Run(context.Background())
	duration := time.Since(start)

	require.Error(t, err)
	assert.True(
		t,
		errors.Is(err, server.ErrAlreadyListening) || strings.Contains(err.Error(), "already listening"),
		"must report already listening error: %v",
		err,
	)
	assert.Less(t, duration, 1*time.Second, "Run must return early without waiting for refresh")
}

func TestDaemon_NotificationGroups_InboxAndFocus(t *testing.T) {
	t.Parallel()

	// 1. When groups includes "inbox", changes on inbox PR emit notification events.
	inboxRunning := pr(101, "Colleague Feature", "kalverra/pronto")
	inboxRunning.Author = "colleague"
	inboxRunning.Checks = runningChecks()
	inboxFailed := inboxRunning
	inboxFailed.Checks = failedChecks()

	src := &scriptedSource{results: []fetchResult{
		{queue: model.Queue{Viewer: "kalverra", Inbox: []model.PullRequest{inboxRunning}}},
		{queue: model.Queue{Viewer: "kalverra", Inbox: []model.PullRequest{inboxFailed}}},
	}}

	bus := runDaemon(t, daemon.Options{
		Source: src,
		NotificationConfig: config.NotificationConfig{
			Groups: []string{config.GroupInbox},
		},
	})
	c := collect(t, bus)

	ev := c.waitFor(t, events.TypeCIFailed)
	assert.Equal(t, 101, ev.PR)
	assert.Equal(t, "Colleague Feature", ev.Title)
}
