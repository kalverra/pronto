package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rs/zerolog"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/source"
)

const (
	// SnapshotFreshFor is how old a queue snapshot may be before the TUI
	// kicks an immediate refresh on startup.
	SnapshotFreshFor = 5 * time.Minute
)

// QueueLoadedMsg carries the outcome of a background or initial fetch.
type QueueLoadedMsg struct {
	Queue model.Queue
	Err   error
}

// FetchProgressMsg carries incremental progress during queue fetching.
type FetchProgressMsg struct {
	Loaded int
	Total  int
	next   <-chan tea.Msg
}

// EventMsg carries an event from the daemon to the TUI.
type EventMsg struct {
	events.Event
}

// NotificationMsg carries newly detected notifications for the user.
type NotificationMsg struct {
	Notifications []notify.Notification
}

func notifyCmd(
	ctx context.Context,
	detector *notify.Detector,
	notifier notify.Notifier,
	logger zerolog.Logger,
	prev, curr []model.PullRequest,
) tea.Cmd {
	if detector == nil {
		return nil
	}
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		notes, err := detector.DetectChanges(ctx, prev, curr)
		if err != nil {
			logger.Error().Err(err).Msg("change detection failed")
			return nil
		}
		if len(notes) == 0 {
			return nil
		}
		if notifier != nil {
			for _, n := range notes {
				if nErr := notifier.Notify(ctx, n); nErr != nil {
					logNotifyFailure(logger, nErr, n)
				}
			}
		}
		return NotificationMsg{Notifications: notes}
	}
}

// FetchQueueCmd builds a Bubbletea command that fetches the queue and streams progress.
func FetchQueueCmd(ctx context.Context, src source.Source) tea.Cmd {
	return fetchQueueCmd(ctx, src, 0)
}

// FetchQueueColdCmd builds a fetch command under the cold-start budget: the
// initial load may need to hydrate a large queue from an empty cache, which
// cannot fit the refresh-tick deadline.
func FetchQueueColdCmd(ctx context.Context, src source.Source) tea.Cmd {
	return fetchQueueCmd(ctx, src, source.ColdFetchTimeout)
}

func fetchQueueCmd(ctx context.Context, src source.Source, timeout time.Duration) tea.Cmd {
	fetchCtx, done := withFetchTimeout(ctx, timeout)
	ch := make(chan tea.Msg, 16)
	go func() {
		defer close(ch)
		defer done()
		progressCtx := source.WithProgress(fetchCtx, func(loaded, total int) {
			select {
			case <-fetchCtx.Done():
			case ch <- FetchProgressMsg{Loaded: loaded, Total: total, next: ch}:
			}
		})
		q, err := src.Fetch(progressCtx)
		select {
		case <-fetchCtx.Done():
		case ch <- QueueLoadedMsg{Queue: q, Err: err}:
		}
	}()

	return func() tea.Msg {
		return <-ch
	}
}

// withFetchTimeout applies d to ctx when positive, returning a cleanup the
// caller must defer for the lifetime of the fetch.
func withFetchTimeout(ctx context.Context, d time.Duration) (context.Context, func()) {
	if d <= 0 {
		return ctx, func() {}
	}
	timed, cancel := context.WithTimeout(ctx, d)
	return timed, cancel
}

func waitForFetchMsg(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// WaitForEventCmd waits for the next event on ch.
func WaitForEventCmd(ch <-chan events.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return EventMsg{Event: ev}
	}
}

// StartupModel builds the initial TUI model from snapshot state: a fresh
// snapshot renders instantly without fetching, a stale one renders and kicks
// a refresh, and no snapshot starts in a loading state.
func StartupModel(ctx context.Context, src source.Source, store cache.Store, opts ...Option) Model {
	if ds, ok := src.(interface{ IsDaemon() bool }); ok && ds.IsDaemon() {
		opts = append([]Option{WithoutChangeDetection()}, opts...)
	}
	if sub, ok := src.(interface {
		Subscribe(context.Context, ...events.Subscription) (<-chan events.Event, error)
	}); ok {
		if ch, err := sub.Subscribe(ctx); err == nil {
			opts = append(opts, WithEvents(ch))
		}
	}
	if store != nil {
		opts = append(opts, WithStore(store))
		if keys, _, ok := store.Focus(ctx); ok {
			opts = append(opts, WithFocusedPRs(keys))
		}
		if q, savedAt, ok := store.Queue(ctx); ok {
			q.Authored = filterBogusPRs(q.Authored)
			q.Inbox = filterBogusPRs(q.Inbox)
			m := New(q, opts...)
			m.src = src
			m.ctx = ctx
			m.lastFetch = savedAt
			if time.Since(savedAt) >= SnapshotFreshFor {
				m.staleSnapshot = true
			}
			return m
		}
	}

	m := New(model.Queue{}, opts...)
	m.src = src
	m.ctx = ctx
	m.loading = true
	return m
}

// Run starts the interactive TUI.
func Run(ctx context.Context, src source.Source, store cache.Store, opts ...Option) error {
	m := StartupModel(ctx, src, store, opts...)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen(), tea.WithMouseCellMotion())
	go func() {
		ticker := time.NewTicker(spinnerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				program.Send(SpinnerTickMsg{})
			}
		}
	}()
	_, err := program.Run()
	return err
}

func filterBogusPRs(prs []model.PullRequest) []model.PullRequest {
	if len(prs) == 0 {
		return prs
	}
	filtered := make([]model.PullRequest, 0, len(prs))
	for _, pr := range prs {
		if pr.Title != "Embedded PR" {
			filtered = append(filtered, pr)
		}
	}
	return filtered
}
