package model

// ActionStatus represents the human-actionable state of a pull request.
type ActionStatus string

const (
	// ActionStatusUnknown indicates the action status could not be determined.
	ActionStatusUnknown ActionStatus = "unknown"
	// ActionStatusDraft indicates the pull request is a draft.
	ActionStatusDraft ActionStatus = "draft"
	// ActionStatusConflict indicates the pull request has merge conflicts.
	ActionStatusConflict ActionStatus = "conflict"
	// ActionStatusFailingCI indicates required or reported checks have failed.
	ActionStatusFailingCI ActionStatus = "failing_ci"
	// ActionStatusChangesReq indicates reviewer requested changes.
	ActionStatusChangesReq ActionStatus = "changes_requested"
	// ActionStatusNeedsReview indicates pull request requires review.
	ActionStatusNeedsReview ActionStatus = "needs_review"
	// ActionStatusCIRunning indicates checks are still in progress.
	ActionStatusCIRunning ActionStatus = "ci_running"
	// ActionStatusBehind indicates base branch has advanced.
	ActionStatusBehind ActionStatus = "behind"
	// ActionStatusClean indicates pull request is cleanly mergeable.
	ActionStatusClean ActionStatus = "clean"
	// ActionStatusBlocked indicates pull request is blocked by unmet policy.
	ActionStatusBlocked ActionStatus = "blocked"
	// ActionStatusQueued indicates pull request has entered a merge queue.
	ActionStatusQueued ActionStatus = "queued"
)

// Badge returns the badge text for the action status.
func (s ActionStatus) Badge() string {
	switch s {
	case ActionStatusDraft:
		return "DRAFT"
	case ActionStatusConflict:
		return "CONFLICT"
	case ActionStatusFailingCI:
		return "FAILING CI"
	case ActionStatusChangesReq:
		return "CHANGES REQ"
	case ActionStatusNeedsReview:
		return "NEEDS REVIEW"
	case ActionStatusCIRunning:
		return "CI RUNNING"
	case ActionStatusBehind:
		return "BEHIND"
	case ActionStatusClean:
		return "CLEAN"
	case ActionStatusBlocked:
		return "BLOCKED"
	case ActionStatusQueued:
		return "QUEUED"
	default:
		return ""
	}
}

// ActionStatus returns the human-actionable state of the pull request.
func (pr PullRequest) ActionStatus() ActionStatus {
	if pr.IsDraft || pr.MergeStatus.IsDraft {
		return ActionStatusDraft
	}
	if pr.MergeStatus.HasConflict() || pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" {
		return ActionStatusConflict
	}
	if pr.Checks.IsFailing() {
		return ActionStatusFailingCI
	}
	if pr.InMergeQueue() {
		return ActionStatusQueued
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		return ActionStatusChangesReq
	}
	if pr.ReviewDecision == "REVIEW_REQUIRED" {
		return ActionStatusNeedsReview
	}
	if pr.Checks.IsRunning() {
		return ActionStatusCIRunning
	}
	if pr.MergeStatus.IsBehind() || pr.MergeStateStatus == "BEHIND" {
		return ActionStatusBehind
	}
	if pr.MergeStatus.IsClean() || pr.MergeStateStatus == "CLEAN" {
		return ActionStatusClean
	}
	if pr.MergeStatus.IsBlocked() || pr.MergeStateStatus == "BLOCKED" {
		return ActionStatusBlocked
	}
	return ActionStatusUnknown
}
