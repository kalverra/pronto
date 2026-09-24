package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

func TestModel_View_PRNumbersAndTitlesAlignmentAndConsolidatedStack(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

	prs := []model.PullRequest{
		// 1. Stack of 4 PRs (collapsed): #23503
		{
			Number:            23503,
			Title:             "Lint Roller 11: deployment (ccip)",
			RepoName:          "chainlink",
			RepoNameWithOwner: "smartcontractkit/chainlink",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-1 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_A", Number: 23500, Size: 7, Position: 4},
		},
		{
			Number:            23504,
			Title:             "Lint Roller 12",
			RepoName:          "chainlink",
			RepoNameWithOwner: "smartcontractkit/chainlink",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-1 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_A", Number: 23500, Size: 7, Position: 5},
		},
		{
			Number:            23505,
			Title:             "Lint Roller 13",
			RepoName:          "chainlink",
			RepoNameWithOwner: "smartcontractkit/chainlink",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-1 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_A", Number: 23500, Size: 7, Position: 6},
		},
		{
			Number:            23506,
			Title:             "Lint Roller 14",
			RepoName:          "chainlink",
			RepoNameWithOwner: "smartcontractkit/chainlink",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-1 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_A", Number: 23500, Size: 7, Position: 7},
		},
		// 2. Stack of 4 PRs (collapsed): #691 (short PR number)
		{
			Number:            691,
			Title:             "feat(cli): add cd verify context resolution",
			RepoName:          "infra-griddle-app",
			RepoNameWithOwner: "smartcontractkit/infra-griddle-app",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-2 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_B", Number: 691, Size: 4, Position: 1},
		},
		{
			Number:            692,
			Title:             "feat(cli): add cd verify child",
			RepoName:          "infra-griddle-app",
			RepoNameWithOwner: "smartcontractkit/infra-griddle-app",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-2 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_B", Number: 691, Size: 4, Position: 2},
		},
		// 3. Solo PR: #23787
		{
			Number:            23787,
			Title:             "chore: bump ci versions",
			RepoName:          "chainlink",
			RepoNameWithOwner: "smartcontractkit/chainlink",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-2 * time.Hour),
		},
		// 4. Solo stack member (totalInCat <= 1): #23611
		{
			Number:            23611,
			Title:             "feat(githooks+ci): use modern actionlint",
			RepoName:          "chainlink",
			RepoNameWithOwner: "smartcontractkit/chainlink",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-3 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_C", Number: 23600, Size: 3, Position: 3},
		},
		// 5. Solo PR (very short number): #14
		{
			Number:            14,
			Title:             "chore(bump-versions)",
			RepoName:          "chainlink",
			RepoNameWithOwner: "smartcontractkit/chainlink",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-4 * time.Hour),
		},
		// 6. Solo PR: #23569
		{
			Number:            23569,
			Title:             "Break Up PRs",
			RepoName:          "chainlink",
			RepoNameWithOwner: "smartcontractkit/chainlink",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-5 * time.Hour),
		},
	}

	q := model.Queue{Authored: prs}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(180, 40),
		tui.WithActiveTab(tui.TabMine),
	)

	view := m.View()

	// 1. Collapsed stack must consolidate data: show how big stack is, NO [n..n+x]
	assert.Contains(t, view, "⎘ 4 PRs")
	assert.NotContains(t, view, "[4..7]")
	assert.NotContains(t, view, "[1..4]")
	assert.Contains(t, view, "⎘ [3/3]")

	// 2. Alignment: PR numbers and titles must align neatly across rows
	lines := strings.Split(view, "\n")
	type prRowInfo struct {
		numStr    string
		titleFrag string
	}
	expectedRows := []prRowInfo{
		{numStr: "#23503", titleFrag: "Lint Roller 11: deployment (ccip)"},
		{numStr: "#691", titleFrag: "feat(cli): add cd verify context resolution"},
		{numStr: "#23787", titleFrag: "chore: bump ci versions"},
		{numStr: "#23611", titleFrag: "feat(githooks+ci): use modern actionlint"},
		{numStr: "#14", titleFrag: "chore(bump-versions)"},
		{numStr: "#23569", titleFrag: "Break Up PRs"},
	}

	findRuneSubstr := func(r []rune, sub string) int {
		subRunes := []rune(sub)
		for i := 0; i <= len(r)-len(subRunes); i++ {
			match := true
			for j := range subRunes {
				if r[i+j] != subRunes[j] {
					match = false
					break
				}
			}
			if match {
				return i
			}
		}
		return -1
	}

	var prNumCol, titleCol int
	for i, exp := range expectedRows {
		var matchedLine string
		for _, l := range lines {
			clean := ansi.Strip(l)
			if strings.Contains(clean, exp.numStr) && strings.Contains(clean, exp.titleFrag) {
				matchedLine = clean
				break
			}
		}
		require.NotEmpty(t, matchedLine, "must find line with PR %s in view", exp.numStr)

		runes := []rune(matchedLine)
		numIdx := findRuneSubstr(runes, exp.numStr)
		titleIdx := findRuneSubstr(runes, exp.titleFrag)

		if i == 0 {
			prNumCol = numIdx
			titleCol = titleIdx
			assert.GreaterOrEqual(t, prNumCol, 0)
			assert.GreaterOrEqual(t, titleCol, 0)
		} else {
			assert.Equal(
				t,
				prNumCol,
				numIdx,
				"PR number %s must start at column %d (got %d)",
				exp.numStr,
				prNumCol,
				numIdx,
			)
			assert.Equal(
				t,
				titleCol,
				titleIdx,
				"PR title %q must start at column %d (got %d)",
				exp.titleFrag,
				titleCol,
				titleIdx,
			)
		}
	}
}

func TestModel_View_SoloPRsOnly_NoWastedStackSpacing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

	prs := []model.PullRequest{
		{
			Number:            101,
			Title:             "Alpha PR",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-1 * time.Hour),
		},
		{
			Number:            102,
			Title:             "Beta PR",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-2 * time.Hour),
		},
	}

	q := model.Queue{Authored: prs}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabMine),
	)

	view := m.View()

	// When no stacks exist, titles should not have wide blank padding for non-existent stacks
	assert.Contains(t, view, "#101  Alpha PR")
	assert.Contains(t, view, "#102  Beta PR")
}
