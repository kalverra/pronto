package tui

import (
	"slices"
	"strings"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/score"
)

// PRExplanation describes why a PR is classified into its tab and section,
// and what notification events will and will not fire for it.
type PRExplanation struct {
	Tab           Tab
	TabName       string
	Section       model.Section
	SectionName   string
	TabReason     string
	SectionReason string
	BaseTab       Tab
	BaseTabName   string
	BaseTabReason string
	IsFocused     bool
	FocusReason   string

	Scopes     []notify.Scope
	WillNotify []events.Type
	WontNotify []events.Type
	PopupsOff  bool
}

// ExplainPR computes the category and notification explanation for scored PR.
func (m Model) ExplainPR(scored score.Scored) PRExplanation {
	pr := scored.PR
	exp := PRExplanation{
		Tab:     m.activeTab,
		TabName: tabTitle(m.activeTab),
	}

	mineKeys := make(map[model.PRKey]bool, len(m.mineItems))
	for _, it := range m.mineItems {
		mineKeys[it.PR.Key()] = true
	}
	isAuthoredPR := mineKeys[pr.Key()] || (m.viewer != "" && strings.EqualFold(pr.Author, m.viewer))
	isAuthored := func(p model.PullRequest) bool {
		return mineKeys[p.Key()] || (m.viewer != "" && strings.EqualFold(p.Author, m.viewer))
	}

	// Section & section explanation (stack-aware)
	allPRs := slices.Concat(m.queue.Authored, m.queue.Inbox)
	if len(allPRs) == 0 {
		allPRs = []model.PullRequest{pr}
	}
	details := model.EffectiveSectionDetails(allPRs, isAuthored, m.refTime())
	if detail, ok := details[pr.Key()]; ok {
		exp.Section = detail.Section
		exp.SectionReason = detail.Reason
		exp.SectionName = sectionDisplayName(detail.Section)
	}

	if m.activeTab == TabFocus {
		exp.TabReason, exp.BaseTabName, exp.BaseTabReason, exp.BaseTab = m.explainFocusTab(pr, isAuthoredPR)
	} else {
		exp.TabReason, exp.IsFocused, exp.FocusReason = m.explainOtherTab(pr)
	}

	exp.Scopes = m.resolvePRScopes(pr, isAuthoredPR, exp.Section)
	exp.WillNotify, exp.WontNotify = m.evaluateTriggers(exp.Scopes)
	exp.PopupsOff = m.notificationsDisabled || (m.notifCfg != nil && !m.notifCfg.Popups)

	return exp
}

func (m Model) explainFocusTab(
	pr model.PullRequest,
	isAuthored bool,
) (tabReason, baseTabName, baseTabReason string, baseTab Tab) {
	switch {
	case m.focusedKeys[pr.Key()]:
		tabReason = "Manually focused (pinned with 'f')"
	case m.focusCfg.Matches(pr):
		tabReason = "Matched focus rule: " + m.focusCfg.Explain(pr)
	default:
		tabReason = "Focused"
	}

	switch {
	case isAuthored:
		baseTab = TabMine
		baseTabName = "Mine"
		baseTabReason = "Authored by you"
		if m.viewer != "" {
			baseTabReason += " (@" + m.viewer + ")"
		}
	case m.priorityCfg.Matches(pr) || isPartOfPriorityStack(m.priorityCfg, pr, m.queue.Inbox):
		baseTab = TabPriority
		baseTabName = "Priority"
		baseTabReason = m.priorityCfg.Explain(pr, m.queue.Inbox)
		if baseTabReason == "" {
			baseTabReason = "Matched Priority criteria"
		}
	default:
		baseTab = TabInbox
		baseTabName = "Inbox"
		if pr.IsAuthorBot() {
			baseTabReason = "Bot-authored PR (excluded from Priority)"
		} else {
			baseTabReason = "Incoming review request (no Priority rules matched)"
		}
	}
	return tabReason, baseTabName, baseTabReason, baseTab
}

func (m Model) explainOtherTab(pr model.PullRequest) (tabReason string, isFocused bool, focusReason string) {
	switch m.activeTab {
	case TabMine:
		tabReason = "Authored by you"
		if m.viewer != "" {
			tabReason += " (@" + m.viewer + ")"
		}
	case TabPriority:
		tabReason = m.priorityCfg.Explain(pr, m.queue.Inbox)
		if tabReason == "" {
			tabReason = "Priority review queue"
		}
	case TabInbox:
		if pr.IsAuthorBot() {
			tabReason = "Bot-authored PR (excluded from Priority)"
		} else {
			tabReason = "Incoming review request (no Priority rules matched)"
		}
	}

	if m.IsFocused(pr.Key()) {
		isFocused = true
		switch {
		case m.focusedKeys[pr.Key()]:
			focusReason = "Also focused (pinned with 'f')"
		case m.focusCfg.Matches(pr):
			focusReason = "Also focused (matched rule: " + m.focusCfg.Explain(pr) + ")"
		default:
			focusReason = "Also in Focus tab"
		}
	}
	return tabReason, isFocused, focusReason
}

func (m Model) resolvePRScopes(pr model.PullRequest, isAuthored bool, sec model.Section) []notify.Scope {
	for _, obs := range m.observe(m.queue) {
		if obs.PR.Key() == pr.Key() {
			return obs.Scopes
		}
	}
	var notifTab notify.Tab
	switch {
	case isAuthored:
		notifTab = notify.TabMine
	case m.priorityCfg.Matches(pr):
		notifTab = notify.TabPriority
	default:
		notifTab = notify.TabInbox
	}
	scopes := []notify.Scope{{Tab: notifTab}, {Tab: notifTab, Section: sec}}
	if m.IsFocused(pr.Key()) {
		scopes = append(scopes,
			notify.Scope{Tab: notify.TabFocus},
			notify.Scope{Tab: notify.TabFocus, Section: sec},
		)
	}
	return scopes
}

func (m Model) evaluateTriggers(scopes []notify.Scope) (willNotify, wontNotify []events.Type) {
	policy := m.notifPolicy
	if policy == nil {
		if m.notifCfg != nil {
			policy = m.notifCfg.Policy()
		} else {
			policy = config.DefaultNotificationConfig().Policy()
		}
	}

	for _, t := range events.TriggerTypes {
		if policy.Wants(notify.Notification{Scopes: scopes, Trigger: t}) {
			willNotify = append(willNotify, t)
		} else {
			wontNotify = append(wontNotify, t)
		}
	}
	return willNotify, wontNotify
}

func isPartOfPriorityStack(cfg config.PriorityConfig, pr model.PullRequest, inbox []model.PullRequest) bool {
	if !pr.IsPartOfStack() || len(inbox) == 0 {
		return false
	}
	for _, other := range inbox {
		if other.IsPartOfStack() && other.StackKey() == pr.StackKey() && cfg.Matches(other) {
			return true
		}
	}
	return false
}

func tabTitle(tab Tab) string {
	switch tab {
	case TabFocus:
		return "Focus"
	case TabMine:
		return "Mine"
	case TabPriority:
		return "Priority"
	case TabInbox:
		return "Inbox"
	}
	return ""
}

func sectionDisplayName(sec model.Section) string {
	switch sec {
	case model.SectionAttention:
		return "Needs your attention"
	case model.SectionActionRequired:
		return "Action required"
	case model.SectionMergeQueue:
		return "In merge queue"
	case model.SectionReadyToMerge:
		return "Ready to merge"
	case model.SectionInReview:
		return "In review"
	case model.SectionBlocked:
		return "Blocked"
	case model.SectionDrafts:
		return "Drafts"
	case model.SectionStale:
		return "Stale"
	default:
		return string(sec)
	}
}
