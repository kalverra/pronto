// Package model defines core domain entities for pronto review queues.
package model

import (
	"fmt"
	"slices"
)

// CheckState represents the status of a check run or context.
type CheckState string

// ContextCheck represents an individual check run or status context.
type ContextCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// CheckRollup captures rollup-level metadata from GitHub statusCheckRollup.
type CheckRollup struct {
	State        string         `json:"state"`
	TotalCount   int            `json:"total_count"`
	RunCounts    map[string]int `json:"run_counts"`
	StatusCounts map[string]int `json:"status_counts"`
}

// ChecksSummary summarizes CI check status for a pull request.
type ChecksSummary struct {
	State             string `json:"state,omitempty"`
	HasRequiredChecks bool   `json:"has_required_checks"`

	ReqTotal   int `json:"req_total"`
	ReqRunning int `json:"req_running"`
	ReqDone    int `json:"req_done"`
	ReqFailed  int `json:"req_failed"`

	Total   int `json:"total"`
	Running int `json:"running"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
}

// Succeeded returns the number of completed checks that succeeded.
func (c ChecksSummary) Succeeded() int {
	if c.HasRequiredChecks {
		return max(0, c.ReqDone-c.ReqFailed)
	}
	return max(0, c.Done-c.Failed)
}

// ComputeChecksSummary computes the check summary from branch protection required contexts
// and status check rollup contexts. When required contexts are configured, only those are
// counted; otherwise all rollup contexts are aggregated.
func ComputeChecksSummary(requiredContexts []string, checks []ContextCheck) ChecksSummary {
	return ComputeChecksSummaryWithRollup(requiredContexts, checks, CheckRollup{})
}

// ComputeChecksSummaryWithRollup computes the check summary incorporating rollup-level
// statusCheckRollup metadata from GitHub.
func ComputeChecksSummaryWithRollup(
	requiredContexts []string,
	checks []ContextCheck,
	rollup CheckRollup,
) ChecksSummary {
	if len(requiredContexts) > 0 {
		return computeRequiredChecksSummary(requiredContexts, checks, rollup.State)
	}
	if rollup.TotalCount > 0 || len(rollup.RunCounts) > 0 || len(rollup.StatusCounts) > 0 {
		return aggregateRollupCounts(rollup)
	}
	return computeFallbackChecksSummary(checks, rollup.State)
}

func computeRequiredChecksSummary(requiredContexts []string, checks []ContextCheck, state string) ChecksSummary {
	summary := ChecksSummary{
		State:             state,
		HasRequiredChecks: true,
		ReqTotal:          len(requiredContexts),
	}

	for _, req := range requiredContexts {
		idx := slices.IndexFunc(checks, func(c ContextCheck) bool {
			return c.Name == req
		})
		if idx == -1 {
			continue
		}
		done, failed, running := classifyCheck(checks[idx])
		if done {
			summary.ReqDone++
		}
		if failed {
			summary.ReqFailed++
		}
		if running {
			summary.ReqRunning++
		}
	}

	return summary
}

func aggregateRollupCounts(rollup CheckRollup) ChecksSummary {
	var failed, running, success, skipped, pending int
	for state, count := range rollup.RunCounts {
		switch state {
		case "FAILURE", "ERROR", "TIMED_OUT", "STARTUP_FAILURE", "CANCELLED", "ACTION_REQUIRED":
			failed += count
		case "IN_PROGRESS", "QUEUED", "WAITING":
			running += count
		case "SUCCESS":
			success += count
		case "SKIPPED", "NEUTRAL":
			skipped += count
		}
	}
	for state, count := range rollup.StatusCounts {
		switch state {
		case "FAILURE", "ERROR":
			failed += count
		case "SUCCESS":
			success += count
		case "PENDING", "EXPECTED":
			pending += count
		}
	}

	total := rollup.TotalCount
	if total == 0 {
		total = failed + running + success + skipped + pending
	}

	done := total - running - pending
	if done < 0 {
		done = success + failed + skipped
	}

	if (rollup.State == "FAILURE" || rollup.State == "ERROR") && failed == 0 {
		failed = 1
		if done < failed {
			done = failed
		}
		if total < done {
			total = done
		}
	}

	return ChecksSummary{
		State:   rollup.State,
		Total:   total,
		Running: running,
		Done:    done,
		Failed:  failed,
	}
}

func computeFallbackChecksSummary(checks []ContextCheck, state string) ChecksSummary {
	if len(checks) == 0 {
		if state != "" {
			return ChecksSummary{State: state}
		}
		return ChecksSummary{}
	}

	summary := ChecksSummary{
		State: state,
		Total: len(checks),
	}

	for _, c := range checks {
		done, failed, running := classifyCheck(c)
		if done {
			summary.Done++
		}
		if failed {
			summary.Failed++
		}
		if running {
			summary.Running++
		}
	}

	if (state == "FAILURE" || state == "ERROR") && summary.Failed == 0 {
		summary.Failed = 1
	}

	return summary
}

// classifyCheck classifies a single check run or status context.
// done means finished (whatever the outcome), failed means finished badly,
// running means not finished yet. Covers both wire shapes: CheckRun
// (status IN_PROGRESS/QUEUED/COMPLETED + conclusion) and legacy StatusContext
// (state EXPECTED/SUCCESS/FAILURE/ERROR, no conclusion).
func classifyCheck(c ContextCheck) (done, failed, running bool) {
	switch c.Status {
	case "IN_PROGRESS", "QUEUED":
		return false, false, true
	case "PENDING", "EXPECTED":
		return false, false, false
	case "COMPLETED":
		return true, isCheckFailed(c.Conclusion), false
	case "SUCCESS":
		return true, false, false
	case "FAILURE", "ERROR":
		return true, true, false
	default:
		// Unknown status: fall back to conclusion if present, otherwise unclassifiable.
		if c.Conclusion != "" {
			return true, isCheckFailed(c.Conclusion), false
		}
		return false, false, false
	}
}

// IsPassing reports whether all required checks (or fallback checks) passed.
func (c ChecksSummary) IsPassing() bool {
	if c.HasRequiredChecks {
		return c.ReqTotal > 0 && c.ReqDone == c.ReqTotal && c.ReqFailed == 0
	}
	if c.State == "FAILURE" || c.State == "ERROR" {
		return false
	}
	return c.Total > 0 && c.Done == c.Total && c.Failed == 0
}

// IsFailing reports whether any required check (or fallback check) failed.
func (c ChecksSummary) IsFailing() bool {
	if c.HasRequiredChecks {
		return c.ReqFailed > 0
	}
	if c.State == "FAILURE" || c.State == "ERROR" {
		return true
	}
	return c.Failed > 0
}

// IsRunning reports whether any check is still queued or running.
func (c ChecksSummary) IsRunning() bool {
	if c.HasRequiredChecks {
		return c.ReqRunning > 0
	}
	return c.Running > 0
}

// IsSettled reports whether the summary can no longer change without a new push
// or workflow dispatch. Required mode demands every required context has both
// appeared AND finished; a required context absent from the rollup is counted in
// neither Done, Running, nor Failed, so IsRunning alone cannot detect
// "workflow has not dispatched yet".
func (c ChecksSummary) IsSettled() bool {
	if c.HasRequiredChecks {
		return c.ReqTotal > 0 && c.ReqDone == c.ReqTotal
	}
	return c.Total > 0 && c.Done == c.Total
}

// Badge returns the CI badge string formatted for the TUI using the default static spinner.
func (c ChecksSummary) Badge() string {
	return c.BadgeWithSpinner("⏳")
}

// BadgeWithSpinner returns the CI badge string formatted for the TUI using the provided spinner glyph when running.
func (c ChecksSummary) BadgeWithSpinner(spinner string) string {
	if c.HasRequiredChecks {
		succeeded := c.Succeeded()
		if c.ReqFailed > 0 {
			if c.ReqRunning > 0 {
				if succeeded > 0 {
					return fmt.Sprintf("CI: ✓ %d  ✗ %d req (%d %s)", succeeded, c.ReqFailed, c.ReqRunning, spinner)
				}
				return fmt.Sprintf("CI: ✗ %d req failed (%d %s)", c.ReqFailed, c.ReqRunning, spinner)
			}
			if succeeded > 0 {
				return fmt.Sprintf("CI: ✓ %d  ✗ %d req", succeeded, c.ReqFailed)
			}
			return fmt.Sprintf("CI: ✗ %d req failed", c.ReqFailed)
		}
		if c.ReqRunning > 0 {
			return fmt.Sprintf("CI: %d/%d req (%d %s)", c.ReqDone, c.ReqTotal, c.ReqRunning, spinner)
		}
		if c.ReqTotal > 0 && c.ReqDone == c.ReqTotal {
			return fmt.Sprintf("CI: ✓ %d/%d", c.ReqDone, c.ReqTotal)
		}
		return fmt.Sprintf("CI: %d/%d req", c.ReqDone, c.ReqTotal)
	}

	if c.Total == 0 {
		return ""
	}
	succeeded := c.Succeeded()
	if c.Failed > 0 {
		if c.Running > 0 {
			if succeeded > 0 {
				return fmt.Sprintf("CI: ✓ %d  ✗ %d (%d %s)", succeeded, c.Failed, c.Running, spinner)
			}
			return fmt.Sprintf("CI: ✗ %d failed (%d %s)", c.Failed, c.Running, spinner)
		}
		if succeeded > 0 {
			return fmt.Sprintf("CI: ✓ %d  ✗ %d", succeeded, c.Failed)
		}
		return fmt.Sprintf("CI: ✗ %d failed", c.Failed)
	}
	if c.Running > 0 {
		return fmt.Sprintf("CI: %d/%d (%d %s)", c.Done, c.Total, c.Running, spinner)
	}
	if c.Done == c.Total {
		return fmt.Sprintf("CI: ✓ %d/%d", c.Done, c.Total)
	}
	return fmt.Sprintf("CI: %d/%d", c.Done, c.Total)
}

func isCheckFailed(conclusion string) bool {
	switch conclusion {
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "CANCELLED":
		return true
	default:
		return false
	}
}
