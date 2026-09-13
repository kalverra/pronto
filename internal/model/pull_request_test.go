package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
)

func TestPullRequest_WaitHours(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		timelineItems []model.TimelineItem
		viewer        string
		teams         []string
		wantWaitHours float64
	}{
		{
			name: "direct user review requested 2 hours ago",
			timelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    now.Add(-2 * time.Hour),
					ReviewerUser: "kalverra",
				},
			},
			viewer:        "kalverra",
			teams:         []string{"core"},
			wantWaitHours: 2.0,
		},
		{
			name: "team review requested 4 hours ago",
			timelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    now.Add(-4 * time.Hour),
					ReviewerTeam: "core",
				},
			},
			viewer:        "kalverra",
			teams:         []string{"core"},
			wantWaitHours: 4.0,
		},
		{
			name: "review requested for another user ignored",
			timelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    now.Add(-10 * time.Hour),
					ReviewerUser: "alice",
				},
			},
			viewer:        "kalverra",
			teams:         []string{"core"},
			wantWaitHours: 0.0,
		},
		{
			name: "most recent review request used",
			timelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    now.Add(-6 * time.Hour),
					ReviewerTeam: "core",
				},
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    now.Add(-1 * time.Hour),
					ReviewerUser: "kalverra",
				},
			},
			viewer:        "kalverra",
			teams:         []string{"core"},
			wantWaitHours: 1.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pr := model.PullRequest{
				TimelineItems: tc.timelineItems,
			}
			got := pr.WaitHours(now, tc.viewer, tc.teams)
			assert.InDelta(t, tc.wantWaitHours, got, 0.01)
		})
	}
}

func TestPullRequest_AuthorIdle(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	viewer := "kalverra"
	teams := []string{"core"}

	tests := []struct {
		name          string
		timelineItems []model.TimelineItem
		commits       []model.Commit
		wantIdle      bool
	}{
		{
			name: "author pushed after viewer's review request => not idle",
			timelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    baseTime,
					ReviewerUser: viewer,
				},
			},
			commits: []model.Commit{
				{
					OID:           "abc1234",
					CommittedDate: baseTime.Add(1 * time.Hour),
				},
			},
			wantIdle: false,
		},
		{
			name: "author made no pushes after viewer's review request => idle",
			timelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    baseTime.Add(2 * time.Hour),
					ReviewerUser: viewer,
				},
			},
			commits: []model.Commit{
				{
					OID:           "abc1234",
					CommittedDate: baseTime,
				},
			},
			wantIdle: true,
		},
		{
			name: "team review request counts for idle",
			timelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    baseTime.Add(2 * time.Hour),
					ReviewerTeam: "core",
				},
			},
			commits: []model.Commit{
				{
					OID:           "abc1234",
					CommittedDate: baseTime,
				},
			},
			wantIdle: true,
		},
		{
			name: "request for another reviewer ignored",
			timelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    baseTime.Add(2 * time.Hour),
					ReviewerUser: "alice",
				},
			},
			commits: []model.Commit{
				{
					OID:           "abc1234",
					CommittedDate: baseTime,
				},
			},
			wantIdle: false,
		},
		{
			name: "push after another reviewer's later request does not reset viewer's clock",
			timelineItems: []model.TimelineItem{
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    baseTime,
					ReviewerUser: viewer,
				},
				{
					Type:         model.TimelineItemReviewRequested,
					CreatedAt:    baseTime.Add(1 * time.Hour),
					ReviewerUser: "alice",
				},
			},
			commits: []model.Commit{
				{
					OID:           "abc1234",
					CommittedDate: baseTime.Add(30 * time.Minute),
				},
			},
			wantIdle: false,
		},
		{
			name:          "no review request events",
			timelineItems: nil,
			commits: []model.Commit{
				{
					OID:           "abc1234",
					CommittedDate: baseTime,
				},
			},
			wantIdle: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pr := model.PullRequest{
				TimelineItems: tc.timelineItems,
				Commits:       tc.commits,
			}
			assert.Equal(t, tc.wantIdle, pr.AuthorIdle(viewer, teams))
		})
	}
}

func TestPullRequest_LastReviewAndCommitsSince(t *testing.T) {
	t.Parallel()

	reviewTime := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)

	pr := model.PullRequest{
		LatestReviews: []model.Review{
			{
				Author:      "kalverra",
				State:       "CHANGES_REQUESTED",
				CommitOID:   "commit-1",
				SubmittedAt: reviewTime,
			},
			{
				Author:      "alice",
				State:       "APPROVED",
				CommitOID:   "commit-2",
				SubmittedAt: reviewTime.Add(1 * time.Hour),
			},
		},
		Commits: []model.Commit{
			{
				OID:           "commit-1",
				CommittedDate: reviewTime.Add(-1 * time.Hour),
			},
			{
				OID:           "commit-2",
				CommittedDate: reviewTime.Add(30 * time.Minute),
			},
		},
	}

	t.Run("find review by author", func(t *testing.T) {
		t.Parallel()

		rev := pr.LastReviewBy("kalverra")
		require.NotNil(t, rev)
		assert.Equal(t, "commit-1", rev.CommitOID)
		assert.Equal(t, "CHANGES_REQUESTED", rev.State)

		missing := pr.LastReviewBy("bob")
		assert.Nil(t, missing)
	})

	t.Run("detect new commits since review", func(t *testing.T) {
		t.Parallel()

		// commit-2 committed at reviewTime + 30m, which is after kalverra's review
		assert.True(t, pr.HasNewCommitsSinceReview("kalverra"))

		// alice reviewed at reviewTime + 1h, no commits after that
		assert.False(t, pr.HasNewCommitsSinceReview("alice"))

		// bob never reviewed
		assert.False(t, pr.HasNewCommitsSinceReview("bob"))
	})
}

func TestPullRequest_IsAuthorBot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		author      string
		authorIsBot bool
		wantBot     bool
	}{
		{
			name:    "dependabot detected as bot",
			author:  "dependabot",
			wantBot: true,
		},
		{
			name:    "dependabot[bot] detected as bot",
			author:  "dependabot[bot]",
			wantBot: true,
		},
		{
			name:    "renovate detected as bot",
			author:  "renovate",
			wantBot: true,
		},
		{
			name:    "app-token-issuer-infra-renovate detected as bot",
			author:  "app-token-issuer-infra-renovate",
			wantBot: true,
		},
		{
			name:    "github-actions[bot] detected as bot",
			author:  "github-actions[bot]",
			wantBot: true,
		},
		{
			name:    "custom-bot with suffix detected as bot",
			author:  "my-service-bot",
			wantBot: true,
		},
		{
			name:        "explicit AuthorIsBot flag true",
			author:      "custom-service-account",
			authorIsBot: true,
			wantBot:     true,
		},
		{
			name:    "human kalverra is not a bot",
			author:  "kalverra",
			wantBot: false,
		},
		{
			name:    "human alice is not a bot",
			author:  "alice",
			wantBot: false,
		},
		{
			name:    "human harry-secure is not a bot",
			author:  "harry-secure",
			wantBot: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pr := model.PullRequest{
				Author:      tc.author,
				AuthorIsBot: tc.authorIsBot,
			}
			assert.Equal(t, tc.wantBot, pr.IsAuthorBot())
		})
	}
}

func TestPullRequest_IsStale(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		updatedAt time.Time
		isStale   bool
		wantStale bool
	}{
		{
			name:      "active PR updated 5 days ago",
			updatedAt: now.Add(-5 * 24 * time.Hour),
			wantStale: false,
		},
		{
			name:      "active PR updated 29 days ago",
			updatedAt: now.Add(-29 * 24 * time.Hour),
			wantStale: false,
		},
		{
			name:      "stale PR updated exactly 30 days ago",
			updatedAt: now.Add(-30 * 24 * time.Hour),
			wantStale: true,
		},
		{
			name:      "stale PR updated 45 days ago",
			updatedAt: now.Add(-45 * 24 * time.Hour),
			wantStale: true,
		},
		{
			name:      "explicit IsStale flag true with zero UpdatedAt",
			isStale:   true,
			wantStale: true,
		},
		{
			name:      "zero UpdatedAt and no flag is not stale",
			wantStale: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pr := model.PullRequest{
				UpdatedAt: tc.updatedAt,
				Stale:     tc.isStale,
			}
			assert.Equal(t, tc.wantStale, pr.IsStale(now))
		})
	}
}

func TestPullRequest_ActionStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		pr        model.PullRequest
		wantState model.ActionStatus
		wantBadge string
	}{
		{
			name: "draft PR",
			pr: model.PullRequest{
				IsDraft: true,
			},
			wantState: model.ActionStatusDraft,
			wantBadge: "DRAFT",
		},
		{
			name: "merge conflict via mergeable CONFLICTING",
			pr: model.PullRequest{
				Mergeable: "CONFLICTING",
			},
			wantState: model.ActionStatusConflict,
			wantBadge: "CONFLICT",
		},
		{
			name: "merge conflict via DIRTY mergeStateStatus",
			pr: model.PullRequest{
				MergeStateStatus: "DIRTY",
			},
			wantState: model.ActionStatusConflict,
			wantBadge: "CONFLICT",
		},
		{
			name: "conflict takes priority over failing CI",
			pr: model.PullRequest{
				Mergeable: "CONFLICTING",
				Checks: model.ChecksSummary{
					Failed: 1,
				},
			},
			wantState: model.ActionStatusConflict,
			wantBadge: "CONFLICT",
		},
		{
			name: "failing CI with required checks",
			pr: model.PullRequest{
				Checks: model.ChecksSummary{
					HasRequiredChecks: true,
					ReqFailed:         1,
				},
			},
			wantState: model.ActionStatusFailingCI,
			wantBadge: "FAILING CI",
		},
		{
			name: "failing CI fallback checks",
			pr: model.PullRequest{
				Checks: model.ChecksSummary{
					Failed: 2,
				},
			},
			wantState: model.ActionStatusFailingCI,
			wantBadge: "FAILING CI",
		},
		{
			name: "failing CI takes priority over review decision",
			pr: model.PullRequest{
				ReviewDecision: "CHANGES_REQUESTED",
				Checks: model.ChecksSummary{
					Failed: 1,
				},
			},
			wantState: model.ActionStatusFailingCI,
			wantBadge: "FAILING CI",
		},
		{
			name: "changes requested",
			pr: model.PullRequest{
				ReviewDecision: "CHANGES_REQUESTED",
			},
			wantState: model.ActionStatusChangesReq,
			wantBadge: "CHANGES REQ",
		},
		{
			name: "needs review",
			pr: model.PullRequest{
				ReviewDecision: "REVIEW_REQUIRED",
			},
			wantState: model.ActionStatusNeedsReview,
			wantBadge: "NEEDS REVIEW",
		},
		{
			name: "CI running with required checks",
			pr: model.PullRequest{
				Checks: model.ChecksSummary{
					HasRequiredChecks: true,
					ReqRunning:        1,
					ReqTotal:          2,
					ReqDone:           1,
				},
			},
			wantState: model.ActionStatusCIRunning,
			wantBadge: "CI RUNNING",
		},
		{
			name: "behind base branch",
			pr: model.PullRequest{
				MergeStateStatus: "BEHIND",
			},
			wantState: model.ActionStatusBehind,
			wantBadge: "BEHIND",
		},
		{
			name: "clean and ready",
			pr: model.PullRequest{
				Mergeable:        "MERGEABLE",
				MergeStateStatus: "CLEAN",
			},
			wantState: model.ActionStatusClean,
			wantBadge: "CLEAN",
		},
		{
			name: "clean via computed merge status",
			pr: model.PullRequest{
				MergeStatus: model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
			},
			wantState: model.ActionStatusClean,
			wantBadge: "CLEAN",
		},
		{
			name: "queued PR via mergeStateStatus",
			pr: model.PullRequest{
				MergeStateStatus: "QUEUED",
			},
			wantState: model.ActionStatusQueued,
			wantBadge: "QUEUED",
		},
		{
			name: "queued PR via IsInMergeQueue",
			pr: model.PullRequest{
				IsInMergeQueue: true,
			},
			wantState: model.ActionStatusQueued,
			wantBadge: "QUEUED",
		},
		{
			name: "generic blocked when checks and reviews unspecified",
			pr: model.PullRequest{
				MergeStateStatus: "BLOCKED",
			},
			wantState: model.ActionStatusBlocked,
			wantBadge: "BLOCKED",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.pr.ActionStatus()
			assert.Equal(t, tc.wantState, got)
			assert.Equal(t, tc.wantBadge, got.Badge())
		})
	}
}

func TestPullRequest_InboxCategory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		pr      model.PullRequest
		wantCat model.InboxCategory
	}{
		{
			name: "passing CI and needs review is attention",
			pr: model.PullRequest{
				UpdatedAt:      now.Add(-2 * time.Hour),
				ReviewDecision: "REVIEW_REQUIRED",
				Checks: model.ChecksSummary{
					Total: 2,
					Done:  2,
				},
			},
			wantCat: model.CategoryAttention,
		},
		{
			name: "clean PR is attention",
			pr: model.PullRequest{
				UpdatedAt:        now.Add(-2 * time.Hour),
				Mergeable:        "MERGEABLE",
				MergeStateStatus: "CLEAN",
			},
			wantCat: model.CategoryAttention,
		},
		{
			name: "failing CI is blocked",
			pr: model.PullRequest{
				UpdatedAt:      now.Add(-2 * time.Hour),
				ReviewDecision: "REVIEW_REQUIRED",
				Checks: model.ChecksSummary{
					Total:  2,
					Failed: 1,
				},
			},
			wantCat: model.CategoryBlocked,
		},
		{
			name: "running CI is blocked",
			pr: model.PullRequest{
				UpdatedAt:      now.Add(-2 * time.Hour),
				ReviewDecision: "REVIEW_REQUIRED",
				Checks: model.ChecksSummary{
					Total:   2,
					Running: 1,
				},
			},
			wantCat: model.CategoryBlocked,
		},
		{
			name: "conflict is blocked",
			pr: model.PullRequest{
				UpdatedAt: now.Add(-2 * time.Hour),
				Mergeable: "CONFLICTING",
			},
			wantCat: model.CategoryBlocked,
		},
		{
			name: "changes requested is blocked",
			pr: model.PullRequest{
				UpdatedAt:      now.Add(-2 * time.Hour),
				ReviewDecision: "CHANGES_REQUESTED",
			},
			wantCat: model.CategoryBlocked,
		},
		{
			name: "stale PR is stale even if clean",
			pr: model.PullRequest{
				UpdatedAt:        now.Add(-40 * 24 * time.Hour),
				Mergeable:        "MERGEABLE",
				MergeStateStatus: "CLEAN",
			},
			wantCat: model.CategoryStale,
		},
		{
			name: "stale PR is stale even if CI failing",
			pr: model.PullRequest{
				UpdatedAt: now.Add(-40 * 24 * time.Hour),
				Checks: model.ChecksSummary{
					Failed: 1,
				},
			},
			wantCat: model.CategoryStale,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.wantCat, tc.pr.InboxCategory(now))
		})
	}
}

func TestPullRequest_Stack(t *testing.T) {
	t.Parallel()

	t.Run("nil stack is not part of stack", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{}
		assert.False(t, pr.IsPartOfStack())
		assert.Empty(t, pr.StackKey())
	})

	t.Run("single item stack with size 1 is not part of multi-PR stack", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			RepoNameWithOwner: "org/repo",
			Stack: &model.PRStack{
				ID:       "PRS_1",
				Number:   1,
				Size:     1,
				Position: 1,
			},
		}
		assert.False(t, pr.IsPartOfStack())
	})

	t.Run("multi-PR stack", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			RepoNameWithOwner: "org/repo",
			Stack: &model.PRStack{
				ID:          "PRS_123",
				Number:      42,
				Size:        3,
				Position:    2,
				BaseRefName: "main",
			},
		}
		assert.True(t, pr.IsPartOfStack())
		assert.Equal(t, "org/repo#PRS_123", pr.StackKey())
	})

	t.Run("json roundtrip", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			Number:            101,
			RepoNameWithOwner: "org/repo",
			Stack: &model.PRStack{
				ID:          "PRS_999",
				Number:      5,
				Size:        2,
				Position:    1,
				BaseRefName: "develop",
			},
		}

		data, err := json.Marshal(pr)
		require.NoError(t, err)

		var decoded model.PullRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)
		require.NotNil(t, decoded.Stack)
		assert.Equal(t, "PRS_999", decoded.Stack.ID)
		assert.Equal(t, 5, decoded.Stack.Number)
		assert.Equal(t, 2, decoded.Stack.Size)
		assert.Equal(t, 1, decoded.Stack.Position)
		assert.Equal(t, "develop", decoded.Stack.BaseRefName)
	})

	t.Run("ref names json roundtrip and omitempty", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			Number:      101,
			HeadRefName: "feature-branch",
			HeadRefOID:  "abc1234",
			BaseRefName: "main",
		}

		data, err := json.Marshal(pr)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"head_ref_name":"feature-branch"`)
		assert.Contains(t, string(data), `"base_ref_name":"main"`)

		var decoded model.PullRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)
		assert.Equal(t, "feature-branch", decoded.HeadRefName)
		assert.Equal(t, "main", decoded.BaseRefName)

		emptyPR := model.PullRequest{Number: 102}
		emptyData, err := json.Marshal(emptyPR)
		require.NoError(t, err)
		assert.NotContains(t, string(emptyData), "head_ref_name")
		assert.NotContains(t, string(emptyData), "base_ref_name")
	})
}
