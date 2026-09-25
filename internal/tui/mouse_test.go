package tui_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

// screenLines mimics bubbletea's renderer: an oversized view keeps only its
// last height lines.
func screenLines(m tui.Model, height int) []string {
	lines := strings.Split(m.View(), "\n")
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	return lines
}

// locate returns the screen cell (x, y) where needle first renders on screen.
func locate(t *testing.T, m tui.Model, needle string) (int, int) {
	t.Helper()
	for y, line := range screenLines(m, m.Height()) {
		if before, _, ok := strings.Cut(line, needle); ok {
			return len([]rune(before)), y
		}
	}
	t.Fatalf("%q not rendered in view:\n%s", needle, m.View())
	return 0, 0
}

func click(t *testing.T, m tui.Model, needle string) tui.Model {
	t.Helper()
	x, y := locate(t, m, needle)
	out, _ := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return out.(tui.Model)
}

func mouseQueue(n int) model.Queue {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	var prs []model.PullRequest
	for i := range n {
		prs = append(prs, model.PullRequest{
			Number:            500 + i,
			Title:             fmt.Sprintf("Mouse PR %d", i),
			Author:            "alice",
			RepoOwner:         "org",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			Mergeable:         "MERGEABLE",
			MergeStateStatus:  "CLEAN",
			MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
			TimelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    now.Add(-time.Duration(i+1) * time.Hour),
					ReviewerTeam: "org/core",
				},
			},
		})
	}
	return model.Queue{Inbox: prs}
}

func newMouseModel(n, height int) tui.Model {
	return tui.New(mouseQueue(n),
		tui.WithNow(time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)),
		tui.WithDimensions(140, height),
		tui.WithActiveTab(tui.TabInbox),
	)
}

func TestMouse_ClickRowSelectsPR(t *testing.T) {
	t.Parallel()

	m := newMouseModel(4, 40)
	require.NotEqual(t, 502, m.SelectedPR().Number)

	m = click(t, m, "#502")
	require.NotNil(t, m.SelectedPR())
	assert.Equal(t, 502, m.SelectedPR().Number)
	assert.False(t, m.IsDetailsOpen(), "single click only selects")
}

func TestMouse_DoubleClickOpensDetails(t *testing.T) {
	t.Parallel()

	m := newMouseModel(4, 40)
	m = click(t, m, "#501")
	m = click(t, m, "#501")
	assert.True(t, m.IsDetailsOpen())
	assert.Equal(t, 501, m.SelectedPR().Number)
}

func TestMouse_ClickDifferentRowsDoesNotOpenDetails(t *testing.T) {
	t.Parallel()

	m := newMouseModel(4, 40)
	m = click(t, m, "#501")
	m = click(t, m, "#502")
	assert.False(t, m.IsDetailsOpen())
	assert.Equal(t, 502, m.SelectedPR().Number)
}

func TestMouse_ClickRespectsScroll(t *testing.T) {
	t.Parallel()

	m := newMouseModel(30, 20)
	end, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = end.(tui.Model)
	require.Positive(t, m.ScrollOffset())

	m = click(t, m, "#520")
	require.NotNil(t, m.SelectedPR())
	assert.Equal(t, 520, m.SelectedPR().Number)
}

func TestMouse_ClickTabSwitches(t *testing.T) {
	t.Parallel()

	m := newMouseModel(2, 40)
	for _, tc := range []struct {
		label string
		tab   tui.Tab
	}{
		{"1: Focus", tui.TabFocus},
		{"3: Priority", tui.TabPriority},
		{"2: Mine", tui.TabMine},
		{"4: Inbox", tui.TabInbox},
	} {
		m = click(t, m, tc.label)
		assert.Equal(t, tc.tab, m.ActiveTab(), tc.label)
	}
}

func TestMouse_WheelMovesCursor(t *testing.T) {
	t.Parallel()

	m := newMouseModel(4, 40)
	start := m.Cursor()
	down, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = down.(tui.Model)
	assert.Equal(t, start+1, m.Cursor())

	up, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	assert.Equal(t, start, up.(tui.Model).Cursor())
}

func TestMouse_ClickTabsWithFullList(t *testing.T) {
	t.Parallel()

	// A list longer than the screen fills every visible row.
	m := newMouseModel(80, 24)
	for _, tc := range []struct {
		label string
		tab   tui.Tab
	}{
		{"1: Focus", tui.TabFocus},
		{"4: Inbox", tui.TabInbox},
		{"2: Mine", tui.TabMine},
		{"4: Inbox", tui.TabInbox},
	} {
		m = click(t, m, tc.label)
		assert.Equal(t, tc.tab, m.ActiveTab(), tc.label)
	}
	m = click(t, m, "#505")
	assert.Equal(t, 505, m.SelectedPR().Number, "row clicks land on the clicked row")
}
