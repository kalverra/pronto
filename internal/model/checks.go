// Package model defines core domain entities for pronto review queues.
package model

import (
	"fmt"
	"slices"
	"time"
)

// CheckState represents the status of a check run or context.
type CheckState string

// ContextCheck represents an individual check run or status context.
type ContextCheck struct {
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// URL links to the check's details (CheckRun detailsUrl or StatusContext targetUrl).
	URL string `json:"url,omitempty"`
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
	State             string     `json:"state,omitempty"`
	HasRequiredChecks bool       `json:"has_required_checks"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`

	ReqTotal   int `json:"req_total"`
	ReqRunning int `json:"req_running"`
	ReqDone    int `json:"req_done"`
	ReqFailed  int `json:"req_failed"`

	Total   int `json:"total"`
	Running int `json:"running"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`

	// FailedURL links to the first failed check (required checks only when
	// configured) that has a details URL; empty when none qualifies.
	FailedURL string `json:"failed_url,omitempty"`
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
	var summary ChecksSummary
	switch {
	case len(requiredContexts) > 0:
		summary = computeRequiredChecksSummary(requiredContexts, checks, rollup.State)
	case rollup.TotalCount > 0 || len(rollup.RunCounts) > 0 || len(rollup.StatusCounts) > 0:
		summary = aggregateRollupCounts(rollup, checks)
	default:
		summary = computeFallbackChecksSummary(checks, rollup.State)
	}
	summary.FailedURL = failedCheckURL(requiredContexts, checks)
	return summary
}

// failedCheckURL returns the URL of the first failed check that has one,
// considering only required checks when any are configured.
func failedCheckURL(requiredContexts []string, checks []ContextCheck) string {
	for _, c := range checks {
		if c.URL == "" {
			continue
		}
		if len(requiredContexts) > 0 && !slices.Contains(requiredContexts, c.Name) {
			continue
		}
		if _, failed, _ := classifyCheck(c); failed {
			return c.URL
		}
	}
	return ""
}

func computeRequiredChecksSummary(requiredContexts []string, checks []ContextCheck, state string) ChecksSummary {
	summary := ChecksSummary{
		State:             state,
		HasRequiredChecks: true,
		ReqTotal:          len(requiredContexts),
	}

	relevantChecks := make([]ContextCheck, 0, len(requiredContexts))
	for _, req := range requiredContexts {
		idx := slices.IndexFunc(checks, func(c ContextCheck) bool {
			return c.Name == req
		})
		if idx == -1 {
			continue
		}
		c := checks[idx]
		relevantChecks = append(relevantChecks, c)
		done, failed, running := classifyCheck(c)
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

	allDone := summary.ReqTotal > 0 && summary.ReqDone == summary.ReqTotal && summary.ReqRunning == 0
	summary.StartedAt, summary.CompletedAt = aggregateTimestamps(relevantChecks, allDone)

	return summary
}

func aggregateRollupCounts(rollup CheckRollup, checks []ContextCheck) ChecksSummary {
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

	summary := ChecksSummary{
		State:   rollup.State,
		Total:   total,
		Running: running,
		Done:    done,
		Failed:  failed,
	}

	allDone := total > 0 && done == total && running == 0
	summary.StartedAt, summary.CompletedAt = aggregateTimestamps(checks, allDone)

	return summary
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

	allDone := summary.Total > 0 && summary.Done == summary.Total && summary.Running == 0
	summary.StartedAt, summary.CompletedAt = aggregateTimestamps(checks, allDone)

	return summary
}

func aggregateTimestamps(checks []ContextCheck, allDone bool) (startedAt, completedAt *time.Time) {
	var earliestStart *time.Time
	var latestComplete *time.Time

	for _, c := range checks {
		if c.StartedAt != nil && !c.StartedAt.IsZero() {
			if earliestStart == nil || c.StartedAt.Before(*earliestStart) {
				t := *c.StartedAt
				earliestStart = &t
			}
		}
		if c.CompletedAt != nil && !c.CompletedAt.IsZero() {
			if latestComplete == nil || c.CompletedAt.After(*latestComplete) {
				t := *c.CompletedAt
				latestComplete = &t
			}
		}
	}

	if earliestStart != nil {
		startedAt = earliestStart
	}
	if allDone && latestComplete != nil {
		completedAt = latestComplete
	}
	return startedAt, completedAt
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

// Duration returns the elapsed duration of checks. If checks are running, it
// calculates elapsed time against refTime. If checks are settled and CompletedAt
// is set, it calculates duration from StartedAt to CompletedAt. If StartedAt is
// nil/zero, or if completed but CompletedAt is nil/zero, it returns (0, false).
func (c ChecksSummary) Duration(refTime time.Time) (time.Duration, bool) {
	if c.StartedAt == nil || c.StartedAt.IsZero() {
		return 0, false
	}
	if c.IsRunning() {
		return max(0, refTime.Sub(*c.StartedAt)), true
	}
	if c.CompletedAt != nil && !c.CompletedAt.IsZero() {
		return max(0, c.CompletedAt.Sub(*c.StartedAt)), true
	}
	return 0, false
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
