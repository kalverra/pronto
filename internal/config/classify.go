package config

import (
	"time"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

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
	prio PriorityConfig,
	focus RuleSet,
	override FocusOverride,
	now time.Time,
) []notify.Observed {
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

	priority, _ := prio.Partition(q.Inbox)
	isPriority := make(map[model.PRKey]bool, len(priority))
	for _, pr := range priority {
		isPriority[pr.Key()] = true
	}

	out := make([]notify.Observed, 0, len(all))
	for _, pr := range all {
		key := pr.Key()
		section := sections[key]

		var tab notify.Tab
		switch {
		case q.IsAuthored(pr):
			tab = notify.TabMine
		case isPriority[key]:
			tab = notify.TabPriority
		default:
			tab = notify.TabInbox
		}
		scopes := []notify.Scope{{Tab: tab}, {Tab: tab, Section: section}}

		focused, decided := false, false
		if override != nil {
			focused, decided = override(key)
		}
		if !decided {
			focused = focus.Matches(pr)
		}
		if focused {
			scopes = append(
				scopes,
				notify.Scope{Tab: notify.TabFocus},
				notify.Scope{Tab: notify.TabFocus, Section: section},
			)
		}
		out = append(out, notify.Observed{PR: pr, Scopes: scopes})
	}
	return out
}
