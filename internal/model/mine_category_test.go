package model_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
)

func TestPullRequest_MineCategory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		pr      model.PullRequest
		wantCat model.MineCategory
	}{
		{
			name: "stale pr by age",
			pr: model.PullRequest{
				Number:    1,
				UpdatedAt: now.Add(-31 * 24 * time.Hour),
			},
			wantCat: model.MineCategoryStale,
		},
		{
			name: "draft pr",
			pr: model.PullRequest{
				Number:    2,
				IsDraft:   true,
				UpdatedAt: now.Add(-2 * time.Hour),
			},
			wantCat: model.MineCategoryDraft,
		},
		{
			name: "changes requested",
			pr: model.PullRequest{
				Number:         3,
				ReviewDecision: "CHANGES_REQUESTED",
				UpdatedAt:      now.Add(-2 * time.Hour),
			},
			wantCat: model.MineCategoryActionRequired,
		},
		{
			name: "ci failing",
			pr: model.PullRequest{
				Number:    4,
				UpdatedAt: now.Add(-2 * time.Hour),
				Checks: model.ChecksSummary{
					HasRequiredChecks: true,
					ReqTotal:          2,
					ReqFailed:         1,
				},
			},
			wantCat: model.MineCategoryActionRequired,
		},
		{
			name: "conflict",
			pr: model.PullRequest{
				Number:    5,
				Mergeable: "CONFLICTING",
				UpdatedAt: now.Add(-2 * time.Hour),
			},
			wantCat: model.MineCategoryActionRequired,
		},
		{
			name: "queued pr by in merge queue flag",
			pr: model.PullRequest{
				Number:         60,
				IsInMergeQueue: true,
				UpdatedAt:      now.Add(-2 * time.Hour),
			},
			wantCat: model.MineCategoryQueued,
		},
		{
			name: "queued pr by merge state status",
			pr: model.PullRequest{
				Number:           61,
				MergeStateStatus: "QUEUED",
				UpdatedAt:        now.Add(-2 * time.Hour),
			},
			wantCat: model.MineCategoryQueued,
		},
		{
			name: "clean ready to merge",
			pr: model.PullRequest{
				Number:           6,
				Mergeable:        "MERGEABLE",
				MergeStateStatus: "CLEAN",
				ReviewDecision:   "APPROVED",
				UpdatedAt:        now.Add(-2 * time.Hour),
			},
			wantCat: model.MineCategoryReadyToMerge,
		},
		{
			name: "behind ready to merge with update",
			pr: model.PullRequest{
				Number:           7,
				Mergeable:        "MERGEABLE",
				MergeStateStatus: "BEHIND",
				ReviewDecision:   "APPROVED",
				UpdatedAt:        now.Add(-2 * time.Hour),
			},
			wantCat: model.MineCategoryReadyToMerge,
		},
		{
			name: "needs review",
			pr: model.PullRequest{
				Number:         8,
				ReviewDecision: "REVIEW_REQUIRED",
				UpdatedAt:      now.Add(-2 * time.Hour),
			},
			wantCat: model.MineCategoryInReview,
		},
		{
			name: "ci running",
			pr: model.PullRequest{
				Number:    9,
				UpdatedAt: now.Add(-2 * time.Hour),
				Checks: model.ChecksSummary{
					Total:   2,
					Running: 1,
				},
			},
			wantCat: model.MineCategoryInReview,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.pr.MineCategory(now)
			assert.Equal(t, tc.wantCat, got)
		})
	}
}
