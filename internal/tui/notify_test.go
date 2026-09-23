package tui_test

import (
	"context"
	"strings"
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
	assert.Contains(t, mod2.View(), "CI PASS")

	// 'n' focuses the notification banner
	navUpdated, _ := mod2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	navMod := navUpdated.(tui.Model)
	assert.True(t, navMod.IsNotificationFocused())

	// Esc unfocuses the notification banner without removing it
	escUpdated, _ := navMod.Update(tea.KeyMsg{Type: tea.KeyEsc})
	escMod := escUpdated.(tui.Model)
	assert.False(t, escMod.IsNotificationFocused())
	assert.NotNil(t, escMod.LastNotification())

	// Re-focus and dismiss with 'x'
	refocused, _ := escMod.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	xUpdated, _ := refocused.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	xMod := xUpdated.(tui.Model)
	assert.Nil(t, xMod.LastNotification())
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
		assert.Contains(t, view, "CI FAIL")
		assert.Contains(t, view, "Feature")
		assert.Contains(t, view, "kalverra/pronto#1")
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
		assert.Contains(t, view, "CONFLICT")
		assert.Contains(t, view, "Feature")
		assert.Contains(t, view, "kalverra/pronto#1")
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
		assert.Contains(t, view, "CHANGES REQ")
		assert.Contains(t, view, "alice")
		assert.Contains(t, view, "#1")
		assert.NotContains(t, view, "63;185;80")
	})
}

func TestModel_Notification_FocusAndEscape(t *testing.T) {
	t.Parallel()

	// 1. When notifications list is empty, 'n' does nothing
	q := model.Queue{
		Authored: []model.PullRequest{
			{Number: 1, Title: "Feature", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q)
	assert.False(t, m.IsNotificationFocused())
	mNoOp, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	assert.False(t, mNoOp.(tui.Model).IsNotificationFocused())

	// 2. When notifications exist, 'n' toggles focus
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
	mWithNote := updated.(tui.Model)
	assert.False(t, mWithNote.IsNotificationFocused())

	// Press 'n' to focus
	mFocusedRaw, _ := mWithNote.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mFocused := mFocusedRaw.(tui.Model)
	assert.True(t, mFocused.IsNotificationFocused())

	// Press 'n' again to unfocus
	mUnfocusedRaw, _ := mFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mUnfocused := mUnfocusedRaw.(tui.Model)
	assert.False(t, mUnfocused.IsNotificationFocused())
	assert.Len(t, mUnfocused.Notifications(), 1, "unfocusing with 'n' must not clear notifications")

	// Press 'n' to re-focus, then 'esc' to unfocus without clearing notifications
	mRefocusedRaw, _ := mUnfocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mRefocused := mRefocusedRaw.(tui.Model)
	assert.True(t, mRefocused.IsNotificationFocused())

	mEscRaw, _ := mRefocused.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mEsc := mEscRaw.(tui.Model)
	assert.False(t, mEsc.IsNotificationFocused(), "esc must exit notification focus")
	assert.Len(t, mEsc.Notifications(), 1, "esc must not clear notification list")

	// Press 'esc' when not focused does not clear notifications
	mEscAgainRaw, _ := mEsc.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mEscAgain := mEscAgainRaw.(tui.Model)
	assert.False(t, mEscAgain.IsNotificationFocused())
	assert.Len(t, mEscAgain.Notifications(), 1)
}

func TestModel_Notification_CursorNavigation(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 1, Title: "PR 1", RepoNameWithOwner: "kalverra/pronto"},
			{Number: 2, Title: "PR 2", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q, tui.WithActiveTab(tui.TabInbox))
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{PRNumber: 1, Title: "Note 1"},
			{PRNumber: 2, Title: "Note 2"},
			{PRNumber: 3, Title: "Note 3"},
		},
	})
	m3 := updated.(tui.Model)
	require.Len(t, m3.Notifications(), 3)
	assert.Equal(t, 0, m3.NotificationCursor())

	// Focus notifications
	mFocRaw, _ := m3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mFoc := mFocRaw.(tui.Model)
	assert.True(t, mFoc.IsNotificationFocused())
	assert.Equal(t, 0, mFoc.NotificationCursor())

	// 'j' moves cursor down
	mjRaw, _ := mFoc.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mj := mjRaw.(tui.Model)
	assert.Equal(t, 1, mj.NotificationCursor())
	assert.Equal(t, 0, mj.Cursor(), "table cursor must remain unchanged")

	// 'down' moves cursor down to last item
	mDownRaw, _ := mj.Update(tea.KeyMsg{Type: tea.KeyDown})
	mDown := mDownRaw.(tui.Model)
	assert.Equal(t, 2, mDown.NotificationCursor())

	// 'j' at bottom clamps to len-1 (2)
	mClampBottomRaw, _ := mDown.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mClampBottom := mClampBottomRaw.(tui.Model)
	assert.Equal(t, 2, mClampBottom.NotificationCursor())

	// 'k' moves cursor up
	mkRaw, _ := mClampBottom.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	mk := mkRaw.(tui.Model)
	assert.Equal(t, 1, mk.NotificationCursor())

	// 'up' moves cursor up to first item
	mUpRaw, _ := mk.Update(tea.KeyMsg{Type: tea.KeyUp})
	mUp := mUpRaw.(tui.Model)
	assert.Equal(t, 0, mUp.NotificationCursor())

	// 'k' at top clamps to 0
	mClampTopRaw, _ := mUp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	mClampTop := mClampTopRaw.(tui.Model)
	assert.Equal(t, 0, mClampTop.NotificationCursor())

	// 'G' / 'end' moves to bottom
	mGRaw, _ := mClampTop.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	mG := mGRaw.(tui.Model)
	assert.Equal(t, 2, mG.NotificationCursor())

	// 'g' / 'home' moves to top
	mgRaw, _ := mG.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	mg := mgRaw.(tui.Model)
	assert.Equal(t, 0, mg.NotificationCursor())

	// Unfocused table cursor navigation: 'j' on table does not jump into notifications
	mUnfocusedRaw, _ := mg.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mUnfoc := mUnfocusedRaw.(tui.Model)
	assert.False(t, mUnfoc.IsNotificationFocused())
	assert.Equal(t, 0, mUnfoc.Cursor())

	mTableNavRaw, _ := mUnfoc.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mTableNav := mTableNavRaw.(tui.Model)
	assert.Equal(t, 1, mTableNav.Cursor())
	assert.False(t, mTableNav.IsNotificationFocused())

	// At bottom of table, 'j' stays in table and does not focus notifications
	mTableBottomRaw, _ := mTableNav.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mTableBottom := mTableBottomRaw.(tui.Model)
	assert.Equal(t, 1, mTableBottom.Cursor())
	assert.False(t, mTableBottom.IsNotificationFocused())
}

func TestModel_Notification_OpenSelectedURL(t *testing.T) {
	t.Parallel()

	var openedURL string
	q := model.Queue{
		Authored: []model.PullRequest{
			{Number: 1, Title: "Feature", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q, tui.WithOpener(func(rawURL string) error {
		openedURL = rawURL
		return nil
	}))

	// PR 1 added first, then PR 2 added. Feed is newest-first: index 0 is PR 2, index 1 is PR 1.
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:  notify.TriggerCIFailed,
				PRNumber: 1,
				Title:    "CI Failed (#1)",
				URL:      "https://github.com/kalverra/pronto/pull/1",
			},
			{
				Trigger:  notify.TriggerConflict,
				PRNumber: 2,
				Title:    "Merge Conflict (#2)",
				URL:      "https://github.com/kalverra/pronto/pull/2",
			},
		},
	})
	mod := updated.(tui.Model)
	require.Len(t, mod.Notifications(), 2)
	assert.Equal(t, 2, mod.Notifications()[0].PRNumber)
	assert.Equal(t, 1, mod.Notifications()[1].PRNumber)

	// Focus notifications
	modF, _ := mod.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mF := modF.(tui.Model)
	require.True(t, mF.IsNotificationFocused())

	// Move to index 1 (PR 1 CIFailed)
	mNext, _ := mF.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mAt1 := mNext.(tui.Model)
	assert.Equal(t, 1, mAt1.NotificationCursor())

	// Press Enter -> opens PR 1 checks URL, not index 0 (PR 2)
	_, cmd := mAt1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	cmd()
	assert.Equal(t, "https://github.com/kalverra/pronto/pull/1/checks", openedURL)

	// Test 'o' on index 0 (PR 2 conflict)
	mAt0, _ := mAt1.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	assert.Equal(t, 0, mAt0.(tui.Model).NotificationCursor())
	openedURL = ""
	_, cmdOpen := mAt0.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	require.NotNil(t, cmdOpen)
	cmdOpen()
	assert.Equal(t, "https://github.com/kalverra/pronto/pull/2", openedURL)
}

func TestModel_Notification_DismissSelected(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	stalePR := model.PullRequest{
		Number:            10,
		Title:             "Old Feature",
		RepoNameWithOwner: "kalverra/pronto",
		UpdatedAt:         now.Add(-40 * 24 * time.Hour),
	}
	q := model.Queue{
		Authored: []model.PullRequest{stalePR},
	}
	m := tui.New(q, tui.WithNow(now))

	// Notifications added newest-first: index 0 is Note 3, index 1 is Note 2, index 2 is Note 1.
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{PRNumber: 1, Title: "Note 1"},
			{PRNumber: 2, Title: "Note 2"},
			{PRNumber: 3, Title: "Note 3"},
		},
	})
	m3 := updated.(tui.Model)
	require.Len(t, m3.Notifications(), 3)
	assert.Equal(t, 3, m3.Notifications()[0].PRNumber)
	assert.Equal(t, 2, m3.Notifications()[1].PRNumber)
	assert.Equal(t, 1, m3.Notifications()[2].PRNumber)

	// Focus notifications
	mFocRaw, _ := m3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mFoc := mFocRaw.(tui.Model)
	require.True(t, mFoc.IsNotificationFocused())

	// Move cursor to index 1 (Note 2)
	mNavRaw, _ := mFoc.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mAt1 := mNavRaw.(tui.Model)
	assert.Equal(t, 1, mAt1.NotificationCursor())

	// Press 'x' to dismiss Note 2
	mDismiss1Raw, _ := mAt1.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	mDismiss1 := mDismiss1Raw.(tui.Model)
	require.Len(t, mDismiss1.Notifications(), 2)
	assert.Equal(t, 3, mDismiss1.Notifications()[0].PRNumber)
	assert.Equal(t, 1, mDismiss1.Notifications()[1].PRNumber)
	assert.Equal(t, 1, mDismiss1.NotificationCursor(), "cursor should stay at 1 pointing to Note 1")
	assert.True(t, mDismiss1.IsNotificationFocused())

	// Dismiss Note 1 at index 1 -> cursor clamps to 0 (Note 3)
	mDismiss2Raw, _ := mDismiss1.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	mDismiss2 := mDismiss2Raw.(tui.Model)
	require.Len(t, mDismiss2.Notifications(), 1)
	assert.Equal(t, 3, mDismiss2.Notifications()[0].PRNumber)
	assert.Equal(t, 0, mDismiss2.NotificationCursor(), "cursor should clamp to 0")
	assert.True(t, mDismiss2.IsNotificationFocused())

	// Dismiss last remaining notification -> list empty, unfocuses
	mDismiss3Raw, _ := mDismiss2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	mDismiss3 := mDismiss3Raw.(tui.Model)
	assert.Empty(t, mDismiss3.Notifications())
	assert.False(t, mDismiss3.IsNotificationFocused())

	// In PR table (not notification focused), 'x' triggers PR close confirmation
	// Switch to Mine tab so Authored PR 10 is selected
	mMineRaw, _ := mDismiss3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	mMine := mMineRaw.(tui.Model)
	mClosePRRaw, _ := mMine.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	mClosePR := mClosePRRaw.(tui.Model)
	assert.NotNil(t, mClosePR.ConfirmingClosePR(), "x when unfocused must trigger stale PR close confirmation")
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

func TestModel_Notification_ViewRenderBetweenTabsAndTable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 42, Title: "Inbox PR", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q, tui.WithNow(now), tui.WithActiveTab(tui.TabInbox))

	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:     notify.TriggerCIFailed,
				PRNumber:    1,
				Repo:        "kalverra/pronto",
				Message:     "CI failed for Feature",
				SubmittedAt: now.Add(-2 * time.Minute),
			},
			{
				Trigger:     notify.TriggerCIPassed,
				PRNumber:    2,
				Repo:        "kalverra/pronto",
				Message:     "Checks passed",
				SubmittedAt: now.Add(-10 * time.Minute),
			},
			{
				Trigger:     notify.TriggerConflict,
				PRNumber:    3,
				Repo:        "kalverra/pronto",
				Message:     "Merge conflict in Bugfix",
				SubmittedAt: now.Add(-45 * time.Second),
			},
			{
				Trigger:     notify.TriggerReviewReceived,
				ReviewState: "APPROVED",
				PRNumber:    4,
				Repo:        "kalverra/pronto",
				Message:     "@alice approved",
				SubmittedAt: now.Add(-5 * time.Minute),
			},
			{
				Trigger:     notify.TriggerPRMerged,
				PRNumber:    5,
				Repo:        "kalverra/pronto",
				Message:     "Merged: Feature",
				SubmittedAt: now.Add(-1 * time.Hour),
			},
		},
	})
	mod := updated.(tui.Model)
	view := mod.View()

	// Assert order: Tabs -> Notifications -> PR Table
	idxTabs := strings.Index(view, "Inbox")
	idxNotifs := strings.Index(view, "NOTIFICATIONS (5)")
	idxTable := strings.Index(view, "TITLE")
	require.NotEqual(t, -1, idxTabs, "tabs must appear in view")
	require.NotEqual(t, -1, idxNotifs, "notifications header must appear in view")
	require.NotEqual(t, -1, idxTable, "table header must appear in view")
	assert.Less(t, idxTabs, idxNotifs, "notifications must render after tabs")
	assert.Less(t, idxNotifs, idxTable, "notifications must render before table")

	// Assert badges
	assert.Contains(t, view, "CI FAIL")
	assert.Contains(t, view, "CI PASS")
	assert.Contains(t, view, "CONFLICT")
	assert.Contains(t, view, "APPROVED")
	assert.Contains(t, view, "MERGED")

	// Assert PR reference, messages, and relative age timestamps
	assert.Contains(t, view, "kalverra/pronto#1")
	assert.Contains(t, view, "Feature")
	assert.Contains(t, view, "2m ago")

	assert.Contains(t, view, "kalverra/pronto#2")
	assert.Contains(t, view, "10m ago")

	assert.Contains(t, view, "kalverra/pronto#3")
	assert.Contains(t, view, "Bugfix")
	assert.Contains(t, view, "45s ago")

	assert.Contains(t, view, "kalverra/pronto#4")
	assert.Contains(t, view, "alice")
	assert.Contains(t, view, "5m ago")

	assert.Contains(t, view, "kalverra/pronto#5")
	assert.Contains(t, view, "Feature")
	assert.Contains(t, view, "1h ago")
}

func TestModel_Notification_EmptyView(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 1, Title: "PR 1", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q)
	view := m.View()

	assert.Contains(t, view, "🔔 0")
	assert.NotContains(t, view, "NOTIFICATIONS")
	assert.NotContains(t, view, "No recent notifications")
}

func TestModel_Notification_ViewFocusIndicator(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 1, Title: "PR 1", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q, tui.WithNow(now))

	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:     notify.TriggerCIFailed,
				PRNumber:    10,
				Repo:        "kalverra/pronto",
				Message:     "Note 10",
				SubmittedAt: now,
			},
			{
				Trigger:     notify.TriggerConflict,
				PRNumber:    20,
				Repo:        "kalverra/pronto",
				Message:     "Note 20",
				SubmittedAt: now,
			},
		},
	})
	mNotifs := updated.(tui.Model)

	// When unfocused: help bar shows 'n: notifs'
	unfocusedView := mNotifs.View()
	assert.Contains(t, unfocusedView, "n: notifs")
	assert.NotContains(t, unfocusedView, "esc: back to PRs")

	// Press 'n' to focus
	mFocusedRaw, _ := mNotifs.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mFocused := mFocusedRaw.(tui.Model)
	require.True(t, mFocused.IsNotificationFocused())

	focusedView := mFocused.View()
	// Bottom help bar shows notification hints
	assert.Contains(t, focusedView, "enter/o: open • ↑/↓: select • x: dismiss • esc: back to PRs • q: quit")
	// Index 0 has cursor prefix '> '
	assert.Contains(t, focusedView, "> ")

	// Move cursor down to index 1
	mDownRaw, _ := mFocused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mDown := mDownRaw.(tui.Model)
	assert.Equal(t, 1, mDown.NotificationCursor())
	downView := mDown.View()
	assert.Contains(t, downView, "> ")
}

func TestModel_Notification_VisibleRowsAccounting(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 1, Title: "PR 1", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q)
	// Base height = 24.
	// Base chrome = 10.
	// 0 notifications: takes 0 lines. Total chrome = 10.
	// VisibleRows() = 24 - 10 = 14.
	assert.Equal(t, 14, m.VisibleRows())

	// 1 notification: notificationsHeight = 1 + 3 = 4 (top border + 1 row + bottom border + newline).
	// Total chrome = 10 + 4 = 14.
	// VisibleRows() = 24 - 14 = 10.
	updated1, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{PRNumber: 1, Title: "Note 1"},
		},
	})
	m1 := updated1.(tui.Model)
	assert.Equal(t, 10, m1.VisibleRows())

	// 5 notifications: notificationsHeight = 5 + 3 = 8.
	// Total chrome = 10 + 8 = 18.
	// VisibleRows() = 24 - 18 = 6.
	updated5, _ := m1.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{PRNumber: 2, Title: "Note 2"},
			{PRNumber: 3, Title: "Note 3"},
			{PRNumber: 4, Title: "Note 4"},
			{PRNumber: 5, Title: "Note 5"},
		},
	})
	m5 := updated5.(tui.Model)
	require.Len(t, m5.Notifications(), 5)
	assert.Equal(t, 6, m5.VisibleRows())
}
