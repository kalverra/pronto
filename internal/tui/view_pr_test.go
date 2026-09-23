package tui_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

func TestModel_ViewPR_EnterTriggersViewer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            101,
		Title:             "Add auth middleware",
		URL:               "https://github.com/org/repo/pull/101",
		RepoNameWithOwner: "org/repo",
		UpdatedAt:         now.Add(-1 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	var viewedPR *model.PullRequest
	viewer := func(p model.PullRequest) tea.Cmd {
		viewedPR = &p
		return func() tea.Msg {
			return tui.ViewPRMsg{PR: p}
		}
	}

	m := tui.New(q, tui.WithNow(now), tui.WithPRViewer(viewer), tui.WithActiveTab(tui.TabInbox))

	m2, cmd := sendRune(m, 'v')
	require.NotNil(t, cmd)
	require.NotNil(t, viewedPR)
	assert.Equal(t, 101, viewedPR.Number)
	assert.Equal(t, "https://github.com/org/repo/pull/101", viewedPR.URL)

	msg := cmd()
	m3, _ := m2.Update(msg)
	model3 := m3.(tui.Model)
	require.NoError(t, model3.ViewErr())
}

func TestModel_ViewPR_MineTab(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	inboxPR := model.PullRequest{
		Number:    101,
		Title:     "Inbox PR",
		UpdatedAt: now.Add(-1 * time.Hour),
	}
	minePR := model.PullRequest{
		Number:            202,
		Title:             "My authored PR",
		URL:               "https://github.com/org/repo/pull/202",
		RepoNameWithOwner: "org/repo",
		UpdatedAt:         now.Add(-2 * time.Hour),
	}
	q := model.Queue{
		Inbox:    []model.PullRequest{inboxPR},
		Authored: []model.PullRequest{minePR},
	}

	var viewedPR *model.PullRequest
	viewer := func(p model.PullRequest) tea.Cmd {
		viewedPR = &p
		return nil
	}

	m := tui.New(q, tui.WithNow(now), tui.WithPRViewer(viewer))
	mTab, _ := sendKey(m, tea.KeyTab)
	modelTab := mTab.(tui.Model)
	assert.Equal(t, tui.TabMine, modelTab.ActiveTab())

	_, cmd := sendRune(modelTab, 'v')
	assert.Nil(t, cmd)
	require.NotNil(t, viewedPR)
	assert.Equal(t, 202, viewedPR.Number)
}

func TestModel_ViewPR_EmptyQueue(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{}

	var viewerCalled bool
	viewer := func(_ model.PullRequest) tea.Cmd {
		viewerCalled = true
		return nil
	}

	m := tui.New(q, tui.WithNow(now), tui.WithPRViewer(viewer))
	_, cmd := sendRune(m, 'v')
	assert.Nil(t, cmd)
	assert.False(t, viewerCalled)
}

func TestModel_ViewPR_DefaultViewer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            101,
		Title:             "Add auth middleware",
		URL:               "https://github.com/org/repo/pull/101",
		RepoNameWithOwner: "org/repo",
		UpdatedAt:         now.Add(-1 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(q, tui.WithNow(now), tui.WithActiveTab(tui.TabInbox))
	_, cmd := sendRune(m, 'v')
	require.NotNil(t, cmd)
	// Evaluating the Bubbletea ExecProcess cmd produces an execMsg without blocking execution
	msg := cmd()
	assert.NotNil(t, msg)
}

func TestModel_ViewPR_ErrorBanner(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            101,
		Title:             "Failing view PR",
		RepoNameWithOwner: "org/repo",
		UpdatedAt:         now.Add(-1 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(q, tui.WithNow(now))
	m2, _ := m.Update(tui.ViewPRMsg{
		PR:  pr,
		Err: errors.New("gh command not found"),
	})
	model2 := m2.(tui.Model)

	require.Error(t, model2.ViewErr())
	assert.Contains(t, model2.ViewErr().Error(), "gh command not found")
	require.NotNil(t, model2.ViewErrPR())
	assert.Equal(t, 101, model2.ViewErrPR().Number)

	view := model2.View()
	assert.Contains(t, view, "⚠ failed to view #101: gh command not found")

	// Navigation acknowledges/clears error banner
	m3, _ := sendKey(model2, tea.KeyDown)
	model3 := m3.(tui.Model)
	require.NoError(t, model3.ViewErr())
	assert.Nil(t, model3.ViewErrPR())
	assert.NotContains(t, model3.View(), "failed to view #101")
}

func TestModel_HelpBarIncludesView(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:    101,
				Title:     "Sample PR",
				UpdatedAt: now.Add(-1 * time.Hour),
			},
		},
	}

	m := tui.New(q, tui.WithNow(now))
	view := m.View()
	assert.Contains(t, view, "enter: details")
	assert.NotContains(t, view, "d: diff", "diff key was removed")
}

func TestViewPREnv_InteractivePagerWithoutF(t *testing.T) {
	t.Setenv("PAGER", "cat")
	t.Setenv("GH_PAGER", "less -RFX")

	env := tui.ViewPREnv()
	var ghPager, pager string
	for _, kv := range env {
		if after, ok := strings.CutPrefix(kv, "GH_PAGER="); ok {
			ghPager = after
		}
		if after, ok := strings.CutPrefix(kv, "PAGER="); ok {
			pager = after
		}
	}

	assert.Equal(t, "less -R", ghPager, "GH_PAGER must force less -R without -F")
	assert.Equal(t, "less -R", pager, "PAGER must force less -R without -F")
}

func TestViewPREnv_CustomProntoPager(t *testing.T) {
	t.Setenv("PRONTO_PAGER", "custom-pager")

	env := tui.ViewPREnv()
	var ghPager string
	for _, kv := range env {
		if after, ok := strings.CutPrefix(kv, "GH_PAGER="); ok {
			ghPager = after
		}
	}

	assert.Equal(t, "custom-pager", ghPager)
}

func TestModel_DetailModal_CIDuration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	startedRunning := now.Add(-14*time.Minute - 12*time.Second)
	startedDone := now.Add(-20*time.Minute - 12*time.Second)
	completedDone := now.Add(-12 * time.Minute)

	t.Run("running CI shows elapsed duration", func(t *testing.T) {
		t.Parallel()

		pr := model.PullRequest{
			Number:            101,
			Title:             "Running CI PR",
			RepoNameWithOwner: "org/repo",
			Checks: model.ChecksSummary{
				Total:     2,
				Done:      1,
				Running:   1,
				StartedAt: &startedRunning,
			},
		}
		q := model.Queue{Inbox: []model.PullRequest{pr}}
		m := tui.New(q, tui.WithNow(now), tui.WithActiveTab(tui.TabInbox))

		mOpened, _ := sendKey(m, tea.KeyEnter)
		view := mOpened.View()

		assert.Contains(t, view, "CI Checks:")
		assert.Contains(t, view, "1/2 (1 ⠋)")
		assert.Contains(t, view, "14m12s")
		assert.NotContains(t, view, "1/2 (1 ⠋) 14m12s", "badge and duration should be styled separately")
		assert.Contains(t, view, "Total: 2 | Done: 1 | Failed: 0 | Running: 1 | Elapsed: 14m12s")
	})

	t.Run("settled CI shows total duration", func(t *testing.T) {
		t.Parallel()

		pr := model.PullRequest{
			Number:            102,
			Title:             "Settled CI PR",
			RepoNameWithOwner: "org/repo",
			Checks: model.ChecksSummary{
				Total:       2,
				Done:        2,
				StartedAt:   &startedDone,
				CompletedAt: &completedDone,
			},
		}
		q := model.Queue{Inbox: []model.PullRequest{pr}}
		m := tui.New(q, tui.WithNow(now), tui.WithActiveTab(tui.TabInbox))

		mOpened, _ := sendKey(m, tea.KeyEnter)
		view := mOpened.View()

		assert.Contains(t, view, "CI Checks:")
		assert.Contains(t, view, "✓ 2/2")
		assert.Contains(t, view, "8m12s")
		assert.NotContains(t, view, "✓ 2/2 8m12s", "badge and duration should be styled separately")
		assert.Contains(t, view, "Total: 2 | Done: 2 | Failed: 0 | Running: 0 | Duration: 8m12s")
	})

	t.Run("checks without timestamps do not show elapsed or duration", func(t *testing.T) {
		t.Parallel()

		pr := model.PullRequest{
			Number:            103,
			Title:             "No Timestamps PR",
			RepoNameWithOwner: "org/repo",
			Checks: model.ChecksSummary{
				Total: 2,
				Done:  2,
			},
		}
		q := model.Queue{Inbox: []model.PullRequest{pr}}
		m := tui.New(q, tui.WithNow(now), tui.WithActiveTab(tui.TabInbox))

		mOpened, _ := sendKey(m, tea.KeyEnter)
		view := mOpened.View()

		assert.Contains(t, view, "CI Checks:")
		assert.Contains(t, view, "✓ 2/2")
		assert.Contains(t, view, "Total: 2 | Done: 2 | Failed: 0 | Running: 0")
		assert.NotContains(t, view, "Elapsed:")
		assert.NotContains(t, view, "Duration:")
	})
}

func TestFormatCIDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		d        time.Duration
		expected string
	}{
		{name: "zero", d: 0, expected: "0s"},
		{name: "negative", d: -5 * time.Second, expected: "0s"},
		{name: "sub-second", d: 800 * time.Millisecond, expected: "0s"},
		{name: "seconds only", d: 45 * time.Second, expected: "45s"},
		{name: "exact minute", d: 5 * time.Minute, expected: "5m0s"},
		{name: "minutes and seconds", d: 14*time.Minute + 12*time.Second, expected: "14m12s"},
		{name: "sub-second truncated", d: 14*time.Minute + 12*time.Second + 900*time.Millisecond, expected: "14m12s"},
		{name: "exact hour", d: 1 * time.Hour, expected: "1h0m0s"},
		{name: "hours minutes seconds", d: 1*time.Hour + 14*time.Minute + 12*time.Second, expected: "1h14m12s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tui.FormatCIDuration(tt.d))
		})
	}
}

func TestModel_CIBadge_DifferentiatesDuration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	started := now.Add(-14*time.Minute - 12*time.Second)
	completed := now

	pr := model.PullRequest{
		Number:            101,
		Title:             "PR with checks",
		RepoNameWithOwner: "org/repo",
		Checks: model.ChecksSummary{
			Total:       2,
			Done:        2,
			StartedAt:   &started,
			CompletedAt: &completed,
		},
	}

	m := tui.New(
		model.Queue{Inbox: []model.PullRequest{pr}},
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)
	view := m.View()

	// Badge result and duration must be separated, not styled as a single unseparated chunk
	assert.Contains(t, view, "✓ 2/2")
	assert.Contains(t, view, "14m12s")
	assert.NotContains(
		t,
		view,
		"✓ 2/2 14m12s",
		"badge text and timestamp must not be formatted as a single monolithic styled string",
	)
}
