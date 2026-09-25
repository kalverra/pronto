package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// doubleClickWindow is the maximum gap between two clicks on the same row
// that counts as a double click (opens details, like enter).
const doubleClickWindow = 500 * time.Millisecond

// Screen layout offsets mirrored from View: appStyle's top/left margin and the
// table header plus its border line.
const (
	viewMarginTop    = 1
	viewMarginLeft   = 2
	tableHeaderLines = 2
)

// handleMouse maps wheel motion to cursor movement and left clicks to tab
// switches, row selection, and (on double click) opening details.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.modalOpen || m.detailsOpen || m.confirmClosePR != nil || m.loading {
		return m, nil
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		updated, _ := m.handleNavKey("down")
		return updated, nil
	case tea.MouseButtonWheelUp:
		updated, _ := m.handleNavKey("up")
		return updated, nil
	case tea.MouseButtonLeft:
	default:
		return m, nil
	}

	// The renderer drops the top lines of a view taller than the terminal;
	// map screen y back to view y so hit-testing survives any overflow.
	if m.height > 0 {
		if overflow := len(strings.Split(m.View(), "\n")) - m.height; overflow > 0 {
			msg.Y += overflow
		}
	}

	if msg.Y == viewMarginTop {
		if tab, ok := m.tabAt(msg.X); ok {
			return m.handleTabKey(tab)
		}
		return m, nil
	}

	row, ok := m.displayRowAt(msg.Y)
	if !ok || row.kind == rowDivider {
		return m, nil
	}
	m.notificationFocused = false
	m = m.setCursor(row.itemIndex)
	selected := m.SelectedPR()
	if selected == nil {
		return m, nil
	}
	if row.kind == rowStackBanner {
		m.lastClickAt = time.Time{}
		return m.toggleStack(selected.StackKey()), nil
	}

	key, now := selected.Key(), time.Now()
	if key == m.lastClickKey && now.Sub(m.lastClickAt) <= doubleClickWindow {
		m.lastClickAt = time.Time{}
		m.detailsOpen = true
		return m, nil
	}
	m.lastClickKey, m.lastClickAt = key, now
	return m, nil
}

// tabAt returns the tab-key string ("1".."n") whose label spans screen column x.
func (m Model) tabAt(x int) (string, bool) {
	start := viewMarginLeft
	for i, t := range allTabs {
		end := start + lipgloss.Width(m.tabTitle(t))
		if x >= start && x < end {
			return string(rune('1' + i)), true
		}
		start = end + tabGapWidth
	}
	return "", false
}

// displayRowAt returns the display row rendered on screen line y, if any.
func (m Model) displayRowAt(y int) (displayRow, bool) {
	contentWidth := max(20, m.width-4)
	top := viewMarginTop +
		strings.Count(m.renderTabBar(contentWidth), "\n") +
		strings.Count(m.renderNotificationsArea(), "\n") +
		tableHeaderLines
	rows := m.buildDisplayRows(m.activeTab)
	scroll := min(max(m.ScrollOffset(), 0), len(rows))
	end := min(len(rows), scroll+m.VisibleRows())
	idx := scroll + (y - top)
	if y < top || idx >= end {
		return displayRow{}, false
	}
	return rows[idx], true
}
