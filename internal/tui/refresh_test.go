package tui_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/internal/source"
	"github.com/kalverra/pronto/internal/tui"
)

// --- fakes ------------------------------------------------------------------

type scriptedSource struct {
	mu     sync.Mutex
	queues []model.Queue
	errs   []error
	calls  int

	deadline   time.Time
	deadlineOK bool
}

func (s *scriptedSource) Fetch(ctx context.Context) (model.Queue, error) {
	s.mu.Lock()
	s.calls++
	s.deadline, s.deadlineOK = ctx.Deadline()
	s.mu.Unlock()
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return model.Queue{}, err
	}
	if len(s.queues) > 0 {
		q := s.queues[0]
		s.queues = s.queues[1:]
		return q, nil
	}
	return model.Queue{}, errors.New("scripted source exhausted")
}

func (s *scriptedSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scriptedSource) fetchDeadline() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadline, s.deadlineOK
}

type memStore struct {
	mu      sync.Mutex
	queue   model.Queue
	savedAt time.Time
	ok      bool
	saved   int
}

func (m *memStore) Identity(context.Context) (cache.Identity, time.Time, bool) {
	return cache.Identity{}, time.Time{}, false
}

func (m *memStore) SaveIdentity(context.Context, cache.Identity) error { return nil }

func (m *memStore) PR(context.Context, string, int) (model.PullRequest, time.Time, bool) {
	return model.PullRequest{}, time.Time{}, false
}

func (m *memStore) SavePR(context.Context, string, int, model.PullRequest) error {
	return nil
}

func (m *memStore) TouchPR(context.Context, string, int) error {
	return nil
}

func (m *memStore) PrunePRs(context.Context, time.Duration) (int, error) {
	return 0, nil
}

func (m *memStore) Queue(context.Context) (model.Queue, time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queue, m.savedAt, m.ok
}

func (m *memStore) SaveQueue(_ context.Context, q model.Queue) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue, m.savedAt, m.ok = q, time.Now(), true
	m.saved++
	return nil
}

func (m *memStore) Focus(context.Context) ([]model.PRKey, time.Time, bool) {
	return nil, time.Time{}, false
}

func (m *memStore) SaveFocus(context.Context, []model.PRKey) error { return nil }

// --- startup ----------------------------------------------------------------

func TestStartupModel_FreshSnapshotSkipsFetch(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now(), ok: true}

	m := tui.StartupModel(context.Background(), src, store)

	assert.False(t, m.IsLoading())
	assert.False(t, m.IsRefreshing())
	assert.False(t, m.StaleSnapshot())
	assert.False(t, m.IsLoading())
	assert.NotEmpty(t, m.InboxItems(), "snapshot data must be rendered immediately")
	assert.Equal(t, 0, src.callCount(), "fresh snapshot must not trigger a fetch")
}

func TestStartupModel_StaleSnapshotFlagsRefresh(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now().Add(-10 * time.Minute), ok: true}

	m := tui.StartupModel(context.Background(), src, store)

	assert.False(t, m.IsLoading(), "stale data must still render")
	assert.NotEmpty(t, m.InboxItems())
	assert.True(t, m.StaleSnapshot(), "stale snapshot must request an immediate refresh")
}

func TestStartupModel_NoSnapshotLoads(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	store := &memStore{}

	m := tui.StartupModel(context.Background(), src, store)

	assert.True(t, m.IsLoading())
	assert.Empty(t, m.InboxItems())
}

func TestStartupModel_NilStoreLoads(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}

	m := tui.StartupModel(context.Background(), src, nil)

	assert.True(t, m.IsLoading())
}

// --- refresh cycle ----------------------------------------------------------

func TestFetchQueueCmd(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	cmd := tui.FetchQueueCmd(context.Background(), src)

	msg := cmd()
	loaded, ok := msg.(tui.QueueLoadedMsg)
	require.True(t, ok)
	require.NoError(t, loaded.Err)
	assert.NotEmpty(t, loaded.Queue.Inbox)
}

func TestModel_InitFetchesWhenLoading(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	m := tui.StartupModel(context.Background(), src, nil)

	cmd := m.Init()
	require.NotNil(t, cmd)

	msg := cmd()
	loaded, ok := msg.(tui.QueueLoadedMsg)
	require.True(t, ok, "loading model must kick an immediate fetch")
	assert.NoError(t, loaded.Err)
}

// The startup fetch may need to hydrate a large queue from an empty cache, so
// it must carry a longer deadline than the 60s refresh cadence allows.
func TestModel_InitialFetchCarriesColdDeadline(t *testing.T) {
	t.Parallel()

	newQueue := func() []model.Queue { return []model.Queue{makeTestQueue()} }

	// No snapshot: loading startup.
	src := &scriptedSource{queues: newQueue()}
	m := tui.StartupModel(context.Background(), src, nil)
	require.True(t, m.IsLoading())

	cmd := m.Init()
	require.NotNil(t, cmd)
	_ = cmd()

	deadline, ok := src.fetchDeadline()
	require.True(t, ok, "initial fetch must carry an explicit deadline")
	assert.Greater(
		t,
		time.Until(deadline),
		3*time.Minute,
		"initial fetch deadline must be minutes, not the tick budget",
	)

	// Stale snapshot startup fetches too.
	stale := &scriptedSource{queues: newQueue()}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now().Add(-10 * time.Minute), ok: true}
	m = tui.StartupModel(context.Background(), stale, store)
	require.True(t, m.StaleSnapshot())

	cmd = m.Init()
	require.NotNil(t, cmd)
	_ = cmd()

	deadline, ok = stale.fetchDeadline()
	require.True(t, ok, "stale-snapshot fetch must carry an explicit deadline")
	assert.Greater(t, time.Until(deadline), 3*time.Minute)
}

// Refreshes triggered by queue_refreshed must keep the short budget.
func TestModel_QueueRefreshedFetchKeepsShortDeadline(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now(), ok: true}
	m := tui.StartupModel(context.Background(), src, store)
	require.False(t, m.IsLoading())
	require.False(t, m.StaleSnapshot())

	_, cmd := m.Update(tui.EventMsg{Type: events.TypeQueueRefreshed})
	require.NotNil(t, cmd)

	msg := cmd()
	loaded, ok := msg.(tui.QueueLoadedMsg)
	require.True(t, ok)
	require.NoError(t, loaded.Err)

	deadline, ok := src.fetchDeadline()
	if ok {
		assert.LessOrEqual(
			t,
			time.Until(deadline),
			60*time.Second,
			"queue_refreshed fetch must stay inside the refresh interval budget",
		)
	}
}

func TestModel_QueueLoadedUpdatesItemsAndClampsCursor(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	m := tui.StartupModel(context.Background(), src, &memStore{})

	moved, _ := sendRune(m, 'j') // cursor to second of two inbox items
	m = moved.(tui.Model)

	smaller := makeTestQueue()
	smaller.Inbox = smaller.Inbox[:1]
	updated, _ := m.Update(tui.QueueLoadedMsg{Queue: smaller})

	next, ok := updated.(tui.Model)
	require.True(t, ok)
	assert.Len(t, next.InboxItems(), 1)
	assert.Equal(t, 0, next.Cursor(), "cursor must clamp to new list bounds")
	require.NoError(t, next.FetchErr())
	assert.False(t, next.IsLoading())
}

type failOnSaveStore struct {
	memStore
	t *testing.T
}

func (s *failOnSaveStore) SaveQueue(context.Context, model.Queue) error {
	s.t.Fatalf("SaveQueue must not be called by TUI client")
	return nil
}

func TestTUI_ClientModeDoesNotWriteSnapshot(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	src := &scriptedSource{queues: []model.Queue{q}}
	store := &failOnSaveStore{
		queue: q, savedAt: time.Now(), ok: true,
		t: t,
	}
	m := tui.StartupModel(context.Background(), src, store)

	// Refresh load: wasInitial is false, so no tickCmd is returned
	_, cmd := m.Update(tui.QueueLoadedMsg{Queue: q})
	if cmd != nil {
		_ = cmd()
	}

	// Close PR must not save snapshot
	if len(q.Inbox) > 0 {
		_, closeCmd := m.Update(tui.ClosePRMsg{PR: q.Inbox[0]})
		if closeCmd != nil {
			_ = closeCmd()
		}
	}
}

func TestModel_QueueLoadedAdoptsViewerFromQueue(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{}
	m := tui.StartupModel(context.Background(), src, nil)

	q := makeTestQueue()
	q.Viewer = "kalverra"
	q.Teams = []string{"myorg/engineers"}

	updated, _ := m.Update(tui.QueueLoadedMsg{Queue: q})
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	assert.Equal(t, "kalverra", next.Viewer())
	assert.Equal(t, []string{"myorg/engineers"}, next.Teams())
}

func TestModel_FetchErrorKeepsStaleData(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now(), ok: true}
	m := tui.StartupModel(context.Background(), src, store)
	require.NotEmpty(t, m.InboxItems())

	updated, cmd := m.Update(tui.QueueLoadedMsg{Err: errors.New("graphql exploded")})
	next, ok := updated.(tui.Model)
	require.True(t, ok)

	require.Nil(t, cmd, "failed refresh must not overwrite the snapshot")
	assert.NotEmpty(t, next.InboxItems(), "stale data must survive a failed refresh")
	require.Error(t, next.FetchErr())
	assert.Contains(t, next.View(), "refresh failed", "view must surface the error banner")
}

func TestModel_QueueRefreshedSkipsFetchWhileLoading(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	m := tui.StartupModel(context.Background(), src, nil)
	require.True(t, m.IsLoading())

	next, cmd := m.Update(tui.EventMsg{Type: events.TypeQueueRefreshed})
	mNext, ok := next.(tui.Model)
	require.True(t, ok)
	assert.True(t, mNext.IsLoading())
	assert.False(t, mNext.IsRefreshing(), "must not enter refreshing state while loading")
	assert.Nil(t, cmd, "must not dispatch fetch while loading")
}

func TestModel_ManualRefreshKey(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now(), ok: true}
	m := tui.StartupModel(context.Background(), src, store)
	require.False(t, m.IsLoading(), "refresh key test needs a non-loading model")

	updated, cmd := sendRune(m, 'r')
	require.NotNil(t, cmd)
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	assert.True(t, next.IsRefreshing())

	msg := cmd()
	_, isLoaded := msg.(tui.QueueLoadedMsg)
	require.True(t, isLoaded)
}

func TestModel_RefreshKeyIgnoredWithoutSource(t *testing.T) {
	t.Parallel()

	m := tui.New(makeTestQueue())
	_, cmd := sendRune(m, 'r')
	assert.Nil(t, cmd, "models without a source must ignore refresh")
}

// --- views ------------------------------------------------------------------

func TestView_LoadingState(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{}
	m := tui.StartupModel(context.Background(), src, nil)
	require.True(t, m.IsLoading())

	assert.Contains(t, m.View(), "Fetching")
}

func TestView_RefreshFailedBannerShowsDataAge(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now().Add(-3 * time.Minute), ok: true}
	m := tui.StartupModel(context.Background(), src, store)

	updated, _ := m.Update(tui.QueueLoadedMsg{Err: errors.New("gateway timeout")})
	next, ok := updated.(tui.Model)
	require.True(t, ok)

	view := next.View()
	assert.Contains(t, view, "refresh failed")
	assert.Contains(t, view, "3m ago", "banner must show data age")
}

func TestView_HelpMentionsRefresh(t *testing.T) {
	t.Parallel()

	m := tui.New(makeTestQueue())
	assert.Contains(t, m.View(), "r: refresh")
}

func TestModel_FetchProgressUpdatesLoadingProgress(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	m := tui.StartupModel(context.Background(), src, nil)
	require.True(t, m.IsLoading())

	loaded, total := m.LoadingProgress()
	assert.Equal(t, 0, loaded)
	assert.Equal(t, 0, total)

	updated, _ := m.Update(tui.FetchProgressMsg{Loaded: 8, Total: 20})
	next, ok := updated.(tui.Model)
	require.True(t, ok)

	loaded, total = next.LoadingProgress()
	assert.Equal(t, 8, loaded)
	assert.Equal(t, 20, total)

	// Reset on QueueLoadedMsg
	finished, _ := next.Update(tui.QueueLoadedMsg{Queue: makeTestQueue()})
	finalModel, ok := finished.(tui.Model)
	require.True(t, ok)
	loaded, total = finalModel.LoadingProgress()
	assert.Equal(t, 0, loaded)
	assert.Equal(t, 0, total)
}

func TestView_LoadingWithProgressBar(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{}
	m := tui.StartupModel(context.Background(), src, nil)
	require.True(t, m.IsLoading())

	// Before any progress reported: just "Fetching pull requests…"
	assert.Contains(t, m.View(), "Fetching pull requests…")
	assert.NotContains(t, m.View(), "0/0")

	// After progress reported
	updated, _ := m.Update(tui.FetchProgressMsg{Loaded: 12, Total: 24})
	next, ok := updated.(tui.Model)
	require.True(t, ok)

	view := next.View()
	assert.Contains(t, view, "Fetching pull requests")
	assert.Contains(t, view, "12/24")
	assert.NotContains(t, view, "█", "loading view must use spinner and counts without block progress bar")
}

func TestModel_QueueLoadedAfterInitialFetchArmsNoTick(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	store := &memStore{}
	m := tui.StartupModel(context.Background(), src, store)
	require.True(t, m.IsLoading())

	updated, cmd := m.Update(tui.QueueLoadedMsg{Queue: makeTestQueue()})
	_, ok := updated.(tui.Model)
	require.True(t, ok)
	assert.Nil(t, cmd, "initial load completion must keep no cadence and arm no tick")
}

func TestModel_FetchErrorAfterInitialFetchArmsNoTick(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{}
	m := tui.StartupModel(context.Background(), src, nil)
	require.True(t, m.IsLoading())

	updated, cmd := m.Update(tui.QueueLoadedMsg{Err: errors.New("boom")})
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	require.Error(t, next.FetchErr())
	assert.Nil(t, cmd, "failed initial load must arm no tick; client relies on push events")
}

func TestModel_RefreshKeyIgnoredDuringStaleSnapshotFetch(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now().Add(-10 * time.Minute), ok: true}
	m := tui.StartupModel(context.Background(), src, store)
	require.True(t, m.StaleSnapshot())

	updated, cmd := sendRune(m, 'r')
	_, ok := updated.(tui.Model)
	require.True(t, ok)
	require.Nil(t, cmd, "manual refresh must not stack on the in-flight startup fetch")
}

type progressSource struct {
	total int
}

func (p *progressSource) Fetch(ctx context.Context) (model.Queue, error) {
	if fn := source.ProgressFromContext(ctx); fn != nil {
		fn(0, p.total)
		fn(p.total/2, p.total)
		fn(p.total, p.total)
	}
	return makeTestQueue(), nil
}

func TestFetchQueueCmd_StreamsProgress(t *testing.T) {
	t.Parallel()

	src := &progressSource{total: 10}
	cmd := tui.FetchQueueCmd(context.Background(), src)

	// First message: progress 0/10
	msg := cmd()
	prog1, ok := msg.(tui.FetchProgressMsg)
	require.True(t, ok, "expected FetchProgressMsg, got %T", msg)
	assert.Equal(t, 0, prog1.Loaded)
	assert.Equal(t, 10, prog1.Total)

	// Update model with prog1 to get next cmd
	m := tui.StartupModel(context.Background(), src, nil)
	updated, nextCmd := m.Update(prog1)
	m = updated.(tui.Model)
	require.NotNil(t, nextCmd)

	// Second message: progress 5/10
	msg = nextCmd()
	prog2, ok := msg.(tui.FetchProgressMsg)
	require.True(t, ok, "expected FetchProgressMsg, got %T", msg)
	assert.Equal(t, 5, prog2.Loaded)
	assert.Equal(t, 10, prog2.Total)

	// Third message: progress 10/10
	updated, nextCmd = m.Update(prog2)
	m = updated.(tui.Model)
	require.NotNil(t, nextCmd)

	msg = nextCmd()
	prog3, ok := msg.(tui.FetchProgressMsg)
	require.True(t, ok, "expected FetchProgressMsg, got %T", msg)
	assert.Equal(t, 10, prog3.Loaded)
	assert.Equal(t, 10, prog3.Total)

	// Final message: QueueLoadedMsg
	_, nextCmd = m.Update(prog3)
	require.NotNil(t, nextCmd)

	msg = nextCmd()
	loaded, ok := msg.(tui.QueueLoadedMsg)
	require.True(t, ok, "expected QueueLoadedMsg, got %T", msg)
	require.NoError(t, loaded.Err)
	assert.NotEmpty(t, loaded.Queue.Inbox)
}

func TestModel_QueueRefreshedSkipsFetchDuringStaleSnapshotFetch(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now().Add(-10 * time.Minute), ok: true}
	m := tui.StartupModel(context.Background(), src, store)
	require.True(t, m.StaleSnapshot())

	updated, cmd := m.Update(tui.EventMsg{Type: events.TypeQueueRefreshed})
	next, ok := updated.(tui.Model)
	require.True(t, ok)

	assert.False(t, next.IsRefreshing(),
		"queue_refreshed must not stack a second fetch on the in-flight cold-start fetch")
	assert.Nil(t, cmd)
	assert.Equal(t, 0, src.callCount())
}

func TestView_RefreshFailedBeforeAnyDataOmitsAge(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{}
	m := tui.StartupModel(context.Background(), src, nil)
	require.True(t, m.IsLoading())

	updated, _ := m.Update(tui.QueueLoadedMsg{Err: errors.New("gateway timeout")})
	next, ok := updated.(tui.Model)
	require.True(t, ok)

	view := next.View()
	assert.Contains(t, view, "refresh failed")
	assert.NotContains(t, view, "ago", "no data has ever been fetched; age must be omitted")
}

// --- Item 28: Event-driven TUI tests ----------------------------------------

type fakeEventDaemonSource struct {
	mu           sync.Mutex
	queue        model.Queue
	fetchCalls   int
	refreshCalls int
	eventsCh     chan events.Event
}

func (s *fakeEventDaemonSource) Fetch(_ context.Context) (model.Queue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetchCalls++
	return s.queue, nil
}

func (s *fakeEventDaemonSource) IsDaemon() bool {
	return true
}

func (s *fakeEventDaemonSource) Refresh(_ context.Context) (server.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshCalls++
	return server.Snapshot{Queue: s.queue, Refreshing: true}, nil
}

func (s *fakeEventDaemonSource) Subscribe(
	_ context.Context,
	_ ...events.Subscription,
) (<-chan events.Event, error) {
	return s.eventsCh, nil
}

func TestModel_QueueRefreshedEventTriggersSnapshotReload(t *testing.T) {
	t.Parallel()

	q1 := makeTestQueue()
	q2 := makeTestQueue()
	q2.Inbox = append(q2.Inbox, model.PullRequest{
		Number:            99,
		Title:             "Newly refreshed PR",
		RepoNameWithOwner: "kalverra/pronto",
	})

	src := &scriptedSource{queues: []model.Queue{q2}}
	store := &memStore{queue: q1, savedAt: time.Now(), ok: true}
	eventsCh := make(chan events.Event, 1)

	m := tui.StartupModel(context.Background(), src, store, tui.WithEvents(eventsCh))
	require.False(t, m.IsRefreshing())

	updated, cmd := m.Update(tui.EventMsg{
		Type: events.TypeQueueRefreshed,
	})
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	assert.True(t, next.IsRefreshing(), "queue_refreshed event must set refreshing = true")
	require.NotNil(t, cmd, "queue_refreshed event must return a command to re-read the snapshot")

	close(eventsCh)
	msg := cmd()
	var loaded tui.QueueLoadedMsg
	var foundLoaded bool
	if l, ok := msg.(tui.QueueLoadedMsg); ok {
		loaded = l
		foundLoaded = true
	} else if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if l, ok := c().(tui.QueueLoadedMsg); ok {
				loaded = l
				foundLoaded = true
				break
			}
		}
	}
	require.True(t, foundLoaded, "command must reload queue, got %T", msg)
	require.NoError(t, loaded.Err)
	assert.Len(t, loaded.Queue.Inbox, len(q2.Inbox))
}

func TestModel_TriggerEventRendersPopupAndBanner(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	notifier := &mockNotifier{}
	eventsCh := make(chan events.Event, 1)

	m := tui.New(q, tui.WithNotifier(notifier), tui.WithEvents(eventsCh))
	require.Nil(t, m.LastNotification())

	ev := events.Event{
		Type:  events.TypeCIPassed,
		Repo:  "kalverra/pronto",
		PR:    42,
		Title: "Add event-driven TUI",
	}

	updated, cmd := m.Update(tui.EventMsg{Event: ev})
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	require.NotNil(t, cmd, "trigger event must dispatch a notification command")

	close(eventsCh)
	msg := cmd()
	require.NotNil(t, msg)
	var notifMsg tui.NotificationMsg
	var foundNotif bool
	if n, ok := msg.(tui.NotificationMsg); ok {
		notifMsg = n
		foundNotif = true
	} else if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if n, ok := c().(tui.NotificationMsg); ok {
				notifMsg = n
				foundNotif = true
				break
			}
		}
	}
	require.True(t, foundNotif, "expected NotificationMsg, got %T", msg)
	require.Len(t, notifMsg.Notifications, 1)
	assert.Equal(t, 42, notifMsg.Notifications[0].PRNumber)

	// Updating with the notification message sets LastNotification and renders top notification
	updated2, _ := next.Update(notifMsg)
	next2 := updated2.(tui.Model)
	require.NotNil(t, next2.LastNotification())
	assert.Equal(t, 42, next2.LastNotification().PRNumber)
	assert.Contains(t, next2.View(), "CI PASS")
}

func TestModel_ManualRefreshKey_DaemonModeSendsQueueRefresh(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	daemonSrc := &fakeEventDaemonSource{
		queue:    q,
		eventsCh: make(chan events.Event, 1),
	}
	store := &memStore{queue: q, savedAt: time.Now(), ok: true}
	m := tui.StartupModel(context.Background(), daemonSrc, store)
	require.False(t, m.IsRefreshing())

	updated, cmd := sendRune(m, 'r')
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	assert.True(t, next.IsRefreshing(), "r key in daemon mode must enter refreshing state")
	require.NotNil(t, cmd, "r key in daemon mode must return a refresh command")

	_ = cmd()
	daemonSrc.mu.Lock()
	refreshCalls := daemonSrc.refreshCalls
	fetchCalls := daemonSrc.fetchCalls
	daemonSrc.mu.Unlock()

	assert.Equal(t, 1, refreshCalls, "r key in daemon mode must call Refresh on daemon source")
	assert.Equal(t, 0, fetchCalls, "r key in daemon mode must NOT call Fetch directly")
}

func TestStartupModel_SubscribesToDaemonSource(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	eventsCh := make(chan events.Event, 1)
	daemonSrc := &fakeEventDaemonSource{
		queue:    q,
		eventsCh: eventsCh,
	}
	store := &memStore{queue: q, savedAt: time.Now(), ok: true}

	m := tui.StartupModel(context.Background(), daemonSrc, store)
	cmd := m.Init()
	require.NotNil(t, cmd, "Init() must arm event subscription command")

	// Send an event on the channel and verify Init's command receives it
	eventsCh <- events.Event{Type: events.TypeQueueRefreshed}
	msg := cmd()
	eventMsg, ok := msg.(tui.EventMsg)
	require.True(t, ok, "Init command must deliver EventMsg from subscribed channel, got %T", msg)
	assert.Equal(t, events.TypeQueueRefreshed, eventMsg.Type)
}

// daemonProgressSource reports fetch progress through the context callback,
// then returns a fixed queue.
type daemonProgressSource struct {
	queue model.Queue
}

func (s *daemonProgressSource) Fetch(ctx context.Context) (model.Queue, error) {
	if progress := source.ProgressFromContext(ctx); progress != nil {
		progress(3, 10)
	}
	return s.queue, nil
}

// TestStartupModel_EmbeddedDaemonStreamsFetchProgress replicates the real
// embedded-daemon wiring end to end: a daemon with a progress-reporting
// source, an in-process daemon.Source, and a stale cached snapshot. The
// startup model must receive fetch_progress events through its live event
// subscription and expose them via LoadingProgress.
func TestStartupModel_EmbeddedDaemonStreamsFetchProgress(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	src := &daemonProgressSource{queue: makeTestQueue()}
	d := daemon.New(daemon.Options{
		Source:   src,
		Bus:      events.NewBus(),
		Interval: 50 * time.Millisecond,
	})
	go func() { _ = d.Run(ctx) }()
	select {
	case <-d.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("daemon never became ready")
	}

	store := &memStore{queue: makeTestQueue(), savedAt: time.Now().Add(-10 * time.Minute), ok: true}
	m := tui.StartupModel(ctx, daemon.NewSource(d), store)
	require.True(t, m.StaleSnapshot(), "stale snapshot must flag an immediate refresh")

	runCmds(t, m.Init(), func(msg tea.Msg) bool {
		if ev, ok := msg.(tui.EventMsg); ok && ev.Type == events.TypeFetchProgress {
			updated, _ := m.Update(msg)
			next := updated.(tui.Model)
			loaded, total := next.LoadingProgress()
			assert.Equal(t, 3, loaded)
			assert.Equal(t, 10, total)
			return true
		}
		return false
	})
}

// runCmds executes Bubbletea commands (flattening batch messages) until pred
// returns true, failing the test after timeout.
func runCmds(t *testing.T, cmd tea.Cmd, pred func(tea.Msg) bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for predicted message")
		default:
		}
		next := pending[0]
		pending = pending[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if pred(msg) {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			pending = append(pending, batch...)
		}
	}
}

func TestView_InFlightFetchShowsBannerProgressBar(t *testing.T) {
	t.Parallel()

	// Fresh snapshot, not stale, not refreshing: progress events alone must
	// surface the spinner during any background fetch.
	m := tui.New(makeTestQueue())

	updated, _ := m.Update(tui.EventMsg{
		Type:    events.TypeFetchProgress,
		Payload: events.FetchProgressPayload{Loaded: 4, Total: 9},
	})
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	require.False(t, next.IsRefreshing())
	require.False(t, next.StaleSnapshot())

	view := next.View()
	assert.Contains(t, view, "refreshing")
	assert.Contains(t, view, "4/9")
	assert.NotContains(t, view, "█", "refresh indicator must use a compact spinner, not a chunky progress bar")
}

func TestView_CompletedFetchHidesBannerProgressBar(t *testing.T) {
	t.Parallel()

	m := tui.New(makeTestQueue())

	updated, _ := m.Update(tui.EventMsg{
		Type:    events.TypeFetchProgress,
		Payload: events.FetchProgressPayload{Loaded: 9, Total: 9},
	})
	next, ok := updated.(tui.Model)
	require.True(t, ok)

	assert.NotContains(t, next.View(), "9/9",
		"completed fetch must clear the refresh indicator")
}

func TestModel_FetchProgressEventUpdatesLoadingProgress(t *testing.T) {
	t.Parallel()

	eventsCh := make(chan events.Event, 1)
	m := tui.New(makeTestQueue(), tui.WithEvents(eventsCh))
	_, before := m.LoadingProgress()
	require.Zero(t, before, "no fetch in flight; total must start at zero")

	updated, cmd := m.Update(tui.EventMsg{
		Type:    events.TypeFetchProgress,
		Payload: events.FetchProgressPayload{Loaded: 4, Total: 9},
	})
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	require.NotNil(t, cmd, "event handler must re-arm the event wait command")

	loaded, total := next.LoadingProgress()
	assert.Equal(t, 4, loaded)
	assert.Equal(t, 9, total)
}

func TestView_StaleSnapshotBannerShowsProgressBar(t *testing.T) {
	t.Parallel()

	src := &scriptedSource{queues: []model.Queue{makeTestQueue()}}
	store := &memStore{queue: makeTestQueue(), savedAt: time.Now().Add(-10 * time.Minute), ok: true}
	m := tui.StartupModel(context.Background(), src, store)
	require.True(t, m.StaleSnapshot(), "10-minute-old snapshot must flag stale")

	updated, _ := m.Update(tui.EventMsg{
		Type:    events.TypeFetchProgress,
		Payload: events.FetchProgressPayload{Loaded: 4, Total: 9},
	})
	next, ok := updated.(tui.Model)
	require.True(t, ok)

	view := next.View()
	assert.Contains(t, view, "refreshing")
	assert.Contains(t, view, "4/9", "top header must show fetch counts")
	assert.NotContains(t, view, "█", "must not render a chunky progress bar")
}

func TestSpinnerTick_AdvancesWhileLoadingOrRefreshing(t *testing.T) {
	t.Parallel()

	// 1. Loading model advances spinner frame on tick
	mLoading := tui.StartupModel(context.Background(), &scriptedSource{}, nil)
	require.True(t, mLoading.IsLoading())
	require.Zero(t, mLoading.SpinnerFrame())

	updated, _ := mLoading.Update(tui.SpinnerTickMsg{})
	nextLoading, ok := updated.(tui.Model)
	require.True(t, ok)
	assert.Equal(t, 1, nextLoading.SpinnerFrame(), "loading model must advance spinner frame on tick")

	// 2. Refreshing model advances spinner frame on tick
	q := makeTestQueue()
	mRefreshing := tui.New(q)
	updatedRef, _ := mRefreshing.Update(tui.EventMsg{
		Type:    events.TypeFetchProgress,
		Payload: events.FetchProgressPayload{Loaded: 2, Total: 10},
	})
	nextRef, ok := updatedRef.(tui.Model)
	require.True(t, ok)
	require.Zero(t, nextRef.SpinnerFrame())

	updatedRef2, _ := nextRef.Update(tui.SpinnerTickMsg{})
	nextRef2, ok := updatedRef2.(tui.Model)
	require.True(t, ok)
	assert.Equal(t, 1, nextRef2.SpinnerFrame(), "refreshing model must advance spinner frame on tick")
}

func TestView_LoadingStateShowsSpinnerAndMinimalHelp(t *testing.T) {
	t.Parallel()

	m := tui.StartupModel(context.Background(), &scriptedSource{}, nil)
	require.True(t, m.IsLoading())

	view := m.View()
	assert.Contains(t, view, "Fetching pull requests…")
	assert.Contains(t, view, "⠋", "loading body must show animated spinner frame")
	assert.NotContains(t, view, "█")
	// Minimal help text during initial loading
	assert.Contains(t, view, "q: quit")
	assert.NotContains(t, view, "enter: details")
	assert.NotContains(t, view, "x: close stale")
}

func TestView_LoadingStateWithProgress(t *testing.T) {
	t.Parallel()

	m := tui.StartupModel(context.Background(), &scriptedSource{}, nil)
	updated, _ := m.Update(tui.FetchProgressMsg{Loaded: 3, Total: 10})
	next := updated.(tui.Model)

	view := next.View()
	assert.Contains(t, view, "3 of 10")
	assert.Contains(t, view, "⠋")
	assert.NotContains(t, view, "█")
}
