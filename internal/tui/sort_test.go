package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

func TestSortStrategy_Cycling(t *testing.T) {
	t.Parallel()

	assert.Equal(t, tui.SortRepo, tui.SortAction.Next(1))
	assert.Equal(t, tui.SortUpdated, tui.SortRepo.Next(1))
	assert.Equal(t, tui.SortScore, tui.SortUpdated.Next(1))
	assert.Equal(t, tui.SortAction, tui.SortScore.Next(1))

	assert.Equal(t, tui.SortScore, tui.SortAction.Next(-1))
	assert.Equal(t, tui.SortUpdated, tui.SortScore.Next(-1))
	assert.Equal(t, tui.SortRepo, tui.SortUpdated.Next(-1))
	assert.Equal(t, tui.SortAction, tui.SortRepo.Next(-1))

	assert.Equal(t, "action", tui.SortAction.String())
	assert.Equal(t, "repo", tui.SortRepo.String())
	assert.Equal(t, "updated", tui.SortUpdated.String())
	assert.Equal(t, "score", tui.SortScore.String())

	assert.Equal(t, "Action", tui.SortAction.Label())
	assert.Equal(t, "Repo", tui.SortRepo.Label())
	assert.Equal(t, "Updated", tui.SortUpdated.Label())
	assert.Equal(t, "Score", tui.SortScore.Label())
}

func TestModel_SortKeyToggle(t *testing.T) {
	t.Parallel()

	m := tui.New(makeTestQueue())
	assert.Equal(t, tui.SortAction, m.SortStrategy(), "default sort strategy must be SortAction")

	// Press 's' to cycle forward: Action -> Repo
	m1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = m1.(tui.Model)
	assert.Equal(t, tui.SortRepo, m.SortStrategy())

	// Press 's' to cycle forward: Repo -> Updated
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = m2.(tui.Model)
	assert.Equal(t, tui.SortUpdated, m.SortStrategy())

	// Press 's' to cycle forward: Updated -> Score
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = m3.(tui.Model)
	assert.Equal(t, tui.SortScore, m.SortStrategy())

	// Press 's' to cycle forward: Score -> Action
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = m4.(tui.Model)
	assert.Equal(t, tui.SortAction, m.SortStrategy())

	// Press 'S' to cycle backward: Action -> Score
	mRev, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = mRev.(tui.Model)
	assert.Equal(t, tui.SortScore, m.SortStrategy())

	// Press 'S' to cycle backward: Score -> Updated
	mRev2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = mRev2.(tui.Model)
	assert.Equal(t, tui.SortUpdated, m.SortStrategy())
}

func TestModel_SortByRepo(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:            101,
				Title:             "Alpha PR in repo-b",
				RepoOwner:         "org",
				RepoName:          "repo-b",
				RepoNameWithOwner: "org/repo-b",
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-2 * time.Hour),
						ReviewerUser: "viewer",
					},
				},
			},
			{
				Number:            102,
				Title:             "Beta PR in repo-a",
				RepoOwner:         "org",
				RepoName:          "repo-a",
				RepoNameWithOwner: "org/repo-a",
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-1 * time.Hour),
						ReviewerUser: "viewer",
					},
				},
			},
			{
				Number:            103,
				Title:             "Gamma PR in repo-a",
				RepoOwner:         "org",
				RepoName:          "repo-a",
				RepoNameWithOwner: "org/repo-a",
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-3 * time.Hour),
						ReviewerUser: "viewer",
					},
				},
			},
		},
	}

	m := tui.New(q, tui.WithNow(now), tui.WithViewer("viewer"), tui.WithDimensions(120, 40))
	mNav, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) // switch to Inbox tab
	m = mNav.(tui.Model)

	// In default SortAction mode:
	viewAction := m.View()
	assert.Contains(t, viewAction, "NEEDS YOUR ATTENTION")

	// Switch to SortRepo
	mRepo, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = mRepo.(tui.Model)
	require.Equal(t, tui.SortRepo, m.SortStrategy())

	viewRepo := m.View()

	// Repo headers should be present with uppercase names and counts
	assert.Contains(t, viewRepo, "ORG/REPO-A")
	assert.Contains(t, viewRepo, "ORG/REPO-B")
	assert.NotContains(t, viewRepo, "NEEDS YOUR ATTENTION")

	// org/repo-a comes before org/repo-b alphabetically
	idxA := strings.Index(viewRepo, "ORG/REPO-A")
	idxB := strings.Index(viewRepo, "ORG/REPO-B")
	assert.Less(t, idxA, idxB, "repo-a divider should appear before repo-b divider")

	// Under repo-a, both PR 102 and 103 appear before repo-b's PR 101
	idx102 := strings.Index(viewRepo, "#102")
	idx103 := strings.Index(viewRepo, "#103")
	idx101 := strings.Index(viewRepo, "#101")
	assert.Less(t, idx102, idxB, "PR 102 should appear under repo-a")
	assert.Less(t, idx103, idxB, "PR 103 should appear under repo-a")
	assert.Greater(t, idx101, idxB, "PR 101 should appear under repo-b")
}

func TestModel_SortByUpdated(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:            101,
				Title:             "Oldest update",
				RepoOwner:         "org",
				RepoName:          "repo",
				RepoNameWithOwner: "org/repo",
				UpdatedAt:         now.Add(-5 * time.Hour),
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-5 * time.Hour),
						ReviewerUser: "viewer",
					},
				},
			},
			{
				Number:            102,
				Title:             "Newest update",
				RepoOwner:         "org",
				RepoName:          "repo",
				RepoNameWithOwner: "org/repo",
				UpdatedAt:         now.Add(-10 * time.Minute),
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-10 * time.Minute),
						ReviewerUser: "viewer",
					},
				},
			},
			{
				Number:            103,
				Title:             "Middle update",
				RepoOwner:         "org",
				RepoName:          "repo",
				RepoNameWithOwner: "org/repo",
				UpdatedAt:         now.Add(-1 * time.Hour),
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-1 * time.Hour),
						ReviewerUser: "viewer",
					},
				},
			},
		},
	}

	m := tui.New(
		q,
		tui.WithNow(now),
		tui.WithViewer("viewer"),
		tui.WithDimensions(120, 40),
		tui.WithSortStrategy(tui.SortUpdated),
	)
	mNav, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = mNav.(tui.Model)

	view := m.View()
	idx102 := strings.Index(view, "#102")
	idx103 := strings.Index(view, "#103")
	idx101 := strings.Index(view, "#101")

	assert.Less(t, idx102, idx103, "PR 102 (newest) should appear before PR 103 (middle)")
	assert.Less(t, idx103, idx101, "PR 103 (middle) should appear before PR 101 (oldest)")
}

func TestModel_SortByScore(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := makeTestQueue() // PR 101 (clean), PR 102 (conflicting/blocked)

	m := tui.New(
		q,
		tui.WithNow(now),
		tui.WithViewer("viewer"),
		tui.WithDimensions(120, 40),
		tui.WithSortStrategy(tui.SortScore),
	)
	mNav, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = mNav.(tui.Model)

	view := m.View()
	// In pure score mode, category divider headers should not be shown
	assert.NotContains(t, view, "NEEDS YOUR ATTENTION")
	assert.NotContains(t, view, "BLOCKED")
	assert.Contains(t, view, "#101")
	assert.Contains(t, view, "#102")
}

func TestModel_SortMaintainsCursor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:            101,
				Title:             "PR One",
				RepoNameWithOwner: "org/repo-b",
				RepoName:          "repo-b",
				UpdatedAt:         now.Add(-2 * time.Hour),
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-2 * time.Hour),
						ReviewerUser: "viewer",
					},
				},
			},
			{
				Number:            102,
				Title:             "PR Two",
				RepoNameWithOwner: "org/repo-a",
				RepoName:          "repo-a",
				UpdatedAt:         now.Add(-1 * time.Hour),
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-1 * time.Hour),
						ReviewerUser: "viewer",
					},
				},
			},
		},
	}

	m := tui.New(q, tui.WithNow(now), tui.WithViewer("viewer"), tui.WithDimensions(120, 40))
	mNav, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = mNav.(tui.Model)

	// Move cursor down to PR 102
	mDown, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mDown.(tui.Model)
	require.NotNil(t, m.SelectedPR())
	selectedNum := m.SelectedPR().Number

	// Cycle through all sort strategies; the selected PR must stay identical
	for range 4 {
		mCycle, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
		m = mCycle.(tui.Model)
		require.NotNil(t, m.SelectedPR(), "selected PR must not be nil after sort toggle")
		assert.Equal(t, selectedNum, m.SelectedPR().Number, "selected PR must remain stable across sort toggles")
	}
}

func TestModel_TabBarRendersSortBadge(t *testing.T) {
	t.Parallel()

	m := tui.New(makeTestQueue(), tui.WithDimensions(120, 40))
	assert.Contains(t, m.View(), "sort: action")

	mRepo, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.Contains(t, mRepo.(tui.Model).View(), "sort: repo")

	mUpd, _ := mRepo.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.Contains(t, mUpd.(tui.Model).View(), "sort: updated")

	mScore, _ := mUpd.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.Contains(t, mScore.(tui.Model).View(), "sort: score")
}

func TestModel_HelpBarIncludesSort(t *testing.T) {
	t.Parallel()

	m := tui.New(makeTestQueue(), tui.WithDimensions(120, 40))
	assert.Contains(t, m.View(), "s: sort")
}

func TestModel_SortWithStacks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:            201,
				Title:             "Standalone outside stack",
				RepoNameWithOwner: "org/repo-b",
				RepoName:          "repo-b",
				UpdatedAt:         now.Add(-30 * time.Minute),
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-30 * time.Minute),
						ReviewerUser: "viewer",
					},
				},
			},
			{
				Number:            101,
				Title:             "Stack root PR",
				RepoNameWithOwner: "org/repo-a",
				RepoName:          "repo-a",
				UpdatedAt:         now.Add(-2 * time.Hour),
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				Stack:             &model.PRStack{ID: "STACK_1", Number: 101, Size: 2, Position: 1},
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-2 * time.Hour),
						ReviewerUser: "viewer",
					},
				},
			},
			{
				Number:            102,
				Title:             "Stack child PR",
				RepoNameWithOwner: "org/repo-a",
				RepoName:          "repo-a",
				UpdatedAt:         now.Add(-10 * time.Minute),
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				Stack:             &model.PRStack{ID: "STACK_1", Number: 101, Size: 2, Position: 2},
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-10 * time.Minute),
						ReviewerUser: "viewer",
					},
				},
			},
		},
	}

	m := tui.New(q, tui.WithNow(now), tui.WithViewer("viewer"), tui.WithDimensions(120, 40))
	mNav, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = mNav.(tui.Model)

	// Expand all stacks using 'E'
	mExp, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	m = mExp.(tui.Model)

	// Test in SortRepo: stack children appear together in order
	mRepo, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	viewRepo := mRepo.(tui.Model).View()
	assert.Contains(t, viewRepo, "ORG/REPO-A")
	assert.Contains(t, viewRepo, "#101 [1/2]")
	assert.Contains(t, viewRepo, "#102 [2/2]")

	// Test in SortUpdated: stack effective update places it before PR 201
	mUpd, _ := mRepo.(tui.Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	viewUpd := mUpd.(tui.Model).View()
	idx101 := strings.Index(viewUpd, "#101 [1/2]")
	idx102 := strings.Index(viewUpd, "#102 [2/2]")
	idx201 := strings.Index(viewUpd, "#201")
	assert.Less(t, idx101, idx102, "Stack position 1 must precede position 2")
	assert.Less(t, idx102, idx201, "Stack (updated 10m ago) must precede PR 201 (updated 30m ago)")
}
