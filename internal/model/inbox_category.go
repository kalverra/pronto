package model

import (
	"strings"
	"time"
)

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

// ExplainInboxCategory returns human-readable reasons why pr was classified into its InboxCategory.
func (pr PullRequest) ExplainInboxCategory(now time.Time) string {
	if pr.IsStale(now) {
		return "Inactive for more than 30 days"
	}
	if pr.IsDraft || pr.MergeStatus.IsDraft {
		return "Draft pull request"
	}
	var blockedReasons []string
	if pr.MergeStatus.HasConflict() || pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" {
		blockedReasons = append(blockedReasons, "Merge conflict with base branch")
	}
	if pr.Checks.IsFailing() {
		blockedReasons = append(blockedReasons, "Failing CI checks")
	}
	if pr.Checks.IsRunning() {
		blockedReasons = append(blockedReasons, "CI checks running")
	}
	if pr.InMergeQueue() {
		blockedReasons = append(blockedReasons, "PR is in merge queue")
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		blockedReasons = append(blockedReasons, "Changes requested by reviewer")
	}
	if pr.MergeStatus.IsBehind() || pr.MergeStateStatus == "BEHIND" {
		blockedReasons = append(blockedReasons, "Behind base branch")
	}
	if len(blockedReasons) > 0 {
		return strings.Join(blockedReasons, "; ")
	}
	if pr.MergeStatus.IsClean() || pr.MergeStateStatus == "CLEAN" {
		return "Ready for review (clean, CI passed)"
	}
	if pr.ReviewDecision == "REVIEW_REQUIRED" || pr.ReviewDecision == "" {
		return "Ready for review"
	}
	if pr.MergeStatus.IsBlocked() || pr.MergeStateStatus == "BLOCKED" {
		return "Blocked by merge policy"
	}
	return "Ready for review"
}
