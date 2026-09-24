package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
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

	// Stack tags
	assert.Equal(t, accentViolet, styleStackTag.GetForeground())
	assert.Equal(t, accentCharcoal, styleRangeTag.GetForeground())
}
