package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

// The bubbletea renderer keeps only the last height lines of an oversized
// view, cutting from the top. Any overflow shifts every screen coordinate
// (breaking mouse hit-testing) and hides the tab bar, so View must fit.
func TestView_FitsTerminalHeight(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	var prs []model.PullRequest
	for i := range 80 {
		prs = append(prs, model.PullRequest{
			Number: 700 + i, Title: fmt.Sprintf("PR %d", i), Author: "alice",
			RepoName: "repo", RepoNameWithOwner: "org/repo",
			Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN",
		})
	}
	notifs := []notify.Notification{
		{Trigger: notify.TriggerCIFailed, PRNumber: 700, PRTitle: "PR 0", Repo: "org/repo"},
		{Trigger: notify.TriggerCIPassed, PRNumber: 701, PRTitle: "PR 1", Repo: "org/repo"},
	}

	states := map[string]func(*Model){
		"plain":         func(*Model) {},
		"notifications": func(m *Model) { m.notifications = notifs },
		"hidden notifs": func(m *Model) { m.notifications, m.hideNotifs = notifs, true },
		"fetch error":   func(m *Model) { m.fetchErr = errors.New("boom") },
		"notifs+banner": func(m *Model) { m.notifications, m.fetchErr = notifs, errors.New("boom") },
	}
	for name, apply := range states {
		for _, h := range []int{16, 24, 45} {
			for _, w := range []int{90, 160} {
				m := New(model.Queue{Inbox: prs}, WithNow(now), WithDimensions(w, h), WithActiveTab(TabInbox))
				apply(&m)
				m = m.clampAllTabViews()
				if m.VisibleRows() <= 1 {
					continue // chrome alone exceeds the terminal; nothing left to shrink
				}
				lines := len(strings.Split(m.View(), "\n"))
				assert.LessOrEqual(t, lines, h, "%s at %dx%d", name, w, h)
			}
		}
	}
}

// When chrome alone exceeds the terminal, overflow is unavoidable and the
// renderer cuts the top of the view; clicks must map screen lines back to view
// lines (here the tab bar lands on screen line 0 instead of 1).
func TestMouse_ClickSurvivesUnavoidableOverflow(t *testing.T) {
	t.Parallel()

	var prs []model.PullRequest
	for i := range 40 {
		prs = append(prs, model.PullRequest{
			Number: 800 + i, Title: fmt.Sprintf("PR %d", i), Author: "alice",
			RepoName: "repo", RepoNameWithOwner: "org/repo",
			Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN",
		})
	}
	const height = 16
	m := New(model.Queue{Inbox: prs}, WithDimensions(120, height), WithActiveTab(TabInbox))
	m.notifications = []notify.Notification{
		{Trigger: notify.TriggerCIFailed, PRNumber: 900, PRTitle: "N0", Repo: "org/repo"},
		{Trigger: notify.TriggerCIPassed, PRNumber: 901, PRTitle: "N1", Repo: "org/repo"},
	}
	m.fetchErr = errors.New("boom")
	m = m.clampAllTabViews()

	lines := strings.Split(m.View(), "\n")
	require.Greater(t, len(lines), height, "precondition: view overflows")
	screen := lines[len(lines)-height:]

	x, y := -1, -1
	for i, line := range screen {
		if before, _, ok := strings.Cut(line, "1: Focus"); ok {
			x, y = len([]rune(before)), i
			break
		}
	}
	require.GreaterOrEqual(t, y, 0, "tab bar visible on screen")
	require.NotEqual(t, viewMarginTop, y, "precondition: overflow shifted the tab bar")

	out, _ := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	assert.Equal(t, TabFocus, out.(Model).ActiveTab())
}
