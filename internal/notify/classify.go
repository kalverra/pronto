package notify

import (
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// PriorityPartitioner partitions inbox PRs into priority and non-priority subsets.
type PriorityPartitioner interface {
	Partition(prs []model.PullRequest) (priority, remaining []model.PullRequest)
}

// FocusMatcher tests whether a PR matches automatic focus rules.
type FocusMatcher interface {
	Matches(pr model.PullRequest) bool
}

// FocusOverride reports a manual focus decision for a PR: focused is the
// decision and ok is false when the user has not decided (rules then apply).
type FocusOverride func(model.PRKey) (focused, ok bool)

// Classify places every PR in q into its notification scopes: the tab-level
// scope plus the section-level scope for each tab the PR appears in. Authored
// PRs land in mine; incoming PRs in priority or inbox; and any PR that is
// focused (manually, or by rule) additionally lands in focus. Output lists
// authored PRs then inbox PRs, each in input order.
func Classify(
	q model.Queue,
	prio PriorityPartitioner,
	focus FocusMatcher,
	override FocusOverride,
	now time.Time,
) []Observed {
	all := make([]model.PullRequest, 0, len(q.Authored)+len(q.Inbox))
	seen := make(map[model.PRKey]bool, cap(all))
	for _, pr := range q.Authored {
		if !seen[pr.Key()] {
			seen[pr.Key()] = true
			all = append(all, pr)
		}
	}
	for _, pr := range q.Inbox {
		if !seen[pr.Key()] {
			seen[pr.Key()] = true
			all = append(all, pr)
		}
	}

	sections := model.EffectiveSections(all, q.IsAuthored, now)

	var priority []model.PullRequest
	if prio != nil {
		priority, _ = prio.Partition(q.Inbox)
	}
	isPriority := make(map[model.PRKey]bool, len(priority))
	for _, pr := range priority {
		isPriority[pr.Key()] = true
	}

	out := make([]Observed, 0, len(all))
	for _, pr := range all {
		key := pr.Key()
		section := sections[key]

		var tab Tab
		switch {
		case q.IsAuthored(pr):
			tab = TabMine
		case isPriority[key]:
			tab = TabPriority
		default:
			tab = TabInbox
		}
		scopes := []Scope{{Tab: tab}, {Tab: tab, Section: section}}

		focused, decided := false, false
		if override != nil {
			focused, decided = override(key)
		}
		if !decided && focus != nil {
			focused = focus.Matches(pr)
		}
		if focused {
			scopes = append(
				scopes,
				Scope{Tab: TabFocus},
				Scope{Tab: TabFocus, Section: section},
			)
		}
		out = append(out, Observed{PR: pr, Scopes: scopes})
	}
	return out
}
