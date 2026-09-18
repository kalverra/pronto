package tui_test

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/tui"
)

type mockNotifier struct {
	mu    sync.Mutex
	notes []notify.Notification
}

func (m *mockNotifier) Notify(_ context.Context, n notify.Notification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notes = append(m.notes, n)
	return nil
}

func (m *mockNotifier) getNotes() []notify.Notification {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]notify.Notification, len(m.notes))
	copy(copied, m.notes)
	return copied
}

func TestModel_InitialLoad_SeedsDetectorWithoutNotifying(t *testing.T) {
	t.Parallel()

	notifier := &mockNotifier{}
	detector := notify.NewDetector(nil)

	// StartupModel with loading=true
	m := tui.StartupModel(
		context.Background(),
		nil,
		nil,
		tui.WithNotifier(notifier),
		tui.WithDetector(detector),
	)
	require.True(t, m.IsLoading())

	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Feature",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:             2,
					Done:              2,
					ReqTotal:          2,
					ReqDone:           2,
					HasRequiredChecks: true,
				},
			},
		},
	}

	updated, cmd := m.Update(tui.QueueLoadedMsg{Queue: q})
	mod := updated.(tui.Model)
	assert.False(t, mod.IsLoading())

	// Initial load returns no notification command and arms no tick
	assert.Nil(t, cmd)

	assert.Empty(t, notifier.getNotes(), "initial load must not fire notifications")
	assert.Nil(t, mod.LastNotification())
}

func TestModel_Refresh_TriggersNotifierAndBanner(t *testing.T) {
	t.Parallel()

	notifier := &mockNotifier{}
	detector := notify.NewDetector(nil)

	initialQ := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Feature",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:             2,
					Running:           1,
					ReqTotal:          2,
					ReqRunning:        1,
					HasRequiredChecks: true,
				},
			},
		},
	}

	m := tui.New(
		initialQ,
		tui.WithNotifier(notifier),
		tui.WithDetector(detector),
	)

	// Subsequent refresh with CI passed
	refreshedQ := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Feature",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:             2,
					Done:              2,
					ReqTotal:          2,
					ReqDone:           2,
					HasRequiredChecks: true,
				},
			},
		},
	}

	updated, cmd := m.Update(tui.QueueLoadedMsg{Queue: refreshedQ})
	mod := updated.(tui.Model)

	require.NotNil(t, cmd, "refresh with transitions must produce a notification command")
	msg := cmd()
	require.NotNil(t, msg)

	// Update model with the notification message
	updated2, _ := mod.Update(msg)
	mod2 := updated2.(tui.Model)

	require.Len(t, notifier.getNotes(), 1)
	assert.Equal(t, notify.TriggerCIPassed, notifier.getNotes()[0].Trigger)

	require.NotNil(t, mod2.LastNotification())
	assert.Equal(t, notify.TriggerCIPassed, mod2.LastNotification().Trigger)
	assert.Contains(t, mod2.View(), "CI Passed")

	// Navigation past bottom focuses the notification banner
	navUpdated, _ := mod2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	navMod := navUpdated.(tui.Model)
	assert.True(t, navMod.IsNotificationFocused())

	// Esc dismisses the notification banner
	escUpdated, _ := navMod.Update(tea.KeyMsg{Type: tea.KeyEsc})
	escMod := escUpdated.(tui.Model)
	assert.Nil(t, escMod.LastNotification())
}

func TestModel_Notification_BannerColorsAndPrefix(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Authored: []model.PullRequest{
			{Number: 1, Title: "Feature", RepoNameWithOwner: "kalverra/pronto"},
		},
	}

	t.Run("CI failed is red and omits PRonto prefix", func(t *testing.T) {
		t.Parallel()
		m := tui.New(q)
		updated, _ := m.Update(tui.NotificationMsg{
			Notifications: []notify.Notification{
				{
					Trigger:  notify.TriggerCIFailed,
					PRNumber: 1,
					Title:    "PRonto: CI Failed (#1)",
					Message:  "CI failed for Feature (kalverra/pronto#1)",
					URL:      "https://github.com/kalverra/pronto/pull/1",
				},
			},
		})
		mod := updated.(tui.Model)
		view := mod.View()
		assert.Contains(t, view, "CI Failed (#1)")
		assert.NotContains(t, view, "PRonto:")
		// Must NOT use green color #3fb950 (63;185;80)
		assert.NotContains(t, view, "63;185;80")
	})

	t.Run("conflict is red", func(t *testing.T) {
		t.Parallel()
		m := tui.New(q)
		updated, _ := m.Update(tui.NotificationMsg{
			Notifications: []notify.Notification{
				{
					Trigger:  notify.TriggerConflict,
					PRNumber: 1,
					Title:    "Merge Conflict (#1)",
					Message:  "Merge conflict in Feature (kalverra/pronto#1)",
					URL:      "https://github.com/kalverra/pronto/pull/1",
				},
			},
		})
		mod := updated.(tui.Model)
		view := mod.View()
		assert.Contains(t, view, "Merge Conflict (#1)")
		assert.NotContains(t, view, "63;185;80")
	})

	t.Run("changes requested is red", func(t *testing.T) {
		t.Parallel()
		m := tui.New(q)
		updated, _ := m.Update(tui.NotificationMsg{
			Notifications: []notify.Notification{
				{
					Trigger:     notify.TriggerReviewReceived,
					ReviewState: "CHANGES_REQUESTED",
					PRNumber:    1,
					Title:       "Review on #1",
					Message:     "@alice Changes requested",
					URL:         "https://github.com/kalverra/pronto/pull/1",
				},
			},
		})
		mod := updated.(tui.Model)
		view := mod.View()
		assert.Contains(t, view, "Review on #1")
		assert.NotContains(t, view, "63;185;80")
	})
}

func TestModel_Notification_ToggleFocusShortcut(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Authored: []model.PullRequest{
			{Number: 1, Title: "Feature", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q)
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerCIFailed,
				PRNumber: 1,
				Title:    "CI Failed (#1)",
				URL:      "https://github.com/kalverra/pronto/pull/1",
			},
		},
	})
	mod := updated.(tui.Model)
	require.NotNil(t, mod.LastNotification())
	assert.False(t, mod.IsNotificationFocused())

	// Press 'n' to focus
	modFocused, _ := mod.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mFocused := modFocused.(tui.Model)
	assert.True(t, mFocused.IsNotificationFocused())

	// Press 'n' again to unfocus
	modUnfocused, _ := mFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mUnfocused := modUnfocused.(tui.Model)
	assert.False(t, mUnfocused.IsNotificationFocused())
}

func TestModel_Notification_NavigateDownToFocus(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Authored: []model.PullRequest{
			{Number: 1, Title: "Feature 1", RepoNameWithOwner: "kalverra/pronto"},
			{Number: 2, Title: "Feature 2", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q)
	// Switch to Mine tab
	mTab, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	mod := mTab.(tui.Model)

	updated, _ := mod.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerCIFailed,
				PRNumber: 1,
				Title:    "CI Failed (#1)",
				URL:      "https://github.com/kalverra/pronto/pull/1",
			},
		},
	})
	mWithNote := updated.(tui.Model)
	assert.Equal(t, 0, mWithNote.Cursor())
	assert.False(t, mWithNote.IsNotificationFocused())

	// Move down to row 1 (last row in table) - should stay in table, not clear notification
	mRow1, _ := mWithNote.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	modRow1 := mRow1.(tui.Model)
	assert.Equal(t, 1, modRow1.Cursor())
	assert.False(t, modRow1.IsNotificationFocused())
	assert.NotNil(t, modRow1.LastNotification(), "navigating within table must not clear notification")

	// Move down past bottom row -> focuses notification banner
	mNoteFocus, _ := modRow1.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	modNoteFocus := mNoteFocus.(tui.Model)
	assert.True(t, modNoteFocus.IsNotificationFocused(), "moving down past last row must focus notification banner")

	// Move up from focused notification -> returns to last row in PR table
	mUp, _ := modNoteFocus.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	modUp := mUp.(tui.Model)
	assert.False(t, modUp.IsNotificationFocused())
	assert.Equal(t, 1, modUp.Cursor())
}

func TestModel_Notification_EnterLinksToAppropriateURL(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Authored: []model.PullRequest{
			{Number: 1, Title: "Feature", RepoNameWithOwner: "kalverra/pronto"},
		},
	}

	t.Run("CI failure links to PR /checks URL", func(t *testing.T) {
		t.Parallel()
		var openedURL string
		m := tui.New(q, tui.WithOpener(func(rawURL string) error {
			openedURL = rawURL
			return nil
		}))
		updated, _ := m.Update(tui.NotificationMsg{
			Notifications: []notify.Notification{
				{
					Trigger:  notify.TriggerCIFailed,
					PRNumber: 1,
					Title:    "CI Failed (#1)",
					URL:      "https://github.com/kalverra/pronto/pull/1",
				},
			},
		})
		mod := updated.(tui.Model)
		// Focus notification
		modF, _ := mod.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		mF := modF.(tui.Model)
		require.True(t, mF.IsNotificationFocused())

		// Press Enter
		_, cmd := mF.Update(tea.KeyMsg{Type: tea.KeyEnter})
		require.NotNil(t, cmd)
		cmd()
		assert.Equal(t, "https://github.com/kalverra/pronto/pull/1/checks", openedURL)
	})

	t.Run("Other notifications link to PR URL", func(t *testing.T) {
		t.Parallel()
		var openedURL string
		m := tui.New(q, tui.WithOpener(func(rawURL string) error {
			openedURL = rawURL
			return nil
		}))
		updated, _ := m.Update(tui.NotificationMsg{
			Notifications: []notify.Notification{
				{
					Trigger:  notify.TriggerReviewReceived,
					PRNumber: 1,
					Title:    "Review on #1",
					URL:      "https://github.com/kalverra/pronto/pull/1",
				},
			},
		})
		mod := updated.(tui.Model)
		// Focus notification
		modF, _ := mod.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		mF := modF.(tui.Model)
		require.True(t, mF.IsNotificationFocused())

		// Press Enter
		_, cmd := mF.Update(tea.KeyMsg{Type: tea.KeyEnter})
		require.NotNil(t, cmd)
		cmd()
		assert.Equal(t, "https://github.com/kalverra/pronto/pull/1", openedURL)
	})
}

func TestModel_Notification_DismissWithEsc(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Authored: []model.PullRequest{
			{Number: 1, Title: "Feature", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q)
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerCIFailed,
				PRNumber: 1,
				Title:    "CI Failed (#1)",
				URL:      "https://github.com/kalverra/pronto/pull/1",
			},
		},
	})
	mod := updated.(tui.Model)
	modF, _ := mod.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mF := modF.(tui.Model)
	require.True(t, mF.IsNotificationFocused())

	// Press Esc to dismiss
	mEsc, _ := mF.Update(tea.KeyMsg{Type: tea.KeyEsc})
	modEsc := mEsc.(tui.Model)
	assert.False(t, modEsc.IsNotificationFocused())
}

func TestModel_WithNotificationConfig(t *testing.T) {
	t.Parallel()

	cfg := config.NotificationConfig{
		Popups: false,
		Sound:  true,
		Sounds: map[string]string{
			"ci_passed": "/sounds/passed.wav",
		},
		Images: map[string]string{
			"ci_passed": "/icons/passed.png",
		},
	}

	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Feature",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:             2,
					Running:           1,
					ReqTotal:          2,
					ReqRunning:        1,
					HasRequiredChecks: true,
				},
			},
		},
	}

	m := tui.New(q, tui.WithNotificationConfig(cfg))

	refreshedQ := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Feature",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:             2,
					Done:              2,
					ReqTotal:          2,
					ReqDone:           2,
					HasRequiredChecks: true,
				},
			},
		},
	}

	updated, cmd := m.Update(tui.QueueLoadedMsg{Queue: refreshedQ})
	_ = updated
	require.NotNil(t, cmd)
	msg := cmd()
	require.NotNil(t, msg)
	notifMsg, ok := msg.(tui.NotificationMsg)
	require.True(t, ok)
	require.Len(t, notifMsg.Notifications, 1)
	assert.Equal(t, "/sounds/passed.wav", notifMsg.Notifications[0].SoundPath)
	assert.Equal(t, "/icons/passed.png", notifMsg.Notifications[0].ImagePath)
}

type fakeDaemonSource struct {
	q model.Queue
}

func (s *fakeDaemonSource) Fetch(context.Context) (model.Queue, error) {
	return s.q, nil
}

func (s *fakeDaemonSource) IsDaemon() bool {
	return true
}

func TestModel_WithoutNotifications(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Feature",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:             2,
					Running:           1,
					ReqTotal:          2,
					ReqRunning:        1,
					HasRequiredChecks: true,
				},
			},
		},
	}

	m := tui.New(q, tui.WithoutNotifications())

	refreshedQ := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Feature",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:             2,
					Done:              2,
					ReqTotal:          2,
					ReqDone:           2,
					HasRequiredChecks: true,
				},
			},
		},
	}

	updated, cmd := m.Update(tui.QueueLoadedMsg{Queue: refreshedQ})
	_ = updated
	assert.Nil(t, cmd, "WithoutNotifications must not run notification command on refresh")
}

func TestStartupModel_DaemonSource_SkipsChangeDetection(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Feature",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:             2,
					Running:           1,
					ReqTotal:          2,
					ReqRunning:        1,
					HasRequiredChecks: true,
				},
			},
		},
	}

	daemonSrc := &fakeDaemonSource{q: q}
	m := tui.StartupModel(context.Background(), daemonSrc, nil)

	refreshedQ := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Feature",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:             2,
					Done:              2,
					ReqTotal:          2,
					ReqDone:           2,
					HasRequiredChecks: true,
				},
			},
		},
	}

	// Update with initial load
	updated, _ := m.Update(tui.QueueLoadedMsg{Queue: q})
	m = updated.(tui.Model)

	// Update with refresh
	_, cmd := m.Update(tui.QueueLoadedMsg{Queue: refreshedQ})
	assert.Nil(t, cmd, "daemon source must skip local change detection; the daemon detects instead")
}

func TestModel_Notification_FeedCapAndOrdering(t *testing.T) {
	t.Parallel()

	q := model.Queue{}
	m := tui.New(q)

	assert.Empty(t, m.Notifications())
	assert.Nil(t, m.LastNotification())
	assert.Equal(t, 0, m.NotificationCursor())

	// Push 6 notifications (1..6)
	for i := 1; i <= 6; i++ {
		updated, _ := m.Update(tui.NotificationMsg{
			Notifications: []notify.Notification{
				{
					Trigger:  notify.TriggerCIFailed,
					PRNumber: i,
					Repo:     "kalverra/pronto",
					Title:    "CI Failed",
				},
			},
		})
		m = updated.(tui.Model)
	}

	// Max 5 items, newest at index 0 (PR 6, 5, 4, 3, 2)
	notes := m.Notifications()
	require.Len(t, notes, 5)
	assert.Equal(t, 6, notes[0].PRNumber)
	assert.Equal(t, 5, notes[1].PRNumber)
	assert.Equal(t, 4, notes[2].PRNumber)
	assert.Equal(t, 3, notes[3].PRNumber)
	assert.Equal(t, 2, notes[4].PRNumber)
	assert.Equal(t, &notes[0], m.LastNotification())

	// Dedup/supersede tests:
	// 1. CI passed replaces or drops existing CI failed notification for that PR
	updatedPass, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerCIPassed,
				PRNumber: 6,
				Repo:     "kalverra/pronto",
				Title:    "CI Passed",
			},
		},
	})
	m = updatedPass.(tui.Model)
	require.Len(t, m.Notifications(), 5)
	assert.Equal(t, 6, m.Notifications()[0].PRNumber)
	assert.Equal(t, notify.TriggerCIPassed, m.Notifications()[0].Trigger)
	pr6Count := 0
	for _, n := range m.Notifications() {
		if n.PRNumber == 6 {
			pr6Count++
		}
	}
	assert.Equal(t, 1, pr6Count)

	// 2. CI failed replaces existing CI passed notification for that PR
	updatedFail, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerCIFailed,
				PRNumber: 6,
				Repo:     "kalverra/pronto",
				Title:    "CI Failed",
			},
		},
	})
	m = updatedFail.(tui.Model)
	assert.Equal(t, 6, m.Notifications()[0].PRNumber)
	assert.Equal(t, notify.TriggerCIFailed, m.Notifications()[0].Trigger)

	// 3. Merge conflict replaces previous conflict notification for that PR
	updatedConflict1, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerConflict,
				PRNumber: 7,
				Repo:     "kalverra/pronto",
				Title:    "Conflict 1",
			},
		},
	})
	m = updatedConflict1.(tui.Model)
	assert.Equal(t, 7, m.Notifications()[0].PRNumber)
	assert.Equal(t, "Conflict 1", m.Notifications()[0].Title)

	updatedConflict2, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerConflict,
				PRNumber: 7,
				Repo:     "kalverra/pronto",
				Title:    "Conflict 2",
			},
		},
	})
	m = updatedConflict2.(tui.Model)
	assert.Equal(t, 7, m.Notifications()[0].PRNumber)
	assert.Equal(t, "Conflict 2", m.Notifications()[0].Title)
	pr7Count := 0
	for _, n := range m.Notifications() {
		if n.PRNumber == 7 {
			pr7Count++
		}
	}
	assert.Equal(t, 1, pr7Count)

	// 4. PR merged replaces earlier pending notifications for that PR
	updatedMerged, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerPRMerged,
				PRNumber: 7,
				Repo:     "kalverra/pronto",
				Title:    "PR Merged",
			},
		},
	})
	m = updatedMerged.(tui.Model)
	assert.Equal(t, 7, m.Notifications()[0].PRNumber)
	assert.Equal(t, notify.TriggerPRMerged, m.Notifications()[0].Trigger)
	for _, n := range m.Notifications() {
		if n.PRNumber == 7 {
			assert.Equal(t, notify.TriggerPRMerged, n.Trigger)
		}
	}
}

func TestModel_Notification_TTLExpiration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	q := model.Queue{}
	m := tui.New(q, tui.WithNow(now))

	// Notification 1: 35 minutes ago (should expire)
	// Notification 2: 10 minutes ago (should stay)
	// Notification 3: 0 timestamp (defaulted to now, should stay)
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:     notify.TriggerCIFailed,
				PRNumber:    1,
				Repo:        "kalverra/pronto",
				SubmittedAt: now.Add(-35 * time.Minute),
			},
			{
				Trigger:     notify.TriggerCIFailed,
				PRNumber:    2,
				Repo:        "kalverra/pronto",
				SubmittedAt: now.Add(-10 * time.Minute),
			},
			{
				Trigger:  notify.TriggerConflict,
				PRNumber: 3,
				Repo:     "kalverra/pronto",
			},
		},
	})
	m = updated.(tui.Model)

	// Refresh queue to trigger TTL pruning
	updated2, _ := m.Update(tui.QueueLoadedMsg{Queue: q})
	m2 := updated2.(tui.Model)

	notes := m2.Notifications()
	require.Len(t, notes, 2)
	assert.Equal(t, 3, notes[0].PRNumber)
	assert.Equal(t, 2, notes[1].PRNumber)
	// PR 1 must have been evicted by 30-min TTL
	for _, n := range notes {
		assert.NotEqual(t, 1, n.PRNumber)
	}
}

func TestModel_Notification_QueueStateSync(t *testing.T) {
	t.Parallel()

	initialQ := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "PR 1",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:  2,
					Failed: 1,
					Done:   2,
				},
			},
			{
				Number:            2,
				Title:             "PR 2",
				RepoNameWithOwner: "kalverra/pronto",
				Mergeable:         "CONFLICTING",
				MergeStatus:       model.ComputeMergeStatus("CONFLICTING", "DIRTY", false),
			},
			{
				Number:            3,
				Title:             "PR 3",
				RepoNameWithOwner: "kalverra/pronto",
			},
		},
	}

	m := tui.New(initialQ)

	// Seed notifications: PR 1 CI failed, PR 2 conflict, PR 3 CI failed
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerCIFailed,
				PRNumber: 1,
				Repo:     "kalverra/pronto",
			},
			{
				Trigger:  notify.TriggerConflict,
				PRNumber: 2,
				Repo:     "kalverra/pronto",
			},
			{
				Trigger:  notify.TriggerCIFailed,
				PRNumber: 3,
				Repo:     "kalverra/pronto",
			},
		},
	})
	m = updated.(tui.Model)
	require.Len(t, m.Notifications(), 3)

	// Refreshed queue:
	// - PR 1 has passing CI (clears CI fail)
	// - PR 2 conflict resolved (clears conflict)
	// - PR 3 is merged (absent from open PR queue, clears pre-merge alerts)
	refreshedQ := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "PR 1",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total: 2,
					Done:  2,
				},
			},
			{
				Number:            2,
				Title:             "PR 2",
				RepoNameWithOwner: "kalverra/pronto",
				Mergeable:         "MERGEABLE",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
			},
		},
	}

	updated2, _ := m.Update(tui.QueueLoadedMsg{Queue: refreshedQ})
	m2 := updated2.(tui.Model)

	// All 3 pre-merge/failing alerts should have cleared!
	assert.Empty(t, m2.Notifications(), "CI pass, conflict resolved, and merged PR must clear pre-merge alerts")
}
