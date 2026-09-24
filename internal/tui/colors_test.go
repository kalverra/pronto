package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
)

func TestUnifiedColorPalette(t *testing.T) {
	t.Parallel()

	// SIZE column: diff additions and deletions must use identical muted colors
	assert.Equal(t, diffAddStyle.GetForeground(), styleAdd.GetForeground())
	assert.Equal(t, diffDelStyle.GetForeground(), styleDel.GetForeground())
	assert.Equal(t, lipgloss.Color("#57ab5a"), styleAdd.GetForeground())
	assert.Equal(t, lipgloss.Color("#c9514c"), styleDel.GetForeground())

	// PR numbers: muted gray
	assert.Equal(t, lipgloss.Color("#6b7280"), numStyle.GetForeground())

	// CI & STATUS: glyphs must match single PR status badges
	assert.Equal(t, ciPassStyle.GetForeground(), styleGlyphPass.GetForeground())
	assert.Equal(t, badgeCleanStyle.GetForeground(), styleGlyphPass.GetForeground())

	assert.Equal(t, ciFailStyle.GetForeground(), styleGlyphFail.GetForeground())
	assert.Equal(t, badgeConflictStyle.GetForeground(), styleGlyphFail.GetForeground())

	assert.Equal(t, ciRunningStyle.GetForeground(), styleGlyphRun.GetForeground())
	assert.Equal(t, badgeBehindStyle.GetForeground(), styleGlyphReview.GetForeground())

	assert.Equal(t, badgeDraftStyle.GetForeground(), styleGlyphDraft.GetForeground())
	assert.Equal(t, lipgloss.Color("#b1bac4"), badgeDraftStyle.GetForeground())

	// Tab bar: light styling without solid block background
	assert.Equal(t, lipgloss.NoColor{}, activeTabStyle.GetBackground())
	assert.Equal(t, lipgloss.Color("#58a6ff"), activeTabStyle.GetForeground())

	// Stack tags
	assert.Equal(t, accentViolet, styleStackTag.GetForeground())
	assert.Equal(t, accentCharcoal, styleRangeTag.GetForeground())

	// Selected row highlight & row styles
	assert.Equal(t, lipgloss.Color("#21262d"), selectedRowBg)
	assert.Equal(t, lipgloss.Color("#ffffff"), selectedRowStyle.GetForeground())
	assert.True(t, selectedRowStyle.GetBold())
	assert.Equal(t, lipgloss.Color("#e6edf3"), unselectedRowStyle.GetForeground())
	assert.False(t, unselectedRowStyle.GetBold())

	// Selected row pop styles (bold & high-contrast colors)
	assert.Equal(t, lipgloss.Color("#79c0ff"), selectedNumStyle.GetForeground())
	assert.True(t, selectedNumStyle.GetBold())
	assert.Equal(t, lipgloss.Color("#e6edf3"), selectedRangeTagStyle.GetForeground())
	assert.True(t, selectedRangeTagStyle.GetBold())
	assert.Equal(t, lipgloss.Color("#e6edf3"), selectedUpdatedStyle.GetForeground())
	assert.True(t, selectedUpdatedStyle.GetBold())
	assert.Equal(t, lipgloss.Color("#e6edf3"), selectedRepoStyle.GetForeground())
	assert.True(t, selectedRepoStyle.GetBold())
	assert.Equal(t, lipgloss.Color("#ffffff"), selectedAuthorStyle.GetForeground())
	assert.True(t, selectedAuthorStyle.GetBold())
	assert.Equal(t, lipgloss.Color("#3fb950"), selectedDiffAddStyle.GetForeground())
	assert.True(t, selectedDiffAddStyle.GetBold())
	assert.Equal(t, lipgloss.Color("#f85149"), selectedDiffDelStyle.GetForeground())
	assert.True(t, selectedDiffDelStyle.GetBold())

	// CI count colors
	assert.Equal(t, lipgloss.Color("#3fb950"), ciPassStyle.GetForeground())
	assert.Equal(t, lipgloss.Color("#f85149"), ciFailStyle.GetForeground())
	assert.Equal(t, lipgloss.Color("#e3b341"), ciRunningStyle.GetForeground())
	assert.Equal(t, lipgloss.Color("242"), ciDurationStyle.GetForeground())
}

func TestFormatColoredCIBadge(t *testing.T) {
	t.Parallel()

	// Mixed CI: 13 passed, 1 failed
	mixed := model.ChecksSummary{
		Total:  14,
		Done:   14,
		Failed: 1,
	}
	res := formatColoredCIBadge(mixed, "⠋")
	assert.Contains(t, res, "✓ 13")
	assert.Contains(t, res, "✗ 1")

	// All passed
	passed := model.ChecksSummary{
		Total: 5,
		Done:  5,
	}
	resPass := formatColoredCIBadge(passed, "⠋")
	assert.Contains(t, resPass, "✓ 5/5")

	// All failed
	failed := model.ChecksSummary{
		Total:  3,
		Done:   3,
		Failed: 3,
	}
	resFail := formatColoredCIBadge(failed, "⠋")
	assert.Contains(t, resFail, "✗ 3 failed")
}
