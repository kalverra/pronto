package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
)

func TestApplyRowBackground_ContinuousBackgroundAcrossResets(t *testing.T) {
	t.Parallel()

	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	bg := lipgloss.Color("#21262d")
	sample := r.NewStyle().Background(bg).Render(" ")
	idx := strings.Index(sample, " ")
	require.NotEqual(t, -1, idx)
	bgSeq := sample[:idx]

	// Simulate a rendered table row with multiple styled cells that have resets
	styleRed := r.NewStyle().Foreground(lipgloss.Color("#f85149"))
	styleGreen := r.NewStyle().Foreground(lipgloss.Color("#3fb950"))
	rawLine := "  #101  " + styleRed.Render("✖ FAILING") + "  " + styleGreen.Render("+10 -5") + "  repo"

	highlighted := applyRowBackground(rawLine, bg)

	// 1. Plain text content is unchanged
	assert.Equal(t, ansi.Strip(rawLine), ansi.Strip(highlighted))

	// 2. Starts with background sequence
	assert.True(t, strings.HasPrefix(highlighted, bgSeq), "highlighted row must start with background sequence")

	// 3. Ends with clean reset
	assert.True(t, strings.HasSuffix(highlighted, "\x1b[0m"), "highlighted row must end with reset sequence")

	// 4. No bare reset inside the line (every \x1b[0m before the end must be followed by bgSeq)
	contentWithoutFinalReset := strings.TrimSuffix(highlighted, "\x1b[0m")
	// If any \x1b[0m exists, it must be followed by bgSeq
	parts := strings.Split(contentWithoutFinalReset, "\x1b[0m")
	for i := range len(parts) - 1 {
		assert.True(
			t,
			strings.HasPrefix(parts[i+1], bgSeq),
			"reset at index %d was not followed by background sequence %q; got prefix %q",
			i, bgSeq, parts[i+1][:min(len(parts[i+1]), len(bgSeq)+5)],
		)
	}
}

func TestModel_View_SelectedRowHasContinuousHighlightAndPop(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:            23503,
				Title:             "Lint Roller 11: deployment (ccip)",
				RepoName:          "chainlink",
				RepoNameWithOwner: "smartcontractkit/chainlink",
				Additions:         1400,
				Deletions:         2600,
				UpdatedAt:         now.Add(-5 * time.Minute),
				Stack:             &model.PRStack{ID: "STACK_A", Number: 23500, Size: 4, Position: 1},
			},
			{
				Number:            691,
				Title:             "feat(cli): add cd verify context resolution",
				RepoName:          "infra-griddle-app",
				RepoNameWithOwner: "smartcontractkit/infra-griddle-app",
				UpdatedAt:         now.Add(-24 * time.Hour),
			},
		},
	}

	m := New(
		q,
		WithViewer("kalverra"),
		WithNow(now),
		WithDimensions(160, 40),
		WithActiveTab(TabInbox),
	)

	view := m.View()

	// Both PRs exist in the view
	assert.Contains(t, ansi.Strip(view), "#23503")
	assert.Contains(t, ansi.Strip(view), "#691")

	// Verify line for selected PR (#23503) contains continuous background highlight
	lines := strings.Split(view, "\n")
	var selectedLine string
	for _, l := range lines {
		if strings.Contains(ansi.Strip(l), "#23503") {
			selectedLine = l
			break
		}
	}
	require.NotEmpty(t, selectedLine, "must find line with selected PR #23503")

	bg := lipgloss.Color("#21262d")
	sample := lipgloss.NewStyle().Background(bg).Render(" ")
	idx := strings.Index(sample, " ")
	require.NotEqual(t, -1, idx)
	bgSeq := sample[:idx]

	// The selected line must contain the background sequence
	assert.Contains(t, selectedLine, bgSeq, "selected line must contain background sequence")

	// Ensure no bare reset inside the line turns off background
	contentWithoutFinalReset := strings.TrimSuffix(strings.TrimRight(selectedLine, " "), "\x1b[0m")
	parts := strings.Split(contentWithoutFinalReset, "\x1b[0m")
	for i := range len(parts) - 1 {
		assert.True(
			t,
			strings.HasPrefix(parts[i+1], bgSeq),
			"reset at index %d was not followed by background sequence",
			i,
		)
	}
}

func TestSelectionPop_StylesApplied(t *testing.T) {
	t.Parallel()

	// 1. Diffs: selected vs unselected styles are configured bold
	assert.True(t, selectedDiffAddStyle.GetBold())
	assert.True(t, selectedDiffDelStyle.GetBold())
	assert.False(t, diffAddStyle.GetBold())
	assert.False(t, diffDelStyle.GetBold())

	// 2. Diff size rendering
	diffSel := renderDiffSize(50, 10, true)
	diffUnsel := renderDiffSize(50, 10, false)
	assert.Equal(t, ansi.Strip(diffSel), ansi.Strip(diffUnsel))

	// 3. Stack meta tag: selected is bold
	metaSel := formatCollapsedStackMeta(model.PullRequest{Stack: &model.PRStack{Position: 1, Size: 3}}, true)
	metaUnsel := formatCollapsedStackMeta(model.PullRequest{Stack: &model.PRStack{Position: 1, Size: 3}}, false)
	assert.Equal(t, ansi.Strip(metaSel), ansi.Strip(metaUnsel))

	// 4. Author rendering: selected author style is bold
	pr := model.PullRequest{Author: "octocat"}
	authorSel := renderAuthor(pr, true)
	authorUnsel := renderAuthor(pr, false)
	assert.Equal(t, ansi.Strip(authorSel), ansi.Strip(authorUnsel))

	// 5. PR number: selected is bold and high-contrast
	assert.True(t, selectedNumStyle.GetBold())
	assert.False(t, numStyle.GetBold())
	assert.Equal(t, lipgloss.Color("#79c0ff"), selectedNumStyle.GetForeground())

	// 6. Updated time & repo: selected are bold and high-contrast
	assert.True(t, selectedUpdatedStyle.GetBold())
	assert.True(t, selectedRepoStyle.GetBold())
	assert.False(t, updatedStyle.GetBold())
	assert.False(t, repoStyle.GetBold())
}
