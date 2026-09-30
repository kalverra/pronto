package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

// Trigger-set token grammar, evaluated left to right into a set.
const (
	tokenAll     = "all"
	tokenInherit = "inherit"
	tokenNot     = "!"
)

// FocusNotify configures notification triggers for the Focus tab and each of
// its sections. Section lists default to ["inherit"] (the tab's set).
type FocusNotify struct {
	Triggers       []string `json:"triggers"        mapstructure:"triggers"        toml:"triggers"`
	Attention      []string `json:"attention"       mapstructure:"attention"       toml:"attention"`
	ActionRequired []string `json:"action_required" mapstructure:"action_required" toml:"action_required"`
	MergeQueue     []string `json:"merge_queue"     mapstructure:"merge_queue"     toml:"merge_queue"`
	ReadyToMerge   []string `json:"ready_to_merge"  mapstructure:"ready_to_merge"  toml:"ready_to_merge"`
	InReview       []string `json:"in_review"       mapstructure:"in_review"       toml:"in_review"`
	Blocked        []string `json:"blocked"         mapstructure:"blocked"         toml:"blocked"`
	Drafts         []string `json:"drafts"          mapstructure:"drafts"          toml:"drafts"`
	Stale          []string `json:"stale"           mapstructure:"stale"           toml:"stale"`
}

// MineNotify configures notification triggers for the Mine tab and its sections.
type MineNotify struct {
	Triggers       []string `json:"triggers"        mapstructure:"triggers"        toml:"triggers"`
	ActionRequired []string `json:"action_required" mapstructure:"action_required" toml:"action_required"`
	MergeQueue     []string `json:"merge_queue"     mapstructure:"merge_queue"     toml:"merge_queue"`
	ReadyToMerge   []string `json:"ready_to_merge"  mapstructure:"ready_to_merge"  toml:"ready_to_merge"`
	InReview       []string `json:"in_review"       mapstructure:"in_review"       toml:"in_review"`
	Drafts         []string `json:"drafts"          mapstructure:"drafts"          toml:"drafts"`
	Stale          []string `json:"stale"           mapstructure:"stale"           toml:"stale"`
}

// InboxNotify configures notification triggers for an incoming-PR tab
// (Priority or Inbox) and its sections.
type InboxNotify struct {
	Triggers  []string `json:"triggers"  mapstructure:"triggers"  toml:"triggers"`
	Attention []string `json:"attention" mapstructure:"attention" toml:"attention"`
	Blocked   []string `json:"blocked"   mapstructure:"blocked"   toml:"blocked"`
	Stale     []string `json:"stale"     mapstructure:"stale"     toml:"stale"`
}

// tabSections lists the sections addressable under each tab, in display order.
func tabSections(tab notify.Tab) []model.Section {
	switch tab {
	case notify.TabFocus:
		return model.AllSections
	case notify.TabMine:
		return model.MineSections
	case notify.TabPriority, notify.TabInbox:
		return model.InboxSections
	}
	return nil
}

// scopeKey returns the TOML key configuring scope s.
func scopeKey(s notify.Scope) string {
	if s.Section == "" {
		return "notifications." + string(s.Tab) + ".triggers"
	}
	return "notifications." + string(s.Tab) + "." + string(s.Section)
}

// DefaultScopeTokens returns the built-in trigger tokens for scope s.
func DefaultScopeTokens(s notify.Scope) []string {
	if s.Section == "" {
		switch s.Tab {
		case notify.TabFocus:
			return []string{tokenAll, tokenNot + string(notify.TriggerEntered)}
		case notify.TabMine:
			return []string{
				tokenAll, tokenNot + string(notify.TriggerNewCommits), tokenNot + string(notify.TriggerEntered),
			}
		case notify.TabPriority:
			return []string{string(notify.TriggerEntered)}
		case notify.TabInbox:
			return []string{}
		}
		return []string{}
	}
	if (s.Tab == notify.TabPriority || s.Tab == notify.TabInbox) &&
		(s.Section == model.SectionBlocked || s.Section == model.SectionStale) {
		return []string{tokenInherit, tokenNot + string(notify.TriggerEntered)}
	}
	return []string{tokenInherit}
}

// scopeTokens returns the configured trigger tokens for every scope. A nil
// list means "unset" and resolves to the built-in default.
func (n NotificationConfig) scopeTokens() map[notify.Scope][]string {
	tokens := map[notify.Scope][]string{}
	set := func(tab notify.Tab, section model.Section, v []string) {
		tokens[notify.Scope{Tab: tab, Section: section}] = v
	}

	f := n.Focus
	set(notify.TabFocus, "", f.Triggers)
	set(notify.TabFocus, model.SectionAttention, f.Attention)
	set(notify.TabFocus, model.SectionActionRequired, f.ActionRequired)
	set(notify.TabFocus, model.SectionMergeQueue, f.MergeQueue)
	set(notify.TabFocus, model.SectionReadyToMerge, f.ReadyToMerge)
	set(notify.TabFocus, model.SectionInReview, f.InReview)
	set(notify.TabFocus, model.SectionBlocked, f.Blocked)
	set(notify.TabFocus, model.SectionDrafts, f.Drafts)
	set(notify.TabFocus, model.SectionStale, f.Stale)

	m := n.Mine
	set(notify.TabMine, "", m.Triggers)
	set(notify.TabMine, model.SectionActionRequired, m.ActionRequired)
	set(notify.TabMine, model.SectionMergeQueue, m.MergeQueue)
	set(notify.TabMine, model.SectionReadyToMerge, m.ReadyToMerge)
	set(notify.TabMine, model.SectionInReview, m.InReview)
	set(notify.TabMine, model.SectionDrafts, m.Drafts)
	set(notify.TabMine, model.SectionStale, m.Stale)

	for tab, c := range map[notify.Tab]InboxNotify{notify.TabPriority: n.Priority, notify.TabInbox: n.Inbox} {
		set(tab, "", c.Triggers)
		set(tab, model.SectionAttention, c.Attention)
		set(tab, model.SectionBlocked, c.Blocked)
		set(tab, model.SectionStale, c.Stale)
	}
	return tokens
}

// NotificationScopes lists every configurable scope in doc order: for each tab, the
// tab-level scope then its sections.
func NotificationScopes() []notify.Scope {
	var out []notify.Scope
	for _, tab := range notify.Tabs {
		out = append(out, notify.Scope{Tab: tab})
		for _, sec := range tabSections(tab) {
			out = append(out, notify.Scope{Tab: tab, Section: sec})
		}
	}
	return out
}

// resolveTokens evaluates tokens left to right into a trigger set. inherit is
// the tab's resolved set, or nil where "inherit" is not allowed (tab level).
func resolveTokens(tokens []string, inherit notify.TriggerSet) (notify.TriggerSet, error) {
	set := notify.TriggerSet{}
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		switch {
		case tok == tokenAll:
			for _, t := range events.TriggerTypes {
				set[t] = true
			}
		case tok == tokenInherit:
			if inherit == nil {
				return nil, fmt.Errorf("%q is only valid on section keys", tokenInherit)
			}
			for t := range inherit {
				set[t] = true
			}
		case tok == tokenNot+tokenAll:
			return nil, fmt.Errorf("%q is not supported; use [] for an empty set", tok)
		case strings.HasPrefix(tok, tokenNot) && slices.Contains(events.TriggerTypes, events.Type(tok[1:])):
			delete(set, events.Type(tok[1:]))
		case slices.Contains(events.TriggerTypes, events.Type(tok)):
			set[events.Type(tok)] = true
		default:
			return nil, fmt.Errorf(
				"unknown trigger token %q; valid: %s, %s, %s<trigger>, <trigger> (%s)",
				tok, tokenAll, tokenInherit, tokenNot, strings.Join(events.TriggerStrings(), ", "),
			)
		}
	}
	return set, nil
}

// resolve evaluates every scope's tokens. Unset (nil) scopes use defaults.
func (n NotificationConfig) resolve() (notify.Policy, error) {
	tokens := n.scopeTokens()
	policy := notify.Policy{}
	for _, tab := range notify.Tabs {
		tabScope := notify.Scope{Tab: tab}
		tabSet, err := resolveTokens(tokensOrDefault(tokens, tabScope), nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", scopeKey(tabScope), err)
		}
		policy[tabScope] = tabSet
		for _, sec := range tabSections(tab) {
			sc := notify.Scope{Tab: tab, Section: sec}
			set, err := resolveTokens(tokensOrDefault(tokens, sc), tabSet)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", scopeKey(sc), err)
			}
			policy[sc] = set
		}
	}
	return policy, nil
}

func tokensOrDefault(tokens map[notify.Scope][]string, s notify.Scope) []string {
	if t := tokens[s]; t != nil {
		return t
	}
	return DefaultScopeTokens(s)
}

// Policy resolves trigger tokens into a notify.Policy. Invalid tokens (which
// Validate rejects) resolve to nothing.
func (n NotificationConfig) Policy() notify.Policy {
	policy, err := n.resolve()
	if err != nil {
		return notify.Policy{}
	}
	return policy
}

// DefaultNotificationConfig returns the notification config LoadFile produces
// when nothing is configured.
func DefaultNotificationConfig() NotificationConfig {
	return NotificationConfig{Popups: true, Mode: NotifyNative}
}

// notificationScopeSpecs builds one KeySpec per notification scope.
func notificationScopeSpecs() []KeySpec {
	specs := make([]KeySpec, 0, 32)
	for _, sc := range NotificationScopes() {
		spec := KeySpec{
			Key:     scopeKey(sc),
			Type:    "[]trigger",
			Default: DefaultScopeTokens(sc),
		}
		if sc.Section == "" {
			spec.Env = "PRONTO_NOTIFICATIONS_" + strings.ToUpper(string(sc.Tab)) + "_TRIGGERS"
			spec.Doc = fmt.Sprintf(
				"Triggers that notify for PRs in the %s tab. See [Notification policy](#notification-policy).",
				sc.Tab,
			)
		} else {
			spec.Doc = fmt.Sprintf(
				"Triggers that notify for PRs in the %s tab's %s section; `inherit` starts from the tab's set.",
				sc.Tab, sc.Section)
		}
		specs = append(specs, spec)
	}
	return specs
}
