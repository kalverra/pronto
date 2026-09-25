package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	assert.Contains(t, view, "2: Mine (0)")
	assert.Contains(t, view, "3: Priority (0)")
	assert.Contains(t, view, "4: Inbox (0)")
	// Modern tab pills without ncurses vertical border lines between tabs
	assert.NotContains(t, view, "Focus (0)  │  2: Mine")
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

	// Header contains muted 🔔 0 badge and [?] Help (no duplicate [q] Quit)
	assert.Contains(t, view, "🔔 0")
	assert.Contains(t, view, "[?] Help")
	assert.NotContains(t, view, "[?] Help  [q] Quit")
	assert.NotContains(t, view, "[q] Quit")

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

func TestModel_View_SmallWindow_TruncatesTitleAndPreservesColumns(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            23787,
				Title:             "chore: bump ci versions and update github actions configurations for repository",
				RepoName:          "chainlink",
				RepoNameWithOwner: "smartcontractkit/chainlink",
				Checks: model.ChecksSummary{
					Total:  224,
					Failed: 3,
					Done:   221,
				},
				Additions: 191,
				Deletions: 203,
				UpdatedAt: now.Add(-2 * 24 * time.Hour),
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(80, 24),
		tui.WithActiveTab(tui.TabMine),
	)
	view := m.View()

	// All column headers must be preserved even in narrow terminal windows
	assert.Contains(t, view, "TITLE")
	assert.Contains(t, view, "STATUS")
	assert.Contains(t, view, "SIZE")
	assert.Contains(t, view, "CI")
	assert.Contains(t, view, "UPDATED")
	assert.Contains(t, view, "REPO")

	// PR data should cleanly show in columns rather than dropping columns
	assert.Contains(t, view, "+191")
	assert.Contains(t, view, "2d")
	assert.Contains(t, view, "chain")
}

func TestModel_View_ConsistentOrderedPRDisplay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Authored: []model.PullRequest{
			// 1. Stack of 2 PRs (collapsed by default)
			{
				Number:            709,
				Title:             "updates to latest go-github",
				RepoName:          "infra-griddle",
				RepoNameWithOwner: "org/infra-griddle",
				UpdatedAt:         now.Add(-1 * time.Hour),
				ReviewDecision:    "CHANGES_REQUESTED",
				Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 2},
			},
			{
				Number:            710,
				Title:             "add go-github mock tests",
				RepoName:          "infra-griddle",
				RepoNameWithOwner: "org/infra-griddle",
				UpdatedAt:         now.Add(-2 * time.Hour),
				ReviewDecision:    "CHANGES_REQUESTED",
				Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 3},
			},
			// 2. Solo PR (not stacked) in same category
			{
				Number:            23787,
				Title:             "bump ci versions",
				RepoName:          "chainlink",
				RepoNameWithOwner: "org/chainlink",
				UpdatedAt:         now.Add(-2 * time.Hour),
				ReviewDecision:    "CHANGES_REQUESTED",
			},
			// 3. Single PR belonging to a stack where only 1 item is in this category
			{
				Number:            23505,
				Title:             "integration-tests",
				RepoName:          "chainlink",
				RepoNameWithOwner: "org/chainlink",
				UpdatedAt:         now.Add(-3 * time.Hour),
				ReviewDecision:    "CHANGES_REQUESTED",
				Stack:             &model.PRStack{ID: "STACK_CHAIN", Number: 23498, Size: 14, Position: 13},
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(160, 40),
		tui.WithActiveTab(tui.TabMine),
	)
	view := m.View()

	// 1. Collapsed stack: starts with #number, then stack pill [2/7], then title
	assert.Contains(t, view, "#709")
	assert.Contains(t, view, "[2/7]")
	assert.NotContains(t, view, "[STACK]")
	assert.NotContains(t, view, "[2..3]")
	assert.Contains(t, view, "updates to latest go-github")

	// 2. Solo PR: starts with #number, then title; NO parentheses at end
	assert.Contains(t, view, "#23787")
	assert.Contains(t, view, "bump ci versions")
	assert.NotContains(t, view, "bump ci versions (#23787)")

	// 3. Single PR in stack (totalInCat <= 1): starts with #number, then stack pill [13/14], then title
	assert.Contains(t, view, "#23505")
	assert.Contains(t, view, "[13/14]")
	assert.Contains(t, view, "integration-tests")
	assert.NotContains(t, view, "╶ [13/14]")
	assert.NotContains(t, view, "integration-tests (#23505)")
}

func TestModel_View_GutterStrictAlignmentAndBlockerSummary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Authored: []model.PullRequest{
			// Stack of 2 PRs, one failing CI -> should state blocker ✖ FAILING CI
			{
				Number:            732,
				Title:             "e2e test coverage",
				RepoName:          "infra-griddle",
				RepoNameWithOwner: "org/infra-griddle",
				UpdatedAt:         now.Add(-1 * time.Hour),
				Stack:             &model.PRStack{ID: "STACK_1", Number: 732, Size: 2, Position: 1},
			},
			{
				Number:            733,
				Title:             "fix e2e flaker",
				RepoName:          "infra-griddle",
				RepoNameWithOwner: "org/infra-griddle",
				Checks:            model.ChecksSummary{Total: 5, Failed: 1},
				UpdatedAt:         now.Add(-1 * time.Hour),
				Stack:             &model.PRStack{ID: "STACK_1", Number: 732, Size: 2, Position: 2},
			},
			// Solo PR with failing CI
			{
				Number:            23787,
				Title:             "bump ci versions",
				RepoName:          "chainlink",
				RepoNameWithOwner: "org/chainlink",
				Checks:            model.ChecksSummary{Total: 10, Failed: 2},
				UpdatedAt:         now.Add(-2 * time.Hour),
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabMine),
	)
	view := m.View()

	// 1. Blocker clarity: Solo PR with failing CI states blocker
	assert.Contains(t, view, "✖ FAILING CI")

	// 2. Strict gutter alignment: find lines for #732 and #23787
	lines := strings.Split(view, "\n")
	var line732, line23787 string
	for _, l := range lines {
		if strings.Contains(l, "#732") {
			line732 = l
		}
		if strings.Contains(l, "#23787") {
			line23787 = l
		}
	}
	assert.NotEmpty(t, line732)
	assert.NotEmpty(t, line23787)

	r732 := []rune(ansi.Strip(line732))
	r23787 := []rune(ansi.Strip(line23787))
	idx732 := -1
	for i := 0; i <= len(r732)-len([]rune("#732")); i++ {
		if string(r732[i:i+len([]rune("#732"))]) == "#732" {
			idx732 = i
			break
		}
	}
	idx23787 := -1
	for i := 0; i <= len(r23787)-len([]rune("#23787")); i++ {
		if string(r23787[i:i+len([]rune("#23787"))]) == "#23787" {
			idx23787 = i
			break
		}
	}
	assert.Equal(t, idx732, idx23787, "PR numbers must start at the exact same X-coordinate")

	// Stack line has chevron gutter indicator
	assert.Contains(t, line732, "▸")
}

func TestModel_View_RowSelectionHighlight(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 101, Title: "First PR", RepoNameWithOwner: "org/repo", UpdatedAt: now.Add(-1 * time.Hour)},
			{Number: 102, Title: "Second PR", RepoNameWithOwner: "org/repo", UpdatedAt: now.Add(-2 * time.Hour)},
		},
	}

	m := tui.New(q, tui.WithNow(now), tui.WithDimensions(120, 30), tui.WithActiveTab(tui.TabInbox))
	view := m.View()

	// Selected row (#101, cursor 0) has cursor indicator, unselected row does not
	assert.Contains(t, view, "❯    #101")
	assert.NotContains(t, view, "❯    #102")

	// Moving cursor down moves selection highlight to #102
	mDown, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	viewDown := mDown.(tui.Model).View()
	assert.NotContains(t, viewDown, "❯    #101")
	assert.Contains(t, viewDown, "❯    #102")
}

func TestModel_View_CICountSemanticColors(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	started := now.Add(-3*time.Minute - 27*time.Second)
	completed := now
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:            101,
				Title:             "PR with mixed CI",
				RepoNameWithOwner: "org/repo",
				Checks: model.ChecksSummary{
					Total:       14,
					Done:        14,
					Failed:      1,
					StartedAt:   &started,
					CompletedAt: &completed,
				},
				UpdatedAt: now.Add(-1 * time.Hour),
			},
		},
	}

	m := tui.New(q, tui.WithNow(now), tui.WithDimensions(140, 30), tui.WithActiveTab(tui.TabInbox))
	view := m.View()

	// CI column renders 13 passed and 1 failed with duration 3m27s
	assert.Contains(t, ansi.Strip(view), "✓ 13  ✗ 1  3m27s")
	assert.Contains(t, ansi.Strip(view), "✓ 13")
	assert.Contains(t, ansi.Strip(view), "✗ 1")
	assert.Contains(t, ansi.Strip(view), "3m27s")
}
