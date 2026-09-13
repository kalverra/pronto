package model

// MergeState represents the computed merge status of a pull request.
type MergeState string

const (
	// MergeStateClean indicates the pull request is cleanly mergeable.
	MergeStateClean MergeState = "clean"
	// MergeStateConflict indicates the pull request has merge conflicts.
	MergeStateConflict MergeState = "conflict"
	// MergeStateBlocked indicates the pull request is blocked by reviews or checks.
	MergeStateBlocked MergeState = "blocked"
	// MergeStateBehind indicates the pull request base branch has advanced.
	MergeStateBehind MergeState = "behind"
	// MergeStateDraft indicates the pull request is a draft.
	MergeStateDraft MergeState = "draft"
	// MergeStateQueued indicates the pull request has entered a merge queue.
	MergeStateQueued MergeState = "queued"
	// MergeStateUnknown indicates the merge status could not be determined.
	MergeStateUnknown MergeState = "unknown"
)

// MergeStatus evaluates GitHub mergeable and mergeStateStatus fields.
type MergeStatus struct {
	Mergeable        string `json:"mergeable"`
	MergeStateStatus string `json:"merge_state_status"`
	IsDraft          bool   `json:"is_draft"`
	IsInMergeQueue   bool   `json:"is_in_merge_queue,omitempty"`
}

// IsQueued reports if the PR is in a merge queue.
func (m MergeStatus) IsQueued() bool {
	return !m.IsDraft && !m.HasConflict() && (m.IsInMergeQueue || m.MergeStateStatus == "QUEUED")
}

// ComputeMergeStatus calculates the MergeStatus from GitHub fields.
func ComputeMergeStatus(mergeable, mergeStateStatus string, isDraft bool) MergeStatus {
	return MergeStatus{
		Mergeable:        mergeable,
		MergeStateStatus: mergeStateStatus,
		IsDraft:          isDraft,
	}
}

// State returns the high-level MergeState.
func (m MergeStatus) State() MergeState {
	if m.IsDraft {
		return MergeStateDraft
	}
	if m.HasConflict() {
		return MergeStateConflict
	}
	if m.IsQueued() {
		return MergeStateQueued
	}
	switch m.MergeStateStatus {
	case "CLEAN":
		return MergeStateClean
	case "BLOCKED":
		return MergeStateBlocked
	case "BEHIND":
		return MergeStateBehind
	default:
		return MergeStateUnknown
	}
}

// IsClean reports if the PR is cleanly mergeable.
func (m MergeStatus) IsClean() bool {
	return !m.IsDraft && !m.HasConflict() && !m.IsQueued() && m.MergeStateStatus == "CLEAN"
}

// HasConflict reports if the PR has merge conflicts.
func (m MergeStatus) HasConflict() bool {
	return m.Mergeable == "CONFLICTING" || m.MergeStateStatus == "DIRTY"
}

// IsBlocked reports if the PR is blocked by checks or reviews.
func (m MergeStatus) IsBlocked() bool {
	return !m.HasConflict() && !m.IsQueued() && m.MergeStateStatus == "BLOCKED"
}

// IsBehind reports if the PR base branch has advanced.
func (m MergeStatus) IsBehind() bool {
	return !m.HasConflict() && !m.IsQueued() && m.MergeStateStatus == "BEHIND"
}

// Badge returns the badge string for display in the UI.
func (m MergeStatus) Badge() string {
	state := m.State()
	if state == MergeStateUnknown {
		return ""
	}
	return "[" + string(state) + "]"
}
