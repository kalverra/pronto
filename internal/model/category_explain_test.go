package model_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
)

func TestExplainMineCategory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Hour)

	t.Run("stale", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt: now.Add(-31 * 24 * time.Hour),
		}
		assert.Equal(t, model.MineCategoryStale, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "Inactive")
	})

	t.Run("draft", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt: fresh,
			IsDraft:   true,
		}
		assert.Equal(t, model.MineCategoryDraft, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "Draft")
	})

	t.Run("action required - changes requested", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt:      fresh,
			ReviewDecision: "CHANGES_REQUESTED",
		}
		assert.Equal(t, model.MineCategoryActionRequired, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "Changes requested")
	})

	t.Run("action required - failing checks", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt: fresh,
			Checks:    model.ChecksSummary{HasRequiredChecks: true, ReqFailed: 1},
		}
		assert.Equal(t, model.MineCategoryActionRequired, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "Failing CI checks")
	})

	t.Run("action required - conflicts", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt: fresh,
			Mergeable: "CONFLICTING",
		}
		assert.Equal(t, model.MineCategoryActionRequired, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "Merge conflict")
	})

	t.Run("in merge queue", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt:      fresh,
			IsInMergeQueue: true,
		}
		assert.Equal(t, model.MineCategoryQueued, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "merge queue")
	})

	t.Run("in merge queue with failing checks", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt:      fresh,
			IsInMergeQueue: true,
			Checks: model.ChecksSummary{
				Total:  235,
				Failed: 1,
			},
		}
		assert.Equal(t, model.MineCategoryQueued, pr.MineCategory(now))
		assert.Equal(t, "PR is in merge queue", pr.ExplainMineCategory(now))
	})

	t.Run("ready to merge - clean", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt:        fresh,
			MergeStateStatus: "CLEAN",
		}
		assert.Equal(t, model.MineCategoryReadyToMerge, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "Approved and clean")
	})

	t.Run("ready to merge - behind", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt:        fresh,
			MergeStateStatus: "BEHIND",
		}
		assert.Equal(t, model.MineCategoryReadyToMerge, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "Approved and behind")
	})

	t.Run("in review - awaiting review", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt:      fresh,
			ReviewDecision: "REVIEW_REQUIRED",
		}
		assert.Equal(t, model.MineCategoryInReview, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "Waiting on review")
	})

	t.Run("in review - checks running", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt: fresh,
			Checks:    model.ChecksSummary{HasRequiredChecks: true, ReqRunning: 1},
		}
		assert.Equal(t, model.MineCategoryInReview, pr.MineCategory(now))
		assert.Contains(t, pr.ExplainMineCategory(now), "CI checks running")
	})
}

func TestExplainInboxCategory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Hour)

	t.Run("stale", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt: now.Add(-31 * 24 * time.Hour),
		}
		assert.Equal(t, model.CategoryStale, pr.InboxCategory(now))
		assert.Contains(t, pr.ExplainInboxCategory(now), "Inactive")
	})

	t.Run("blocked - draft", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt: fresh,
			IsDraft:   true,
		}
		assert.Equal(t, model.CategoryBlocked, pr.InboxCategory(now))
		assert.Contains(t, pr.ExplainInboxCategory(now), "Draft")
	})

	t.Run("blocked - failing checks", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt: fresh,
			Checks:    model.ChecksSummary{HasRequiredChecks: true, ReqFailed: 1},
		}
		assert.Equal(t, model.CategoryBlocked, pr.InboxCategory(now))
		assert.Contains(t, pr.ExplainInboxCategory(now), "Failing CI checks")
	})

	t.Run("blocked - conflicts", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt: fresh,
			Mergeable: "CONFLICTING",
		}
		assert.Equal(t, model.CategoryBlocked, pr.InboxCategory(now))
		assert.Contains(t, pr.ExplainInboxCategory(now), "Merge conflict")
	})

	t.Run("attention - clean", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt:        fresh,
			MergeStateStatus: "CLEAN",
		}
		assert.Equal(t, model.CategoryAttention, pr.InboxCategory(now))
		assert.Contains(t, pr.ExplainInboxCategory(now), "Ready for review")
	})

	t.Run("attention - review required", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			UpdatedAt:      fresh,
			ReviewDecision: "REVIEW_REQUIRED",
		}
		assert.Equal(t, model.CategoryAttention, pr.InboxCategory(now))
		assert.Contains(t, pr.ExplainInboxCategory(now), "Ready for review")
	})
}

func TestEffectiveSectionDetails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Hour)
	stack := &model.PRStack{ID: "S1", Size: 2}

	pr1 := model.PullRequest{
		Number:            1,
		RepoNameWithOwner: "o/r",
		Author:            "alice",
		UpdatedAt:         fresh,
		Stack:             stack,
		Checks:            model.ChecksSummary{HasRequiredChecks: true, ReqFailed: 1},
	}
	pr2 := model.PullRequest{
		Number:            2,
		RepoNameWithOwner: "o/r",
		Author:            "alice",
		UpdatedAt:         fresh,
		Stack:             stack,
		ReviewDecision:    "REVIEW_REQUIRED",
	}

	authored := func(p model.PullRequest) bool { return p.Author == "alice" }
	details := model.EffectiveSectionDetails([]model.PullRequest{pr1, pr2}, authored, now)

	assert.Equal(t, model.SectionActionRequired, details[pr1.Key()].Section)
	assert.Contains(t, details[pr1.Key()].Reason, "Failing CI checks")

	assert.Equal(t, model.SectionActionRequired, details[pr2.Key()].Section)
	assert.Contains(t, details[pr2.Key()].Reason, "Inherited from stack entry #1")
}
