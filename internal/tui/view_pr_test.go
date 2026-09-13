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

	m := tui.New(q, tui.WithNow(now), tui.WithPRViewer(viewer))

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

	m := tui.New(q, tui.WithNow(now))
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
	assert.Contains(t, view, "d: diff")
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

func TestModel_ViewPR_DiffReadyMsg_Exec(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            101,
		Title:             "Diff PR",
		RepoNameWithOwner: "org/repo",
		UpdatedAt:         now.Add(-1 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(q, tui.WithNow(now))

	msg := tui.DiffReadyMsg{
		PR: pr,
		Args: []string{
			"difft", "--skip-unchanged", "/tmp/fake-base", "/tmp/fake-head",
		},
	}

	m2, cmd := m.Update(msg)
	require.NotNil(t, cmd)
	model2 := m2.(tui.Model)
	require.NoError(t, model2.ViewErr())

	// ExecProcess yields an execMsg cmd without blocking execution
	execMsg := cmd()
	require.NotNil(t, execMsg)
}

func TestModel_ViewPR_DiffReadyMsg_ErrorBanner(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:            101,
		Title:             "Diff PR",
		RepoNameWithOwner: "org/repo",
		UpdatedAt:         now.Add(-1 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(q, tui.WithNow(now))

	msg := tui.DiffReadyMsg{
		PR:  pr,
		Err: errors.New("tarball download failed: 500 internal server error"),
	}

	m2, cmd := m.Update(msg)
	assert.Nil(t, cmd)

	model2 := m2.(tui.Model)
	require.Error(t, model2.ViewErr())
	assert.Contains(t, model2.ViewErr().Error(), "tarball download failed")
	require.NotNil(t, model2.ViewErrPR())
	assert.Equal(t, 101, model2.ViewErrPR().Number)
	assert.Contains(t, model2.View(), "failed to view #101: tarball download failed")
}
