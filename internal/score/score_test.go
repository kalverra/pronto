package score_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/score"
)

type mockExpertiseIndex struct {
	scores map[string]float64
}

func (m mockExpertiseIndex) Score(path string) float64 {
	if s, ok := m.scores[path]; ok {
		return s
	}
	return 0.0
}

func TestComputeSignals(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	viewer := "kalverra"
	reqTime := now.Add(-10 * time.Hour)

	pr := model.PullRequest{
		Number:           101,
		Title:            "Refactor core engine",
		IsDraft:          false,
		Additions:        150,
		Deletions:        50,
		ChangedFiles:     3,
		Mergeable:        "MERGEABLE",
		MergeStateStatus: "CLEAN",
		Files: []string{
			"core/engine.go",
			"core/engine_test.go",
			"go.mod",
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         "ReviewRequestedEvent",
				CreatedAt:    reqTime,
				ReviewerUser: viewer,
			},
		},
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          2,
			ReqDone:           2,
			ReqRunning:        0,
			ReqFailed:         0,
		},
		LatestReviews: []model.Review{
			{
				Author:      viewer,
				SubmittedAt: now.Add(-20 * time.Hour),
			},
		},
		Commits: []model.Commit{
			{
				OID:           "sha-review",
				CommittedDate: now.Add(-21 * time.Hour),
			},
			{
				OID:           "sha-new",
				CommittedDate: now.Add(-5 * time.Hour),
			},
		},
	}

	idx := mockExpertiseIndex{
		scores: map[string]float64{
			"core/engine.go":      0.8,
			"core/engine_test.go": 0.4,
		},
	}
	criticalGlobs := []string{"go.mod", ".github/**"}

	signals := score.ComputeSignals(pr, now, viewer, nil, idx, criticalGlobs)

	assert.InDelta(t, 10.0, signals.WaitHours, 0.01)
	assert.False(t, signals.AuthorIdle, "commit occurred after review request")
	assert.False(t, signals.IsDraft)
	assert.InDelta(t, 0.4, signals.ExpertiseRatio, 0.01) // (0.8 + 0.4 + 0.0) / 3
	assert.Equal(t, score.SizeM, signals.SizeBucket)     // 200 lines total
	assert.False(t, signals.CIFailing)
	assert.False(t, signals.CIRunning)
	assert.True(t, signals.IsMergeable)
	assert.False(t, signals.HasMergeConflict)
	assert.True(t, signals.TouchesCritical, "touches go.mod")
	assert.True(t, signals.HasNewCommits, "new commit after viewer's review")
}

func TestComputeSignals_SizeBuckets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		additions int
		deletions int
		expected  score.SizeBucket
	}{
		{"XS", 4, 3, score.SizeXS},
		{"S", 30, 10, score.SizeS},
		{"M", 100, 80, score.SizeM},
		{"L", 400, 350, score.SizeL},
		{"XL", 1200, 100, score.SizeXL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pr := model.PullRequest{
				Additions: tt.additions,
				Deletions: tt.deletions,
			}
			sig := score.ComputeSignals(pr, time.Now(), "user", nil, nil, nil)
			assert.Equal(t, tt.expected, sig.SizeBucket)
		})
	}
}

func TestComputeSignals_TeamRequestCounts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	teams := []string{"myorg/engineers", "otherorg/platform"}

	pr := model.PullRequest{
		Number: 301,
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-10 * time.Hour),
				ReviewerTeam: "myorg/engineers",
			},
		},
	}

	signals := score.ComputeSignals(pr, now, "kalverra", teams, nil, nil)

	assert.InDelta(t, 10.0, signals.WaitHours, 0.01, "team review request must count toward wait time")
	assert.True(t, signals.AuthorIdle, "no commits after team request means author idle")
}

func TestComputeSignals_TeamRequestNotViewer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	pr := model.PullRequest{
		Number: 302,
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-10 * time.Hour),
				ReviewerTeam: "myorg/some-other-team",
			},
		},
	}

	signals := score.ComputeSignals(pr, now, "kalverra", []string{"myorg/engineers"}, nil, nil)

	assert.InDelta(t, 0.0, signals.WaitHours, 0.01, "request for unrelated team must not count")
}

func TestExplain(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	viewer := "kalverra"
	w := score.DefaultWeights()

	pr := model.PullRequest{
		Number:           42,
		Mergeable:        "MERGEABLE",
		MergeStateStatus: "CLEAN",
		Additions:        5,
		Deletions:        2,
		TimelineItems: []model.TimelineItem{
			{
				Type:         "ReviewRequestedEvent",
				CreatedAt:    now.Add(-4 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}

	breakdown := score.Explain(pr, now, viewer, nil, nil, w)

	require.NotEmpty(t, breakdown.Terms)
	var sum float64
	for _, term := range breakdown.Terms {
		sum += term.Contribution
	}
	assert.InDelta(t, breakdown.Total, sum, 0.001)

	// AuthorIdle should be true (no commits after request)
	var foundAuthorIdle bool
	for _, term := range breakdown.Terms {
		if term.Name == "AuthorIdle" {
			foundAuthorIdle = true
			assert.InDelta(t, w.AuthorIdle, term.Contribution, 0.001)
		}
	}
	assert.True(t, foundAuthorIdle)
}

func TestRank(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	viewer := "kalverra"
	w := score.DefaultWeights()

	prDraft := model.PullRequest{
		Number:  1,
		Title:   "Draft work",
		IsDraft: true,
	}

	prLowPriority := model.PullRequest{
		Number:           2,
		Title:            "Failing conflict PR",
		Mergeable:        "CONFLICTING",
		MergeStateStatus: "DIRTY",
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqFailed:         2,
		},
	}

	prHighPriority := model.PullRequest{
		Number:           3,
		Title:            "Urgent clean PR waiting 48h",
		Mergeable:        "MERGEABLE",
		MergeStateStatus: "CLEAN",
		TimelineItems: []model.TimelineItem{
			{
				Type:         "ReviewRequestedEvent",
				CreatedAt:    now.Add(-48 * time.Hour),
				ReviewerUser: viewer,
			},
		},
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          3,
			ReqDone:           3,
		},
	}

	ranked := score.Rank([]model.PullRequest{prDraft, prLowPriority, prHighPriority}, now, viewer, nil, nil, w)

	require.Len(t, ranked, 2, "draft PR must be filtered out of ranking")
	assert.Equal(t, 3, ranked[0].PR.Number, "high priority PR should rank first")
	assert.Equal(t, 2, ranked[1].PR.Number, "low priority PR should rank second")
	assert.Greater(t, ranked[0].Score, ranked[1].Score)
}

func TestSizeBucket_String(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "XS", score.SizeXS.String())
	assert.Equal(t, "S", score.SizeS.String())
	assert.Equal(t, "M", score.SizeM.String())
	assert.Equal(t, "L", score.SizeL.String())
	assert.Equal(t, "XL", score.SizeXL.String())
}

func TestDiffSizeBucket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		additions int
		deletions int
		want      score.SizeBucket
	}{
		{"XS 7 lines", 4, 3, score.SizeXS},
		{"S 40 lines", 30, 10, score.SizeS},
		{"M 250 lines", 200, 50, score.SizeM},
		{"L 800 lines", 600, 200, score.SizeL},
		{"XL 1500 lines", 1000, 500, score.SizeXL},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, score.DiffSizeBucket(tc.additions, tc.deletions))
		})
	}
}

func TestRank_PartitionsActiveBeforeStale(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	viewer := "kalverra"
	w := score.DefaultWeights()

	// Active PR: updated 2 days ago, low wait time -> low score
	activePR := model.PullRequest{
		Number:    10,
		Title:     "Active low score PR",
		UpdatedAt: now.Add(-2 * 24 * time.Hour),
		TimelineItems: []model.TimelineItem{
			{
				Type:         "ReviewRequestedEvent",
				CreatedAt:    now.Add(-2 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}

	// Stale PR: updated 40 days ago, waiting 40 days -> huge score from WaitHours
	stalePR := model.PullRequest{
		Number:    20,
		Title:     "Stale ancient high score PR",
		UpdatedAt: now.Add(-40 * 24 * time.Hour),
		TimelineItems: []model.TimelineItem{
			{
				Type:         "ReviewRequestedEvent",
				CreatedAt:    now.Add(-40 * 24 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}

	ranked := score.Rank([]model.PullRequest{stalePR, activePR}, now, viewer, nil, nil, w)
	require.Len(t, ranked, 2)

	// Stale PR has a much higher raw score due to 40 days wait
	assert.Greater(t, ranked[1].Score, ranked[0].Score, "stale PR has higher wait score")
	// But active PR must be ranked first (partitioned before stale)
	assert.Equal(t, 10, ranked[0].PR.Number, "active PR must rank before stale PR")
	assert.Equal(t, 20, ranked[1].PR.Number, "stale PR must rank after active PR")
}

func TestRank_PartitionsInboxCategories(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	viewer := "kalverra"
	w := score.DefaultWeights()

	// Attention PR: low wait time -> low score, but needs review with passing CI
	attentionPR := model.PullRequest{
		Number:         10,
		Title:          "Attention PR",
		UpdatedAt:      now.Add(-2 * time.Hour),
		ReviewDecision: "REVIEW_REQUIRED",
		Checks: model.ChecksSummary{
			Total: 2,
			Done:  2,
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         "ReviewRequestedEvent",
				CreatedAt:    now.Add(-1 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}

	// Blocked PR: failing CI, waiting 10 days -> high score
	blockedPR := model.PullRequest{
		Number:         20,
		Title:          "Blocked PR",
		UpdatedAt:      now.Add(-10 * 24 * time.Hour),
		ReviewDecision: "REVIEW_REQUIRED",
		Checks: model.ChecksSummary{
			Total:  2,
			Failed: 1,
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         "ReviewRequestedEvent",
				CreatedAt:    now.Add(-10 * 24 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}

	// Stale PR: waiting 40 days -> massive score
	stalePR := model.PullRequest{
		Number:    30,
		Title:     "Stale PR",
		UpdatedAt: now.Add(-40 * 24 * time.Hour),
		TimelineItems: []model.TimelineItem{
			{
				Type:         "ReviewRequestedEvent",
				CreatedAt:    now.Add(-40 * 24 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}

	ranked := score.Rank([]model.PullRequest{stalePR, blockedPR, attentionPR}, now, viewer, nil, nil, w)
	require.Len(t, ranked, 3)

	assert.Equal(t, 10, ranked[0].PR.Number, "attention PR must rank first")
	assert.Equal(t, 20, ranked[1].PR.Number, "blocked PR must rank second")
	assert.Equal(t, 30, ranked[2].PR.Number, "stale PR must rank third")
}
