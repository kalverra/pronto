package score_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/score"
)

func TestExplainMine(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	weights := score.DefaultMineWeights()

	t.Run("failing ci with recent update", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			Number:    101,
			UpdatedAt: now.Add(-2 * time.Hour),
			Checks: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          2,
				ReqFailed:         1,
			},
			Additions: 5,
			Deletions: 2,
		}

		b := score.ExplainMine(pr, now, weights)
		assert.Greater(t, b.Total, 480.0, "failing CI base (480) + recency + size")

		hasStatus := false
		hasRecency := false
		hasSize := false
		for _, term := range b.Terms {
			if term.Name == "Status:FAILING_CI" || term.Name == "ActionStatus" {
				hasStatus = true
				assert.InDelta(t, weights.BaseCIFailing, term.Contribution, 0.001)
			}
			if term.Name == "Recency" {
				hasRecency = true
				assert.Greater(t, term.Contribution, 0.0)
			}
			if term.Name == "Size" {
				hasSize = true
			}
		}
		assert.True(t, hasStatus, "must include status term")
		assert.True(t, hasRecency, "must include recency term")
		assert.True(t, hasSize, "must include size term")
	})

	t.Run("draft pr base score", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			Number:    102,
			IsDraft:   true,
			UpdatedAt: now.Add(-10 * time.Hour),
		}

		b := score.ExplainMine(pr, now, weights)
		assert.Less(t, b.Total, 200.0, "draft should have low score")
	})

	t.Run("queued pr status term and base score", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			Number:         103,
			IsInMergeQueue: true,
			UpdatedAt:      now.Add(-1 * time.Hour),
		}

		b := score.ExplainMine(pr, now, weights)
		hasQueuedStatus := false
		for _, term := range b.Terms {
			if term.Name == "Status:QUEUED" {
				hasQueuedStatus = true
				assert.InDelta(t, weights.BaseQueued, term.Contribution, 0.001)
			}
		}
		assert.True(t, hasQueuedStatus, "must include Status:QUEUED term")
	})
}

func TestRankMine_Order(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	weights := score.DefaultMineWeights()

	prs := []model.PullRequest{
		{
			Number:    1,
			Title:     "Draft PR",
			IsDraft:   true,
			UpdatedAt: now.Add(-1 * time.Hour),
		},
		{
			Number:         2,
			Title:          "Changes Requested PR",
			ReviewDecision: "CHANGES_REQUESTED",
			UpdatedAt:      now.Add(-5 * time.Hour),
		},
		{
			Number:    3,
			Title:     "Failing CI PR",
			UpdatedAt: now.Add(-2 * time.Hour),
			Checks: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          1,
				ReqFailed:         1,
			},
		},
		{
			Number:           4,
			Title:            "Clean PR ready to merge",
			Mergeable:        "MERGEABLE",
			MergeStateStatus: "CLEAN",
			ReviewDecision:   "APPROVED",
			UpdatedAt:        now.Add(-30 * time.Minute),
		},
		{
			Number:         5,
			Title:          "Needs Review PR",
			ReviewDecision: "REVIEW_REQUIRED",
			UpdatedAt:      now.Add(-3 * time.Hour),
		},
		{
			Number:    6,
			Title:     "Stale PR",
			UpdatedAt: now.Add(-40 * 24 * time.Hour),
		},
	}

	ranked := score.RankMine(prs, now, weights)
	require.Len(t, ranked, len(prs))

	// Expected order:
	// 1. Changes Requested (PR 2)
	// 2. Failing CI (PR 3)
	// 3. Clean (PR 4)
	// 4. Needs Review (PR 5)
	// 5. Draft (PR 1)
	// 6. Stale (PR 6)
	numbers := make([]int, len(ranked))
	for i, s := range ranked {
		numbers[i] = s.PR.Number
	}

	assert.Equal(t, []int{2, 3, 4, 5, 1, 6}, numbers)
}

func TestRankMine_RecencyWithinSameStatus(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	weights := score.DefaultMineWeights()

	prs := []model.PullRequest{
		{
			Number:    1,
			Title:     "Failing CI updated 2 days ago",
			UpdatedAt: now.Add(-48 * time.Hour),
			Checks: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          1,
				ReqFailed:         1,
			},
		},
		{
			Number:    2,
			Title:     "Failing CI updated 1 hour ago",
			UpdatedAt: now.Add(-1 * time.Hour),
			Checks: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          1,
				ReqFailed:         1,
			},
		},
	}

	ranked := score.RankMine(prs, now, weights)
	require.Len(t, ranked, 2)
	assert.Equal(t, 2, ranked[0].PR.Number, "more recent failing CI must rank first")
	assert.Equal(t, 1, ranked[1].PR.Number)
}

func TestRankMine_Stacks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	weights := score.DefaultMineWeights()

	prs := []model.PullRequest{
		// Standalone clean PR
		{
			Number:           100,
			Title:            "Clean standalone",
			Mergeable:        "MERGEABLE",
			MergeStateStatus: "CLEAN",
			ReviewDecision:   "APPROVED",
			UpdatedAt:        now.Add(-2 * time.Hour),
		},
		// Stack Alpha: Position 1 clean, Position 2 FAILING CI
		{
			Number:           101,
			Title:            "Stack Alpha 1/2 (Clean)",
			Mergeable:        "MERGEABLE",
			MergeStateStatus: "CLEAN",
			ReviewDecision:   "APPROVED",
			UpdatedAt:        now.Add(-4 * time.Hour),
			Stack: &model.PRStack{
				ID:       "alpha",
				Number:   101,
				Position: 1,
				Size:     2,
			},
		},
		{
			Number:    102,
			Title:     "Stack Alpha 2/2 (Failing CI)",
			UpdatedAt: now.Add(-4 * time.Hour),
			Checks: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          1,
				ReqFailed:         1,
			},
			Stack: &model.PRStack{
				ID:       "alpha",
				Number:   101,
				Position: 2,
				Size:     2,
			},
		},
		// Stack Beta: All drafts
		{
			Number:    201,
			Title:     "Stack Beta 1/2 (Draft)",
			IsDraft:   true,
			UpdatedAt: now.Add(-1 * time.Hour),
			Stack: &model.PRStack{
				ID:       "beta",
				Number:   201,
				Position: 1,
				Size:     2,
			},
		},
		{
			Number:    202,
			Title:     "Stack Beta 2/2 (Draft)",
			IsDraft:   true,
			UpdatedAt: now.Add(-1 * time.Hour),
			Stack: &model.PRStack{
				ID:       "beta",
				Number:   201,
				Position: 2,
				Size:     2,
			},
		},
	}

	ranked := score.RankMine(prs, now, weights)
	require.Len(t, ranked, 5)

	numbers := make([]int, len(ranked))
	for i, s := range ranked {
		numbers[i] = s.PR.Number
	}

	// Stack Alpha has a failing PR (102), so the entire stack should be elevated
	// above standalone clean (100).
	// Stack Alpha must remain contiguous: 101 then 102.
	// Stack Beta is all drafts, so it should be at the bottom (201 then 202).
	assert.Equal(t, []int{101, 102, 100, 201, 202}, numbers)
}
