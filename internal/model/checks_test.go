package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
)

func TestChecksSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		requiredContexts []string
		checks           []model.ContextCheck
		wantHasRequired  bool
		wantReqTotal     int
		wantReqRunning   int
		wantReqDone      int
		wantReqFailed    int
		wantTotal        int
		wantRunning      int
		wantDone         int
		wantFailed       int
		wantPassing      bool
		wantFailing      bool
		wantIsRunning    bool
		wantBadge        string
	}{
		{
			name:             "required checks all passed",
			requiredContexts: []string{"lint", "test"},
			checks: []model.ContextCheck{
				{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "optional", Status: "COMPLETED", Conclusion: "FAILURE"},
			},
			wantHasRequired: true,
			wantReqTotal:    2,
			wantReqDone:     2,
			wantPassing:     true,
			wantBadge:       "CI: ✓ 2/2",
		},
		{
			name:             "required check failed",
			requiredContexts: []string{"lint", "test"},
			checks: []model.ContextCheck{
				{Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE"},
				{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"},
			},
			wantHasRequired: true,
			wantReqTotal:    2,
			wantReqDone:     2,
			wantReqFailed:   1,
			wantFailing:     true,
			wantBadge:       "CI: ✓ 1  ✗ 1 req",
		},
		{
			name:             "required check running/in progress",
			requiredContexts: []string{"lint", "test"},
			checks: []model.ContextCheck{
				{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "test", Status: "IN_PROGRESS", Conclusion: ""},
			},
			wantHasRequired: true,
			wantReqTotal:    2,
			wantReqRunning:  1,
			wantReqDone:     1,
			wantIsRunning:   true,
			wantBadge:       "CI: 1/2 req (1 ⏳)",
		},
		{
			name:             "required legacy status context success",
			requiredContexts: []string{"ci/build"},
			checks: []model.ContextCheck{
				{Name: "ci/build", Status: "SUCCESS", Conclusion: ""},
			},
			wantHasRequired: true,
			wantReqTotal:    1,
			wantReqDone:     1,
			wantPassing:     true,
			wantBadge:       "CI: ✓ 1/1",
		},
		{
			name:             "required legacy status context failure",
			requiredContexts: []string{"ci/build"},
			checks: []model.ContextCheck{
				{Name: "ci/build", Status: "FAILURE", Conclusion: ""},
			},
			wantHasRequired: true,
			wantReqTotal:    1,
			wantReqDone:     1,
			wantReqFailed:   1,
			wantFailing:     true,
			wantBadge:       "CI: ✗ 1 req failed",
		},
		{
			name:             "required legacy status context error",
			requiredContexts: []string{"ci/build"},
			checks: []model.ContextCheck{
				{Name: "ci/build", Status: "ERROR", Conclusion: ""},
			},
			wantHasRequired: true,
			wantReqTotal:    1,
			wantReqDone:     1,
			wantReqFailed:   1,
			wantFailing:     true,
			wantBadge:       "CI: ✗ 1 req failed",
		},
		{
			name:             "required legacy status context expected does not count as running",
			requiredContexts: []string{"ci/build"},
			checks: []model.ContextCheck{
				{Name: "ci/build", Status: "EXPECTED", Conclusion: ""},
			},
			wantHasRequired: true,
			wantReqTotal:    1,
			wantReqRunning:  0,
			wantIsRunning:   false,
			wantBadge:       "CI: 0/1 req",
		},
		{
			name:             "required check not yet reported",
			requiredContexts: []string{"lint", "test"},
			checks: []model.ContextCheck{
				{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"},
			},
			wantHasRequired: true,
			wantReqTotal:    2,
			wantReqDone:     1,
			wantBadge:       "CI: 1/2 req",
		},
		{
			name: "fallback when no required checks configured - all pass",
			checks: []model.ContextCheck{
				{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"},
			},
			wantTotal:   2,
			wantDone:    2,
			wantPassing: true,
			wantBadge:   "CI: ✓ 2/2",
		},
		{
			name: "fallback when no required checks configured - check failed",
			checks: []model.ContextCheck{
				{Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE"},
				{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"},
			},
			wantTotal:   2,
			wantDone:    2,
			wantFailed:  1,
			wantFailing: true,
			wantBadge:   "CI: ✓ 1  ✗ 1",
		},
		{
			name: "fallback when no required checks configured - running",
			checks: []model.ContextCheck{
				{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "test", Status: "IN_PROGRESS", Conclusion: ""},
			},
			wantTotal:     2,
			wantDone:      1,
			wantRunning:   1,
			wantIsRunning: true,
			wantBadge:     "CI: 1/2 (1 ⏳)",
		},
		{
			name: "fallback legacy status context failure is not silent",
			checks: []model.ContextCheck{
				{Name: "ci/build", Status: "FAILURE", Conclusion: ""},
			},
			wantTotal:   1,
			wantDone:    1,
			wantFailed:  1,
			wantFailing: true,
			wantBadge:   "CI: ✗ 1 failed",
		},
		{
			name: "fallback legacy status context expected does not count as running",
			checks: []model.ContextCheck{
				{Name: "ci/build", Status: "EXPECTED", Conclusion: ""},
			},
			wantTotal:     1,
			wantRunning:   0,
			wantIsRunning: false,
			wantBadge:     "CI: 0/1",
		},
		{
			name: "pending status context does not count as running (like vault-audit)",
			checks: []model.ContextCheck{
				{Name: "vault-audit", Status: "PENDING", Conclusion: ""},
			},
			wantTotal:     1,
			wantRunning:   0,
			wantIsRunning: false,
			wantBadge:     "CI: 0/1",
		},
		{
			name:             "required check running with prior failure and success",
			requiredContexts: []string{"lint", "test", "build"},
			checks: []model.ContextCheck{
				{Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE"},
				{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "build", Status: "IN_PROGRESS", Conclusion: ""},
			},
			wantHasRequired: true,
			wantReqTotal:    3,
			wantReqDone:     2,
			wantReqFailed:   1,
			wantReqRunning:  1,
			wantFailing:     true,
			wantIsRunning:   true,
			wantBadge:       "CI: ✓ 1  ✗ 1 req (1 ⏳)",
		},
		{
			name: "fallback check running with prior failure and success",
			checks: []model.ContextCheck{
				{Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE"},
				{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Name: "build", Status: "IN_PROGRESS", Conclusion: ""},
			},
			wantTotal:     3,
			wantDone:      2,
			wantFailed:    1,
			wantRunning:   1,
			wantFailing:   true,
			wantIsRunning: true,
			wantBadge:     "CI: ✓ 1  ✗ 1 (1 ⏳)",
		},
		{
			name:      "empty checks",
			wantBadge: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			summary := model.ComputeChecksSummary(tc.requiredContexts, tc.checks)
			assert.Equal(t, tc.wantHasRequired, summary.HasRequiredChecks)
			assert.Equal(t, tc.wantReqTotal, summary.ReqTotal)
			assert.Equal(t, tc.wantReqRunning, summary.ReqRunning)
			assert.Equal(t, tc.wantReqDone, summary.ReqDone)
			assert.Equal(t, tc.wantReqFailed, summary.ReqFailed)
			assert.Equal(t, tc.wantTotal, summary.Total)
			assert.Equal(t, tc.wantRunning, summary.Running)
			assert.Equal(t, tc.wantDone, summary.Done)
			assert.Equal(t, tc.wantFailed, summary.Failed)
			assert.Equal(t, tc.wantPassing, summary.IsPassing())
			assert.Equal(t, tc.wantFailing, summary.IsFailing())
			assert.Equal(t, tc.wantIsRunning, summary.IsRunning())
			assert.Equal(t, tc.wantBadge, summary.Badge())
		})
	}
}

func TestChecksSummary_IsSettled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		summary model.ChecksSummary
		want    bool
	}{
		{
			name:    "empty checks is not settled",
			summary: model.ChecksSummary{},
			want:    false,
		},
		{
			name: "required mode all done is settled",
			summary: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          2,
				ReqDone:           2,
				ReqRunning:        0,
			},
			want: true,
		},
		{
			name: "required mode check running is not settled",
			summary: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          2,
				ReqDone:           1,
				ReqRunning:        1,
			},
			want: false,
		},
		{
			name: "required mode context missing from rollup is not settled even when not running",
			summary: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          2,
				ReqDone:           1,
				ReqRunning:        0,
			},
			want: false,
		},
		{
			name: "required mode zero total is not settled",
			summary: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          0,
			},
			want: false,
		},
		{
			name: "fallback mode all done is settled",
			summary: model.ChecksSummary{
				HasRequiredChecks: false,
				Total:             3,
				Done:              3,
				Running:           0,
			},
			want: true,
		},
		{
			name: "fallback mode running is not settled",
			summary: model.ChecksSummary{
				HasRequiredChecks: false,
				Total:             3,
				Done:              2,
				Running:           1,
			},
			want: false,
		},
		{
			name: "fallback mode zero total is not settled",
			summary: model.ChecksSummary{
				HasRequiredChecks: false,
				Total:             0,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.summary.IsSettled())
		})
	}
}

func TestChecksSummary_Succeeded(t *testing.T) {
	t.Parallel()

	// Required checks
	reqSummary := model.ChecksSummary{
		HasRequiredChecks: true,
		ReqTotal:          5,
		ReqDone:           4,
		ReqFailed:         1,
		ReqRunning:        1,
	}
	assert.Equal(t, 3, reqSummary.Succeeded())

	// Fallback checks
	fbSummary := model.ChecksSummary{
		Total:   100,
		Done:    81,
		Failed:  5,
		Running: 19,
	}
	assert.Equal(t, 76, fbSummary.Succeeded())
}

func TestChecksSummary_BadgeWithSpinner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		summary model.ChecksSummary
		spinner string
		want    string
	}{
		{
			name: "running with failures and successes",
			summary: model.ChecksSummary{
				Total:   100,
				Done:    81,
				Failed:  5,
				Running: 19,
			},
			spinner: "⠋",
			want:    "CI: ✓ 76  ✗ 5 (19 ⠋)",
		},
		{
			name: "running with no failures",
			summary: model.ChecksSummary{
				Total:   100,
				Done:    81,
				Failed:  0,
				Running: 19,
			},
			spinner: "⠋",
			want:    "CI: 81/100 (19 ⠋)",
		},
		{
			name: "done with failures and successes",
			summary: model.ChecksSummary{
				Total:   100,
				Done:    100,
				Failed:  22,
				Running: 0,
			},
			spinner: "⠋",
			want:    "CI: ✓ 78  ✗ 22",
		},
		{
			name: "done with failures only",
			summary: model.ChecksSummary{
				Total:   22,
				Done:    22,
				Failed:  22,
				Running: 0,
			},
			spinner: "⠋",
			want:    "CI: ✗ 22 failed",
		},
		{
			name: "done all passed",
			summary: model.ChecksSummary{
				Total:   100,
				Done:    100,
				Failed:  0,
				Running: 0,
			},
			spinner: "⠋",
			want:    "CI: ✓ 100/100",
		},
		{
			name: "required running with failures and successes",
			summary: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          3,
				ReqDone:           2,
				ReqFailed:         1,
				ReqRunning:        1,
			},
			spinner: "⠋",
			want:    "CI: ✓ 1  ✗ 1 req (1 ⠋)",
		},
		{
			name: "required done with failures and successes",
			summary: model.ChecksSummary{
				HasRequiredChecks: true,
				ReqTotal:          2,
				ReqDone:           2,
				ReqFailed:         1,
				ReqRunning:        0,
			},
			spinner: "⠋",
			want:    "CI: ✓ 1  ✗ 1 req",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.summary.BadgeWithSpinner(tc.spinner))
		})
	}
}

func TestComputeChecksSummary_RollupCountsAndState(t *testing.T) {
	t.Parallel()

	t.Run("state failure overrides truncated successful nodes", func(t *testing.T) {
		t.Parallel()
		// 100 checks in slice, all SUCCESS, but Rollup says FAILURE and 216 total.
		checks := make([]model.ContextCheck, 100)
		for i := range checks {
			checks[i] = model.ContextCheck{
				Name:       "test-shard",
				Status:     "COMPLETED",
				Conclusion: "SUCCESS",
			}
		}

		rollup := model.CheckRollup{
			State:      "FAILURE",
			TotalCount: 216,
			RunCounts: map[string]int{
				"FAILURE": 1,
				"SUCCESS": 200,
				"SKIPPED": 14,
			},
			StatusCounts: map[string]int{
				"SUCCESS": 1,
			},
		}

		summary := model.ComputeChecksSummaryWithRollup(nil, checks, rollup)
		assert.True(t, summary.IsFailing(), "rollup failure must mark summary failing even if nodes were truncated")
		assert.False(t, summary.IsPassing())
		assert.Equal(t, 216, summary.Total)
		assert.Equal(t, 1, summary.Failed)
		assert.Equal(t, 216, summary.Done)
		assert.Equal(t, 215, summary.Succeeded())
		assert.Equal(t, "CI: ✓ 215  ✗ 1", summary.Badge())
	})

	t.Run("PR with only pending status context is not running", func(t *testing.T) {
		t.Parallel()

		rollup := model.CheckRollup{
			State:      "FAILURE",
			TotalCount: 226,
			RunCounts: map[string]int{
				"FAILURE":   11,
				"CANCELLED": 33,
				"SUCCESS":   151,
				"SKIPPED":   30,
			},
			StatusCounts: map[string]int{
				"PENDING": 1, // vault-audit
			},
		}

		summary := model.ComputeChecksSummaryWithRollup(nil, nil, rollup)
		assert.False(t, summary.IsRunning(), "pending status context must not trigger running state")
		assert.Equal(t, 0, summary.Running)
		assert.True(t, summary.IsFailing())
		assert.Equal(t, 44, summary.Failed)
		assert.Equal(t, 181, summary.Succeeded())
		assert.Equal(t, "CI: ✓ 181  ✗ 44", summary.BadgeWithSpinner("⠋"))
	})
}
