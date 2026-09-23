package tui_test

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/tui"
)

func TestModel_View_RefinedStatusGlyphs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:            101,
				Title:             "Clean PR",
				RepoNameWithOwner: "kalverra/pronto",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				UpdatedAt:         now.Add(-1 * time.Hour),
			},
			{
				Number:            102,
				Title:             "Conflict PR",
				RepoNameWithOwner: "kalverra/pronto",
				Mergeable:         "CONFLICTING",
				MergeStatus:       model.ComputeMergeStatus("CONFLICTING", "DIRTY", false),
				UpdatedAt:         now.Add(-2 * time.Hour),
			},
			{
				Number:            103,
				Title:             "Failing CI PR",
				RepoNameWithOwner: "kalverra/pronto",
				Checks: model.ChecksSummary{
					Total:  5,
					Failed: 1,
				},
				UpdatedAt: now.Add(-3 * time.Hour),
			},
			{
				Number:            104,
				Title:             "Needs Review PR",
				RepoNameWithOwner: "kalverra/pronto",
				ReviewDecision:    "REVIEW_REQUIRED",
				UpdatedAt:         now.Add(-4 * time.Hour),
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 40),
		tui.WithActiveTab(tui.TabInbox),
	)
	view := m.View()

	// Subtle glyphs + text for refined status badges
	assert.Contains(t, view, "● CLEAN")
	assert.Contains(t, view, "✖ CONFLICT")
	assert.Contains(t, view, "✖ FAILING CI")
	assert.Contains(t, view, "● NEEDS REVIEW")
}

func TestModel_View_NotificationNoRedundantRepoPrefix(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{}

	m := tui.New(q, tui.WithNow(now), tui.WithDimensions(140, 40))
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:     notify.TriggerCIFailed,
				Repo:        "org/repo",
				PRNumber:    42,
				Message:     `CI failed for "Feature" (org/repo#42)`,
				SubmittedAt: now.Add(-2 * time.Minute),
			},
		},
	})

	view := updated.(tui.Model).View()

	assert.Contains(t, view, "org/repo#42")
	assert.Contains(t, view, `"Feature"`)
	assert.NotContains(t, view, "CI failed for")
}

func TestModel_View_TabBarSeparatorsAndHeaderDivider(t *testing.T) {
	t.Parallel()

	q := model.Queue{}
	m := tui.New(q, tui.WithDimensions(120, 40))
	view := m.View()

	assert.Contains(t, view, "1: Focus (0)")
	assert.Contains(t, view, "│")
	assert.Contains(t, view, "2: Mine (0)")
	assert.Contains(t, view, "3: Inbox (0)")
}

func TestModel_View_NotificationStatusBadgeAndNoRedundantInfo(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{}

	m := tui.New(q, tui.WithNow(now), tui.WithDimensions(140, 40))
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:     notify.TriggerCIPassed,
				Repo:        "smartcontractkit/chainlink",
				PRNumber:    23505,
				PRTitle:     "Lint Roller 13: integration-tests",
				Message:     `Checks passed for "Lint Roller 13: integration-tests" (smartcontractkit/chainlink#23505)`,
				SubmittedAt: now.Add(-4 * time.Minute),
			},
			{
				Trigger:     notify.TriggerCIFailed,
				Repo:        "smartcontractkit/infra-griddle-app",
				PRNumber:    732,
				PRTitle:     "chore: e2e test coverage",
				Message:     `CI failed for "chore: e2e test coverage" (smartcontractkit/infra-griddle-app#732)`,
				SubmittedAt: now.Add(-14 * time.Minute),
			},
			{
				Trigger:     notify.TriggerReviewReceived,
				ReviewState: "APPROVED",
				Repo:        "smartcontractkit/chainlink",
				PRNumber:    23686,
				PRTitle:     "Convert bash to Go",
				Author:      "robsondebraga",
				Message:     `@robsondebraga approved: "Convert bash to Go" (smartcontractkit/chainlink#23686)`,
				SubmittedAt: now.Add(-4 * time.Minute),
			},
		},
	})

	view := updated.(tui.Model).View()

	// 1. Status badges match design
	assert.Contains(t, view, "✓ CI PASS")
	assert.Contains(t, view, "✖ CI FAIL")
	assert.Contains(t, view, "✓ APPROVED")

	// 2. PR name and details shown cleanly
	assert.Contains(t, view, "smartcontractkit/chainlink#23505")
	assert.Contains(t, view, "Lint Roller 13: integration-tests")
	assert.Contains(t, view, "smartcontractkit/infra-griddle-app#732")
	assert.Contains(t, view, "chore: e2e test coverage")
	assert.Contains(t, view, "smartcontractkit/chainlink#23686")
	assert.Contains(t, view, "robsondebraga")

	// 3. Redundant text removed
	assert.NotContains(t, view, "Checks passed for")
	assert.NotContains(t, view, "CI failed for")
	assert.NotContains(t, view, `(smartcontractkit/chainlink#23505)`)
}

func TestModel_View_ZeroNotificationsHeaderBadgeAndZeroLines(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 1, Title: "PR 1", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q, tui.WithDimensions(120, 30))
	view := m.View()

	// Header contains muted 🔔 0 badge
	assert.Contains(t, view, "🔔 0")
	assert.Contains(t, view, "│")
	assert.Contains(t, view, "[?] Help  [q] Quit")

	// Body does not waste lines on 0 notifications
	assert.NotContains(t, view, "NOTIFICATIONS")
	assert.NotContains(t, view, "No recent notifications")
}

func TestModel_View_ActiveNotificationsAlertBanner(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 1, Title: "PR 1", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q, tui.WithNow(now), tui.WithDimensions(120, 30))
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{
				Trigger:     notify.TriggerReviewReceived,
				ReviewState: "APPROVED",
				Repo:        "chainlink",
				PRNumber:    23686,
				Author:      "robsondebraga",
				SubmittedAt: now.Add(-4 * time.Minute),
			},
			{
				Trigger:     notify.TriggerCIFailed,
				Repo:        "infra-griddle-app",
				PRNumber:    732,
				PRTitle:     "chore: e2e test coverage",
				SubmittedAt: now.Add(-14 * time.Minute),
			},
		},
	})

	view := updated.(tui.Model).View()

	// Header shows active alert count
	assert.Contains(t, view, "🔔 2 new alerts")

	// Renders clean rounded box container with title and toggle hint
	assert.Contains(t, view, "╭─")
	assert.Contains(t, view, "🔔 NOTIFICATIONS (2)")
	assert.Contains(t, view, "[n to hide]")
	assert.Contains(t, view, "─╮")
	assert.Contains(t, view, "╰─")
	assert.Contains(t, view, "─╯")
	assert.Contains(t, view, "│")
	assert.Contains(t, view, "chainlink#23686")
	assert.Contains(t, view, "infra-griddle-app#732")
}

func TestModel_View_NotificationCollapseToggle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 1, Title: "PR 1", RepoNameWithOwner: "kalverra/pronto"},
		},
	}
	m := tui.New(q, tui.WithNow(now), tui.WithDimensions(120, 30))
	updated, _ := m.Update(tui.NotificationMsg{
		Notifications: []notify.Notification{
			{Trigger: notify.TriggerCIFailed, PRNumber: 1, Repo: "repo", Title: "Fail"},
		},
	})
	mActive := updated.(tui.Model)
	assert.False(t, mActive.HideNotifs())
	assert.Contains(t, mActive.View(), "🔔 NOTIFICATIONS (1)")

	// Press 'n' to focus
	mFocused, _ := mActive.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	assert.True(t, mFocused.(tui.Model).IsNotificationFocused())

	// Press 'n' while focused collapses into single-line chip
	mCollapsed, _ := mFocused.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	modCollapsed := mCollapsed.(tui.Model)
	assert.True(t, modCollapsed.HideNotifs())
	assert.False(t, modCollapsed.IsNotificationFocused())
	collapsedView := modCollapsed.View()
	assert.Contains(t, collapsedView, "▸ 🔔 1 unread [n]")
	assert.NotContains(t, collapsedView, "╭─")

	// Press 'n' again uncollapses
	mExpanded, _ := modCollapsed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	modExpanded := mExpanded.(tui.Model)
	assert.False(t, modExpanded.HideNotifs())
	assert.Contains(t, modExpanded.View(), "🔔 NOTIFICATIONS (1)")
}

func TestModel_View_CategoryHeadersLeftAccentAndPills(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            101,
				Title:             "Action Required PR",
				RepoNameWithOwner: "kalverra/pronto",
				ReviewDecision:    "CHANGES_REQUESTED",
				UpdatedAt:         now.Add(-1 * time.Hour),
			},
			{
				Number:            102,
				Title:             "In Review PR",
				RepoNameWithOwner: "kalverra/pronto",
				ReviewDecision:    "REVIEW_REQUIRED",
				UpdatedAt:         now.Add(-2 * time.Hour),
			},
			{
				Number:            103,
				Title:             "Draft PR",
				RepoNameWithOwner: "kalverra/pronto",
				IsDraft:           true,
				UpdatedAt:         now.Add(-3 * time.Hour),
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(120, 30),
		tui.WithActiveTab(tui.TabMine),
	)
	view := m.View()

	// Redesigned left-accent bars with uppercase category titles and pill count badges
	assert.Contains(t, view, "▌ ACTION REQUIRED")
	assert.Contains(t, view, "▌ IN REVIEW")
	assert.Contains(t, view, "▌ DRAFTS")

	// Must not use old raw ASCII dashes for category headers
	assert.NotContains(t, view, "── ACTION REQUIRED (")
	assert.NotContains(t, view, "── IN REVIEW (")
	assert.NotContains(t, view, "── DRAFTS (")
}
