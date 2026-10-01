package model

import (
	"strings"
	"time"
)

// MineCategory defines the partition for authored pull requests in the Mine view.
type MineCategory int

const (
	// MineCategoryActionRequired indicates PR needs author intervention (changes requested, failing CI, conflict).
	MineCategoryActionRequired MineCategory = iota
	// MineCategoryQueued indicates PR has entered a merge queue.
	MineCategoryQueued
	// MineCategoryReadyToMerge indicates PR is approved and clean/behind ready to merge.
	MineCategoryReadyToMerge
	// MineCategoryInReview indicates PR is waiting on review, CI running, or blocked by policy.
	MineCategoryInReview
	// MineCategoryDraft indicates PR is a draft.
	MineCategoryDraft
	// MineCategoryStale indicates PR has been inactive beyond stale threshold.
	MineCategoryStale
)

// MineCategory returns the authored classification of the pull request at reference time now.
func (pr PullRequest) MineCategory(now time.Time) MineCategory {
	if pr.IsStale(now) {
		return MineCategoryStale
	}
	if pr.IsDraft || pr.MergeStatus.IsDraft {
		return MineCategoryDraft
	}
	if pr.MergeStatus.HasConflict() || pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" {
		return MineCategoryActionRequired
	}
	if pr.InMergeQueue() {
		return MineCategoryQueued
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		return MineCategoryActionRequired
	}
	if pr.Checks.IsFailing() {
		return MineCategoryActionRequired
	}
	if pr.MergeStatus.IsClean() || pr.MergeStateStatus == "CLEAN" {
		return MineCategoryReadyToMerge
	}
	if pr.MergeStatus.IsBehind() || pr.MergeStateStatus == "BEHIND" {
		return MineCategoryReadyToMerge
	}
	if pr.ReviewDecision == "REVIEW_REQUIRED" || pr.ReviewDecision == "" {
		return MineCategoryInReview
	}
	if pr.Checks.IsRunning() {
		return MineCategoryInReview
	}
	if pr.MergeStatus.IsBlocked() || pr.MergeStateStatus == "BLOCKED" {
		return MineCategoryInReview
	}
	return MineCategoryInReview
}

// ExplainMineCategory returns human-readable reasons why pr was classified into its MineCategory.
func (pr PullRequest) ExplainMineCategory(now time.Time) string {
	if pr.IsStale(now) {
		return "Inactive for more than 30 days"
	}
	if pr.IsDraft || pr.MergeStatus.IsDraft {
		return "Draft pull request"
	}
	if pr.MergeStatus.HasConflict() || pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" {
		return "Merge conflict with base branch"
	}
	if pr.InMergeQueue() {
		return "PR is in merge queue"
	}
	var actionReasons []string
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		actionReasons = append(actionReasons, "Changes requested by reviewer")
	}
	if pr.Checks.IsFailing() {
		actionReasons = append(actionReasons, "Failing CI checks")
	}
	if len(actionReasons) > 0 {
		return strings.Join(actionReasons, "; ")
	}
	if pr.MergeStatus.IsClean() || pr.MergeStateStatus == "CLEAN" {
		return "Approved and clean to merge"
	}
	if pr.MergeStatus.IsBehind() || pr.MergeStateStatus == "BEHIND" {
		return "Approved and behind base branch"
	}
	if pr.Checks.IsRunning() {
		return "CI checks running"
	}
	if pr.ReviewDecision == "REVIEW_REQUIRED" {
		return "Waiting on review"
	}
	if pr.MergeStatus.IsBlocked() || pr.MergeStateStatus == "BLOCKED" {
		return "Blocked by merge policy"
	}
	return "Waiting on review"
}
