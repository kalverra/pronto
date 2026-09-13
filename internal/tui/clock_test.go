package tui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
)

func TestHandleQueueLoaded_AdvancesClockWhenNotInjected(t *testing.T) {
	t.Parallel()

	startup := time.Now().Add(-time.Hour)

	// Production models get now from New without WithNow; a later refresh
	// must recompute staleness against the current clock, not startup time.
	m := Model{now: startup, activeTab: TabInbox}
	got, _ := m.handleQueueLoaded(QueueLoadedMsg{Queue: model.Queue{}})
	require.True(t, got.now.After(startup),
		"clock must advance on refresh so PRs age while the app runs")
}

func TestHandleQueueLoaded_KeepsInjectedClock(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	m := Model{now: fixed, nowInjected: true, activeTab: TabInbox}

	got, _ := m.handleQueueLoaded(QueueLoadedMsg{Queue: model.Queue{}})
	assert.True(t, got.now.Equal(fixed), "injected clock must stay fixed for deterministic tests")
}

func TestHandleQueueLoaded_RecordsFetchTime(t *testing.T) {
	t.Parallel()

	before := time.Now()
	m := Model{now: before, nowInjected: true, activeTab: TabInbox}

	got, _ := m.handleQueueLoaded(QueueLoadedMsg{Queue: model.Queue{}})
	assert.False(t, got.lastFetch.Before(before), "successful load must record fetch time")
}

func TestHandleQueueLoaded_ErrorKeepsPreviousFetchTime(t *testing.T) {
	t.Parallel()

	fetchAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	m := Model{now: fetchAt, nowInjected: true, lastFetch: fetchAt, activeTab: TabInbox}

	got, _ := m.handleQueueLoaded(QueueLoadedMsg{Err: assert.AnError})
	assert.True(t, got.lastFetch.Equal(fetchAt), "failed load must not touch lastFetch")
}

func TestRemovePR_DoesNotAdvanceLastFetch(t *testing.T) {
	t.Parallel()

	fetchAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{{Number: 101, Stale: true, UpdatedAt: now.Add(-40 * 24 * time.Hour)}},
	}

	m := Model{queue: q, now: now, nowInjected: true, lastFetch: fetchAt, activeTab: TabInbox}

	got := m.removePR(q.Inbox[0])
	assert.True(t, got.lastFetch.Equal(fetchAt), "removing a PR is not a fetch; lastFetch must not advance")
	assert.Empty(t, got.queue.Inbox)
}
