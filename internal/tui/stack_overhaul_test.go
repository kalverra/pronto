package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

func TestModel_Stack_CollapsedRow_MicroRibbonAndDiffRollup(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	// Create 6 PRs in a stack matching prompt's example:
	// PR 709 [2/7] Needs Review, +673 -1100
	// PR 710 [3/7] Needs Review, +1100 -19
	// PR 711 [4/7] CIFailed,     +24 -20
	// PR 712 [5/7] CIRunning,    +1600 -474
	// PR 732 [6/7] Draft,        +422 -27
	// PR 733 [7/7] Draft,        +38 -38
	prs := []model.PullRequest{
		{
			Number:            709,
			Title:             "chore: updates to latest go-github",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         673,
			Deletions:         1100,
			ReviewDecision:    "REVIEW_REQUIRED",
			UpdatedAt:         now.Add(-1 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 2},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
		{
			Number:            710,
			Title:             "tests: add go-github mock tests",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         1100,
			Deletions:         19,
			ReviewDecision:    "REVIEW_REQUIRED",
			UpdatedAt:         now.Add(-2 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 3},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
		{
			Number:            711,
			Title:             "chore: modern yaml parser",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         24,
			Deletions:         20,
			Checks:            model.ChecksSummary{Total: 5, Failed: 1},
			UpdatedAt:         now.Add(-2 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 4},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
		{
			Number:            712,
			Title:             "parallel tests",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         1600,
			Deletions:         474,
			Checks:            model.ChecksSummary{Total: 5, Running: 2},
			UpdatedAt:         now.Add(-2 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 5},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
		{
			Number:            732,
			Title:             "chore: e2e test coverage",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         422,
			Deletions:         27,
			IsDraft:           true,
			UpdatedAt:         now.Add(-1 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 6},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
		{
			Number:            733,
			Title:             "update versions",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         38,
			Deletions:         38,
			IsDraft:           true,
			UpdatedAt:         now.Add(-1 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 7},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
	}

	q := model.Queue{Inbox: prs}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(160, 40),
		tui.WithActiveTab(tui.TabInbox),
	)

	view := m.View()

	// 1. Fold indicator on selected row: ❯ ▸ #709
	assert.Contains(t, view, "❯ ▸")
	assert.Contains(t, view, "#709")

	// 2. Stack Pill: [STACK] 6 PRs
	assert.Contains(t, view, "[STACK] 6 PRs")
	assert.NotContains(t, view, "[2..7]")

	// 3. Root PR title
	assert.Contains(t, view, "chore: updates to latest go-github")

	// 4. Micro-status ribbon: ● ● ✖ ◌ ○ ○
	assert.Contains(t, view, "● ● ✖ ◌ ○ ○")

	// 5. Micro-CI ribbon: ○ ○ ✖ ◌ ○ ○
	assert.Contains(t, view, "○ ○ ✖ ◌ ○ ○")

	// 6. Diff roll-up: sum adds = 673+1100+24+1600+422+38 = 3857 -> +3.9k
	// sum dels = 1100+19+20+474+27+38 = 1678 -> -1.7k
	assert.Contains(t, view, "+3.9k -1.7k")

	// 6. Child PR titles must be hidden in collapsed view
	assert.NotContains(t, view, "tests: add go-github mock tests")
	assert.NotContains(t, view, "chore: modern yaml parser")
	assert.NotContains(t, view, "update versions")
}

func TestModel_Stack_ExpandedFolderBannerAndTreeConnectors(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	prs := []model.PullRequest{
		{
			Number:            709,
			Title:             "chore: updates to latest go-github",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         673,
			Deletions:         1100,
			ReviewDecision:    "REVIEW_REQUIRED",
			UpdatedAt:         now.Add(-1 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 2},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
		{
			Number:            710,
			Title:             "tests: add go-github mock tests",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         1100,
			Deletions:         19,
			ReviewDecision:    "REVIEW_REQUIRED",
			UpdatedAt:         now.Add(-2 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 3},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
		{
			Number:            711,
			Title:             "chore: modern yaml parser",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         24,
			Deletions:         20,
			Checks:            model.ChecksSummary{Total: 5, Failed: 1},
			UpdatedAt:         now.Add(-2 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 4},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
		{
			Number:            733,
			Title:             "update versions",
			RepoName:          "infra-griddle",
			RepoNameWithOwner: "org/infra-griddle",
			Additions:         38,
			Deletions:         38,
			Checks:            model.ChecksSummary{Total: 5, Failed: 1},
			UpdatedAt:         now.Add(-1 * time.Hour),
			Stack:             &model.PRStack{ID: "STACK_1", Number: 709, Size: 7, Position: 7},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
	}

	q := model.Queue{Inbox: prs}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(160, 40),
		tui.WithCollapsedStacks(false),
		tui.WithActiveTab(tui.TabInbox),
	)

	view := m.View()

	// 1. Open folder banner
	assert.Contains(t, view, "▾")
	assert.Contains(t, view, "[STACK] (4 PRs · #709..#733)")
	assert.Contains(t, view, "[space: fold]")

	// 2. Child rows with connectors
	assert.Contains(t, view, "├─ #709 [2/7]")
	assert.Contains(t, view, "├─ #710 [3/7]")
	assert.Contains(t, view, "├─ #711 [4/7]")
	assert.Contains(t, view, "╰─ #733 [7/7]")

	// 3. Child status column
	assert.Contains(t, view, "● Needs Review")
	assert.Contains(t, view, "✖ Failing CI")

	// 4. Child diffs
	assert.Contains(t, view, "+673 -1.1k")
	assert.Contains(t, view, "+1.1k -19")
	assert.Contains(t, view, "+24 -20")
	assert.Contains(t, view, "+38 -38")
}

func TestModel_Stack_MicroRibbonSpacingRules(t *testing.T) {
	t.Parallel()

	// Short stack (1-6 PRs): 1-space padding
	shortPRs := make([]tui.PRItem, 4)
	for i := range shortPRs {
		shortPRs[i] = tui.PRItem{Status: tui.StatusNeedsReview}
	}
	assert.Equal(t, "● ● ● ●", tui.RenderMicroStatusRibbon(shortPRs))

	// Dense stack (7-10 PRs): no space padding
	densePRs := make([]tui.PRItem, 8)
	for i := range densePRs {
		densePRs[i] = tui.PRItem{Status: tui.StatusCIFailed}
	}
	assert.Equal(t, "✖✖✖✖✖✖✖✖", tui.RenderMicroStatusRibbon(densePRs))

	// Mega stack (>10 PRs): aggregate badge [ 4●  3✖  2◌  5○ ]
	megaPRs := make([]tui.PRItem, 14)
	for i := range 4 {
		megaPRs[i] = tui.PRItem{Status: tui.StatusNeedsReview}
	}
	for i := 4; i < 7; i++ {
		megaPRs[i] = tui.PRItem{Status: tui.StatusCIFailed}
	}
	for i := 7; i < 9; i++ {
		megaPRs[i] = tui.PRItem{Status: tui.StatusCIRunning}
	}
	for i := 9; i < 14; i++ {
		megaPRs[i] = tui.PRItem{Status: tui.StatusDraft}
	}
	badge := tui.RenderMicroStatusRibbon(megaPRs)
	assert.Contains(t, badge, "4●")
	assert.Contains(t, badge, "3✖")
	assert.Contains(t, badge, "2◌")
	assert.Contains(t, badge, "5○")
	assert.True(t, strings.HasPrefix(badge, "[ ") && strings.HasSuffix(badge, " ]"))
}

func TestModel_Stack_MicroCIRibbonSpacingRules(t *testing.T) {
	t.Parallel()

	// Short stack (1-6 PRs): 1-space padding
	shortPRs := make([]tui.PRItem, 4)
	shortPRs[0] = tui.PRItem{CIStatus: tui.CIStatusPassing}
	shortPRs[1] = tui.PRItem{CIStatus: tui.CIStatusFailed}
	shortPRs[2] = tui.PRItem{CIStatus: tui.CIStatusRunning}
	shortPRs[3] = tui.PRItem{CIStatus: tui.CIStatusNone}
	assert.Equal(t, "✓ ✖ ◌ ○", tui.RenderMicroCIRibbon(shortPRs))

	// Dense stack (7-10 PRs): no space padding
	densePRs := make([]tui.PRItem, 8)
	for i := range densePRs {
		densePRs[i] = tui.PRItem{CIStatus: tui.CIStatusFailed}
	}
	assert.Equal(t, "✖✖✖✖✖✖✖✖", tui.RenderMicroCIRibbon(densePRs))

	// Mega stack (>10 PRs): aggregate badge [ 4✓  3✖  2◌  5○ ]
	megaPRs := make([]tui.PRItem, 14)
	for i := range 4 {
		megaPRs[i] = tui.PRItem{CIStatus: tui.CIStatusPassing}
	}
	for i := 4; i < 7; i++ {
		megaPRs[i] = tui.PRItem{CIStatus: tui.CIStatusFailed}
	}
	for i := 7; i < 9; i++ {
		megaPRs[i] = tui.PRItem{CIStatus: tui.CIStatusRunning}
	}
	for i := 9; i < 14; i++ {
		megaPRs[i] = tui.PRItem{CIStatus: tui.CIStatusNone}
	}
	badge := tui.RenderMicroCIRibbon(megaPRs)
	assert.Contains(t, badge, "4✓")
	assert.Contains(t, badge, "3✖")
	assert.Contains(t, badge, "2◌")
	assert.Contains(t, badge, "5○")
	assert.True(t, strings.HasPrefix(badge, "[ ") && strings.HasSuffix(badge, " ]"))

	// No CI on any PR: returns empty string
	noCIPRs := make([]tui.PRItem, 3)
	for i := range noCIPRs {
		noCIPRs[i] = tui.PRItem{CIStatus: tui.CIStatusNone}
	}
	assert.Empty(t, tui.RenderMicroCIRibbon(noCIPRs))
}

func TestModel_Stack_InteractiveToggle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	prs := []model.PullRequest{
		{
			Number:            101,
			Title:             "Alpha base",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			Stack:             &model.PRStack{ID: "STACK_A", Number: 101, Size: 2, Position: 1},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
		{
			Number:            102,
			Title:             "Alpha child",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			Stack:             &model.PRStack{ID: "STACK_A", Number: 101, Size: 2, Position: 2},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
			},
		},
	}

	q := model.Queue{Inbox: prs}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)

	// Collapsed by default: shows collapsed stack row
	assert.Contains(t, m.View(), "[STACK] 2 PRs")
	assert.NotContains(t, m.View(), "[1..2]")
	assert.NotContains(t, m.View(), "Alpha child")

	// Press space to expand
	mExp, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	expView := mExp.(tui.Model).View()
	assert.Contains(t, expView, "[STACK] (2 PRs · #101..#102)")
	assert.Contains(t, expView, "├─ #101 [1/2]")
	assert.Contains(t, expView, "╰─ #102 [2/2]")
	assert.Contains(t, expView, "Alpha child")

	// Press space to collapse
	mCol, _ := mExp.(tui.Model).Update(tea.KeyMsg{Type: tea.KeySpace})
	colView := mCol.(tui.Model).View()
	assert.Contains(t, colView, "[STACK] 2 PRs")
	assert.NotContains(t, colView, "[1..2]")
	assert.NotContains(t, colView, "Alpha child")
}
