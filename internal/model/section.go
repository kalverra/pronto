package model

import (
	"fmt"
	"time"
)

// Section names a category divider shown in a TUI tab.
type Section string

const (
	// SectionAttention holds incoming PRs ready for your review (Inbox/Priority "Needs your attention").
	SectionAttention Section = "attention"
	// SectionActionRequired holds your PRs needing action: changes requested, failing CI, or a conflict.
	SectionActionRequired Section = "action_required"
	// SectionMergeQueue holds your PRs currently in a merge queue.
	SectionMergeQueue Section = "merge_queue"
	// SectionReadyToMerge holds your approved PRs that are clean (or behind) and ready to merge.
	SectionReadyToMerge Section = "ready_to_merge"
	// SectionInReview holds your PRs waiting on review or CI.
	SectionInReview Section = "in_review"
	// SectionBlocked holds incoming PRs blocked on checks, conflicts, the author, or a merge queue, and drafts.
	SectionBlocked Section = "blocked"
	// SectionDrafts holds your draft PRs.
	SectionDrafts Section = "drafts"
	// SectionStale holds PRs with no recent activity (both your PRs and incoming PRs).
	SectionStale Section = "stale"
)

// MineSections lists sections valid for authored PRs, in display order.
var MineSections = []Section{
	SectionActionRequired, SectionMergeQueue, SectionReadyToMerge,
	SectionInReview, SectionDrafts, SectionStale,
}

// InboxSections lists sections valid for incoming PRs, in display order.
var InboxSections = []Section{SectionAttention, SectionBlocked, SectionStale}

// AllSections lists every section in the TUI focus tab's display order.
var AllSections = []Section{
	SectionAttention, SectionActionRequired, SectionMergeQueue, SectionReadyToMerge,
	SectionInReview, SectionBlocked, SectionDrafts, SectionStale,
}

// Section returns the section a mine category is displayed under.
func (c MineCategory) Section() Section {
	switch c {
	case MineCategoryActionRequired:
		return SectionActionRequired
	case MineCategoryQueued:
		return SectionMergeQueue
	case MineCategoryReadyToMerge:
		return SectionReadyToMerge
	case MineCategoryInReview:
		return SectionInReview
	case MineCategoryDraft:
		return SectionDrafts
	case MineCategoryStale:
		return SectionStale
	}
	return ""
}

// Section returns the section an inbox category is displayed under.
func (c InboxCategory) Section() Section {
	switch c {
	case CategoryAttention:
		return SectionAttention
	case CategoryBlocked:
		return SectionBlocked
	case CategoryStale:
		return SectionStale
	}
	return ""
}

// SectionDetail holds the effective section and human-readable explanation for a PR.
type SectionDetail struct {
	Section Section `json:"section"`
	Reason  string  `json:"reason"`
}

type stackLeaderMine struct {
	cat MineCategory
	pr  PullRequest
}

type stackLeaderInbox struct {
	cat InboxCategory
	pr  PullRequest
}

// EffectiveSectionDetails returns each PR's section and explanation, stack-aware:
// every member of a stack takes the most urgent (lowest) category among members of
// the same family. Authored PRs in a merge queue are excluded from the stack minimum
// and keep their own category.
func EffectiveSectionDetails(
	prs []PullRequest,
	isAuthored func(PullRequest) bool,
	now time.Time,
) map[PRKey]SectionDetail {
	mineStack := make(map[string]stackLeaderMine)
	inboxStack := make(map[string]stackLeaderInbox)
	for _, pr := range prs {
		if !pr.IsPartOfStack() {
			continue
		}
		key := pr.StackKey()
		if isAuthored(pr) {
			if pr.InMergeQueue() {
				continue
			}
			cat := pr.MineCategory(now)
			if cur, ok := mineStack[key]; !ok || cat < cur.cat {
				mineStack[key] = stackLeaderMine{cat: cat, pr: pr}
			}
			continue
		}
		cat := pr.InboxCategory(now)
		if cur, ok := inboxStack[key]; !ok || cat < cur.cat {
			inboxStack[key] = stackLeaderInbox{cat: cat, pr: pr}
		}
	}

	out := make(map[PRKey]SectionDetail, len(prs))
	for _, pr := range prs {
		if isAuthored(pr) {
			cat := pr.MineCategory(now)
			reason := pr.ExplainMineCategory(now)
			if pr.IsPartOfStack() && !pr.InMergeQueue() {
				if leader, ok := mineStack[pr.StackKey()]; ok && leader.cat < cat {
					cat = leader.cat
					reason = fmt.Sprintf(
						"Inherited from stack entry #%d (%s)",
						leader.pr.Number,
						leader.pr.ExplainMineCategory(now),
					)
				}
			}
			out[pr.Key()] = SectionDetail{Section: cat.Section(), Reason: reason}
			continue
		}
		cat := pr.InboxCategory(now)
		reason := pr.ExplainInboxCategory(now)
		if pr.IsPartOfStack() {
			if leader, ok := inboxStack[pr.StackKey()]; ok && leader.cat < cat {
				cat = leader.cat
				reason = fmt.Sprintf(
					"Inherited from stack entry #%d (%s)",
					leader.pr.Number,
					leader.pr.ExplainInboxCategory(now),
				)
			}
		}
		out[pr.Key()] = SectionDetail{Section: cat.Section(), Reason: reason}
	}
	return out
}

// EffectiveSections returns each PR's section, stack-aware: every member of a
// stack takes the most urgent (lowest) category among members of the same
// family. Authored PRs in a merge queue are excluded from the stack minimum
// and keep their own category.
func EffectiveSections(prs []PullRequest, isAuthored func(PullRequest) bool, now time.Time) map[PRKey]Section {
	details := EffectiveSectionDetails(prs, isAuthored, now)
	out := make(map[PRKey]Section, len(details))
	for k, d := range details {
		out[k] = d.Section
	}
	return out
}
