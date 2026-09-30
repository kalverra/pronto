package model

import "time"

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

// EffectiveSections returns each PR's section, stack-aware: every member of a
// stack takes the most urgent (lowest) category among members of the same
// family. Authored PRs in a merge queue are excluded from the stack minimum
// and keep their own category.
func EffectiveSections(prs []PullRequest, isAuthored func(PullRequest) bool, now time.Time) map[PRKey]Section {
	mineStack := make(map[string]MineCategory)
	inboxStack := make(map[string]InboxCategory)
	for _, pr := range prs {
		if !pr.IsPartOfStack() {
			continue
		}
		key := pr.StackKey()
		if isAuthored(pr) {
			if pr.InMergeQueue() {
				continue
			}
			if cat := pr.MineCategory(now); !hasMine(mineStack, key) || cat < mineStack[key] {
				mineStack[key] = cat
			}
			continue
		}
		if cat := pr.InboxCategory(now); !hasInbox(inboxStack, key) || cat < inboxStack[key] {
			inboxStack[key] = cat
		}
	}

	out := make(map[PRKey]Section, len(prs))
	for _, pr := range prs {
		if isAuthored(pr) {
			cat := pr.MineCategory(now)
			if pr.IsPartOfStack() && !pr.InMergeQueue() {
				if eff, ok := mineStack[pr.StackKey()]; ok {
					cat = eff
				}
			}
			out[pr.Key()] = cat.Section()
			continue
		}
		cat := pr.InboxCategory(now)
		if pr.IsPartOfStack() {
			if eff, ok := inboxStack[pr.StackKey()]; ok {
				cat = eff
			}
		}
		out[pr.Key()] = cat.Section()
	}
	return out
}

func hasMine(m map[string]MineCategory, k string) bool {
	_, ok := m[k]
	return ok
}

func hasInbox(m map[string]InboxCategory, k string) bool {
	_, ok := m[k]
	return ok
}
