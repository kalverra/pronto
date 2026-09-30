package model_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
)

func TestCategorySections(t *testing.T) {
	t.Parallel()

	assert.Equal(t, model.SectionActionRequired, model.MineCategoryActionRequired.Section())
	assert.Equal(t, model.SectionMergeQueue, model.MineCategoryQueued.Section())
	assert.Equal(t, model.SectionReadyToMerge, model.MineCategoryReadyToMerge.Section())
	assert.Equal(t, model.SectionInReview, model.MineCategoryInReview.Section())
	assert.Equal(t, model.SectionDrafts, model.MineCategoryDraft.Section())
	assert.Equal(t, model.SectionStale, model.MineCategoryStale.Section())
	assert.Equal(t, model.SectionAttention, model.CategoryAttention.Section())
	assert.Equal(t, model.SectionBlocked, model.CategoryBlocked.Section())
	assert.Equal(t, model.SectionStale, model.CategoryStale.Section())
}

func TestEffectiveSections(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Hour)
	stack := &model.PRStack{ID: "S1", Size: 3}
	failing := model.ChecksSummary{HasRequiredChecks: true, ReqTotal: 1, ReqFailed: 1}

	mk := func(n int, mut func(*model.PullRequest)) model.PullRequest {
		pr := model.PullRequest{Number: n, RepoNameWithOwner: "o/r", UpdatedAt: fresh}
		mut(&pr)
		return pr
	}
	authored := func(pr model.PullRequest) bool { return pr.Author == "me" }

	t.Run("standalone", func(t *testing.T) {
		t.Parallel()
		prs := []model.PullRequest{
			mk(1, func(p *model.PullRequest) { p.Author = "me"; p.IsDraft = true }),
			mk(2, func(p *model.PullRequest) { p.Author = "them"; p.Checks = failing }),
		}
		got := model.EffectiveSections(prs, authored, now)
		assert.Equal(t, model.SectionDrafts, got[prs[0].Key()])
		assert.Equal(t, model.SectionBlocked, got[prs[1].Key()])
	})

	t.Run("stack members inherit most urgent category", func(t *testing.T) {
		t.Parallel()
		prs := []model.PullRequest{
			mk(1, func(p *model.PullRequest) { p.Author = "me"; p.Stack = stack; p.Checks = failing }),
			mk(
				2,
				func(p *model.PullRequest) { p.Author = "me"; p.Stack = stack; p.ReviewDecision = "REVIEW_REQUIRED" },
			),
			mk(3, func(p *model.PullRequest) { p.Author = "x"; p.Stack = stack }),
			mk(4, func(p *model.PullRequest) { p.Author = "x"; p.Stack = stack; p.Checks = failing }),
		}
		got := model.EffectiveSections(prs, authored, now)
		assert.Equal(t, model.SectionActionRequired, got[prs[0].Key()])
		assert.Equal(t, model.SectionActionRequired, got[prs[1].Key()])
		// Inbox family is independent of authored: attention beats blocked.
		assert.Equal(t, model.SectionAttention, got[prs[2].Key()])
		assert.Equal(t, model.SectionAttention, got[prs[3].Key()])
	})

	t.Run("authored merge queue member keeps own category", func(t *testing.T) {
		t.Parallel()
		prs := []model.PullRequest{
			mk(1, func(p *model.PullRequest) { p.Author = "me"; p.Stack = stack; p.Checks = failing }),
			mk(2, func(p *model.PullRequest) {
				p.Author = "me"
				p.Stack = stack
				p.IsInMergeQueue = true
				p.ReviewDecision = "APPROVED"
			}),
		}
		got := model.EffectiveSections(prs, authored, now)
		assert.Equal(t, model.SectionActionRequired, got[prs[0].Key()])
		assert.Equal(t, model.SectionMergeQueue, got[prs[1].Key()])
	})
}
