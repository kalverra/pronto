package model

import "time"

// InboxCategory defines the partition for inbox pull requests.
type InboxCategory int

const (
	// CategoryAttention indicates the pull request is ready for review or clean.
	CategoryAttention InboxCategory = iota
	// CategoryBlocked indicates the pull request is blocked on checks, conflicts, or author.
	CategoryBlocked
	// CategoryStale indicates the pull request is stale.
	CategoryStale
)

// InboxCategory returns the inbox classification of the pull request at reference time now.
func (pr PullRequest) InboxCategory(now time.Time) InboxCategory {
	if pr.IsStale(now) {
		return CategoryStale
	}
	if pr.IsDraft || pr.MergeStatus.IsDraft {
		return CategoryBlocked
	}
	if pr.MergeStatus.HasConflict() || pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" {
		return CategoryBlocked
	}
	if pr.Checks.IsFailing() || pr.Checks.IsRunning() {
		return CategoryBlocked
	}
	if pr.InMergeQueue() {
		return CategoryBlocked
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		return CategoryBlocked
	}
	if pr.MergeStatus.IsBehind() || pr.MergeStateStatus == "BEHIND" {
		return CategoryBlocked
	}
	if pr.MergeStatus.IsClean() || pr.MergeStateStatus == "CLEAN" {
		return CategoryAttention
	}
	if pr.ReviewDecision == "REVIEW_REQUIRED" || pr.ReviewDecision == "" {
		return CategoryAttention
	}
	if pr.MergeStatus.IsBlocked() || pr.MergeStateStatus == "BLOCKED" {
		return CategoryBlocked
	}
	return CategoryAttention
}
