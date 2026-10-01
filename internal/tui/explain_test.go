package tui_test

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/score"
	"github.com/kalverra/pronto/internal/tui"
)

func TestModel_ExplainPR_PriorityTab(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            101,
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Title:             "Fix auth flow",
		DirectRequest:     true,
		UpdatedAt:         now.Add(-time.Hour),
		MergeStateStatus:  "CLEAN",
	}

	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithActiveTab(tui.TabPriority),
	)

	scored := score.Scored{PR: pr}
	exp := m.ExplainPR(scored)

	assert.Equal(t, tui.TabPriority, exp.Tab)
	assert.Equal(t, "Priority", exp.TabName)
	assert.Contains(t, exp.TabReason, "Direct review requested")
	assert.Contains(t, exp.SectionReason, "Ready for review")
	assert.NotEmpty(t, exp.WillNotify)
	assert.NotEmpty(t, exp.WontNotify)
}

func TestModel_ExplainPR_FocusTab(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            102,
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Title:             "Fix critical issue",
		DirectRequest:     true,
		UpdatedAt:         now.Add(-time.Hour),
		MergeStateStatus:  "CLEAN",
	}

	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	t.Run("manual focus", func(t *testing.T) {
		t.Parallel()
		m := tui.New(
			q,
			tui.WithViewer("kalverra"),
			tui.WithNow(now),
			tui.WithActiveTab(tui.TabFocus),
			tui.WithFocusedPRs([]model.PRKey{pr.Key()}),
		)

		exp := m.ExplainPR(score.Scored{PR: pr})
		assert.Equal(t, tui.TabFocus, exp.Tab)
		assert.Contains(t, exp.TabReason, "Manually focused")
		assert.Equal(t, tui.TabPriority, exp.BaseTab)
		assert.Contains(t, exp.BaseTabReason, "Direct review requested")
	})

	t.Run("rule focus", func(t *testing.T) {
		t.Parallel()
		m := tui.New(
			q,
			tui.WithViewer("kalverra"),
			tui.WithNow(now),
			tui.WithActiveTab(tui.TabFocus),
			tui.WithFocusConfig(config.RuleSet{
				Rules: []config.Rule{{Keywords: []string{"critical"}}},
			}),
		)

		exp := m.ExplainPR(score.Scored{PR: pr})
		assert.Equal(t, tui.TabFocus, exp.Tab)
		assert.Contains(t, exp.TabReason, "Matched focus rule")
		assert.Contains(t, exp.TabReason, "critical")
	})
}

func TestModel_ExplainPR_MineTab(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            103,
		RepoNameWithOwner: "org/repo",
		Author:            "kalverra",
		Title:             "My awesome feature",
		UpdatedAt:         now.Add(-time.Hour),
		MergeStateStatus:  "CLEAN",
	}

	q := model.Queue{
		Authored: []model.PullRequest{pr},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithActiveTab(tui.TabMine),
	)

	exp := m.ExplainPR(score.Scored{PR: pr})
	assert.Equal(t, tui.TabMine, exp.Tab)
	assert.Contains(t, exp.TabReason, "Authored by you")
	assert.Contains(t, exp.SectionReason, "Approved and clean")
}

func TestModel_ExplainPR_InboxTab(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            104,
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Title:             "Some normal PR",
		UpdatedAt:         now.Add(-time.Hour),
		MergeStateStatus:  "CLEAN",
	}

	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithActiveTab(tui.TabInbox),
	)

	exp := m.ExplainPR(score.Scored{PR: pr})
	assert.Equal(t, tui.TabInbox, exp.Tab)
	assert.Contains(t, exp.TabReason, "Incoming review request")
}

func TestModel_ScoreModal_CategoryAndNotificationsInView(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            105,
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Title:             "Fix cache race condition",
		DirectRequest:     true,
		UpdatedAt:         now.Add(-time.Hour),
		MergeStateStatus:  "CLEAN",
	}

	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithActiveTab(tui.TabPriority),
	)

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model2 := m2.(tui.Model)
	require.True(t, model2.IsModalOpen())

	view := model2.View()
	assert.Contains(t, view, "Score Breakdown: org/repo#105")
	assert.Contains(t, view, "Category: Priority")
	assert.Contains(t, view, "Direct review requested from you")
	assert.Contains(t, view, "Notifications:")
	assert.Contains(t, view, "Will notify")
	assert.Contains(t, view, "Won't notify")
	assert.Contains(t, view, "Score:")
}
