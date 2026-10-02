package tui

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/score"
)

var projRefTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// projPR builds a PR in acme/tool updated an hour before projRefTime.
func projPR(number int, mods ...func(*model.PullRequest)) model.PullRequest {
	pr := model.PullRequest{
		Number:            number,
		Title:             fmt.Sprintf("PR %d", number),
		RepoOwner:         "acme",
		RepoName:          "tool",
		RepoNameWithOwner: "acme/tool",
		UpdatedAt:         projRefTime.Add(-time.Hour),
	}
	for _, mod := range mods {
		mod(&pr)
	}
	return pr
}

func withRepo(name string) func(*model.PullRequest) {
	return func(pr *model.PullRequest) {
		pr.RepoName = name
		pr.RepoNameWithOwner = "acme/" + name
	}
}

func withAuthor(a string) func(*model.PullRequest) {
	return func(pr *model.PullRequest) { pr.Author = a }
}

func withUpdatedAgo(d time.Duration) func(*model.PullRequest) {
	return func(pr *model.PullRequest) { pr.UpdatedAt = projRefTime.Add(-d) }
}

// withStack places the PR at pos in two-PR stack id.
func withStack(id string, pos int) func(*model.PullRequest) {
	return func(pr *model.PullRequest) { pr.Stack = &model.PRStack{ID: id, Position: pos, Size: 2} }
}

var (
	draft            = func(pr *model.PullRequest) { pr.IsDraft = true }
	stale            = withUpdatedAgo(60 * 24 * time.Hour)
	changesReq       = func(pr *model.PullRequest) { pr.ReviewDecision = "CHANGES_REQUESTED" }
	inMergeQueue     = func(pr *model.PullRequest) { pr.IsInMergeQueue = true }
	approvedAndClean = func(pr *model.PullRequest) {
		pr.ReviewDecision = "APPROVED"
		pr.MergeStateStatus = "CLEAN"
	}
)

func scoredList(prs ...model.PullRequest) []score.Scored {
	out := make([]score.Scored, len(prs))
	for i, pr := range prs {
		out[i] = score.Scored{PR: pr}
	}
	return out
}

func focusedNumbers(nums ...int) func(model.PullRequest) bool {
	return func(pr model.PullRequest) bool { return slices.Contains(nums, pr.Number) }
}

// projSig renders each display row as a compact, comparable string.
func projSig(p tableProjection) []string {
	out := make([]string, 0, p.rowCount())
	for _, row := range p.rows() {
		switch row.kind {
		case rowDivider:
			out = append(out, fmt.Sprintf("== %s (%d)", row.dividerTitle, row.dividerCount))
		case rowStackBanner:
			out = append(out, fmt.Sprintf("banner #%d", p.items[row.itemIndex].PR.Number))
		case rowItem:
			s := fmt.Sprintf("#%d", p.items[row.itemIndex].PR.Number)
			if row.isCollapsedStack {
				s += " collapsed"
			}
			if row.isChildInStack {
				s += " child"
			}
			out = append(out, s)
		}
	}
	return out
}

func TestBuildProjection_Empty(t *testing.T) {
	t.Parallel()

	proj := buildProjection(projectionOptions{Tab: TabInbox})
	assert.Zero(t, proj.rowCount())
	assert.Empty(t, proj.visibleItemIndices())
	_, ok := proj.rowAt(0)
	assert.False(t, ok)
	_, ok = proj.collapsedStack(0)
	assert.False(t, ok)
	assert.Zero(t, proj.displayCursor(0))
}

func TestBuildProjection_SectionedTabs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts projectionOptions
		want []string
	}{
		{
			name: "inbox partitions focused first within each section",
			opts: projectionOptions{
				Tab: TabInbox,
				Items: scoredList(
					projPR(1),
					projPR(2, draft),
					projPR(3),
					projPR(4, stale),
				),
				IsFocused: focusedNumbers(3),
			},
			want: []string{
				"== NEEDS YOUR ATTENTION (2)", "#3", "#1",
				"== BLOCKED (1)", "#2",
				"== STALE (1)", "#4",
			},
		},
		{
			name: "mine lays out every authored section in order",
			opts: projectionOptions{
				Tab: TabMine,
				Items: scoredList(
					projPR(15, stale),
					projPR(14, draft),
					projPR(13),
					projPR(12, approvedAndClean),
					projPR(11, inMergeQueue),
					projPR(10, changesReq),
				),
			},
			want: []string{
				"== ACTION REQUIRED (1)", "#10",
				"== MERGE QUEUE (1)", "#11",
				"== READY TO MERGE (1)", "#12",
				"== IN REVIEW (1)", "#13",
				"== DRAFTS (1)", "#14",
				"== STALE (1)", "#15",
			},
		},
		{
			name: "focus classifies mine keys and viewer as authored",
			opts: projectionOptions{
				Tab: TabFocus,
				Items: scoredList(
					projPR(2, draft, withAuthor("carol")),
					projPR(20, withAuthor("ME")),
					projPR(11, inMergeQueue, withAuthor("me-alt")),
					projPR(1, withAuthor("carol")),
				),
				MineKeys: map[model.PRKey]bool{{Repo: "acme/tool", Number: 11}: true},
				Viewer:   "me",
			},
			want: []string{
				"== NEEDS YOUR ATTENTION (1)", "#1",
				"== MERGE QUEUE (1)", "#11",
				"== IN REVIEW (1)", "#20",
				"== BLOCKED (1)", "#2",
			},
		},
		{
			name: "focus without mine keys treats a queued PR as incoming",
			opts: projectionOptions{
				Tab:   TabFocus,
				Items: scoredList(projPR(11, inMergeQueue, withAuthor("me-alt"))),
			},
			want: []string{"== BLOCKED (1)", "#11"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.opts.RefTime = projRefTime
			assert.Equal(t, tt.want, projSig(buildProjection(tt.opts)))
		})
	}
}

func TestBuildProjection_SortStrategies(t *testing.T) {
	t.Parallel()

	items := scoredList(
		projPR(1, withRepo("tool"), withAuthor("alice"), withUpdatedAgo(time.Hour)),
		projPR(2, withRepo("api"), withAuthor("bob"), withUpdatedAgo(2*time.Hour)),
		projPR(3, withRepo("tool"), withUpdatedAgo(30*time.Minute)),
		projPR(4, withRepo("api"), withAuthor("Alice"), withUpdatedAgo(3*time.Hour)),
	)

	tests := []struct {
		sort SortStrategy
		want []string
	}{
		{SortRepo, []string{"== ACME/API (2)", "#4", "#2", "== ACME/TOOL (2)", "#1", "#3"}},
		{SortAuthor, []string{"== @ALICE (2)", "#4", "#1", "== @BOB (1)", "#2", "== UNKNOWN (1)", "#3"}},
		{SortScore, []string{"#4", "#1", "#2", "#3"}},
		{SortUpdated, []string{"#4", "#3", "#1", "#2"}},
	}
	for _, tt := range tests {
		t.Run(tt.sort.String(), func(t *testing.T) {
			t.Parallel()
			proj := buildProjection(projectionOptions{
				Tab:          TabInbox,
				Items:        items,
				SortStrategy: tt.sort,
				RefTime:      projRefTime,
				IsFocused:    focusedNumbers(4),
			})
			assert.Equal(t, tt.want, projSig(proj))
		})
	}
}

func TestBuildProjection_UpdatedSortTies(t *testing.T) {
	t.Parallel()

	t.Run("unrelated PRs with equal timestamps order by number", func(t *testing.T) {
		t.Parallel()
		proj := buildProjection(projectionOptions{
			Tab:          TabInbox,
			Items:        scoredList(projPR(5), projPR(3), projPR(4)),
			SortStrategy: SortUpdated,
		})
		assert.Equal(t, []string{"#3", "#4", "#5"}, projSig(proj))
	})

	t.Run("stack members are never split by an interleaving number", func(t *testing.T) {
		t.Parallel()
		// Stack A is #3 (position 1) and #1 (position 2); lone #2 falls between
		// them by number, which made the old comparator non-transitive.
		proj := buildProjection(projectionOptions{
			Tab: TabInbox,
			Items: scoredList(
				projPR(2),
				projPR(3, withStack("a", 1)),
				projPR(1, withStack("a", 2)),
			),
			SortStrategy: SortUpdated,
			IsExpanded:   func(string) bool { return true },
		})
		assert.Equal(t, []string{"banner #3", "#3 child", "#1 child", "#2"}, projSig(proj))
	})
}

func TestBuildProjection_CollapsedStack(t *testing.T) {
	t.Parallel()

	items := scoredList(
		projPR(10, withStack("s", 1)),
		projPR(11, withStack("s", 2)),
		projPR(5),
	)
	proj := buildProjection(projectionOptions{Tab: TabInbox, Items: items, RefTime: projRefTime})

	require.Equal(t, []string{"== NEEDS YOUR ATTENTION (3)", "#10 collapsed", "#5"}, projSig(proj))
	assert.Equal(t, []int{0, 2}, proj.visibleItemIndices(), "divider and hidden member are not selectable")

	sg, ok := proj.collapsedStack(0)
	require.True(t, ok)
	assert.Len(t, sg.PRs, 2)
	assert.False(t, sg.Expanded)
	_, ok = proj.collapsedStack(2)
	assert.False(t, ok, "lone PR is not a collapsed stack")

	assert.Equal(t, 1, proj.displayCursor(0))
	assert.Equal(t, 1, proj.displayCursor(1), "hidden member maps to its collapsed row")
	assert.Equal(t, 2, proj.displayCursor(2))
	assert.Equal(t, 0, proj.scrollAnchor(0), "top item anchors to the first divider")
	assert.Equal(t, 1, proj.scrollAnchor(1))
}

func TestBuildProjection_ExpandedStack(t *testing.T) {
	t.Parallel()

	items := scoredList(
		projPR(10, withStack("s", 1)),
		projPR(11, withStack("s", 2)),
		projPR(5),
	)
	proj := buildProjection(projectionOptions{
		Tab:        TabInbox,
		Items:      items,
		RefTime:    projRefTime,
		IsExpanded: func(string) bool { return true },
	})

	require.Equal(t, []string{
		"== NEEDS YOUR ATTENTION (3)", "banner #10", "#10 child", "#11 child", "#5",
	}, projSig(proj))
	banner, ok := proj.rowAt(1)
	require.True(t, ok)
	assert.Equal(t, 0, banner.itemIndex, "banner points at the stack root")
	require.NotNil(t, banner.stackGroup)
	assert.True(t, banner.stackGroup.Expanded)

	assert.Equal(t, []int{0, 1, 2}, proj.visibleItemIndices())
	assert.Equal(t, 2, proj.displayCursor(0), "cursor maps to the item row, not the banner")
	assert.Equal(t, 3, proj.displayCursor(1))
	_, ok = proj.collapsedStack(0)
	assert.False(t, ok)
}
