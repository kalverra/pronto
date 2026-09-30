package gendocs

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/notify"
)

// renderNotificationPolicy renders the "Notification policy" section of
// docs/config.md: the token grammar, the trigger and section vocabularies, and
// the resolved default for every scope. Everything comes from the events,
// model, and config packages so it cannot drift.
func renderNotificationPolicy(configDir string) (string, error) {
	internal := filepath.Dir(configDir)
	eventsInfo, err := parsePackage(filepath.Join(internal, "events"))
	if err != nil {
		return "", err
	}
	modelInfo, err := parsePackage(filepath.Join(internal, "model"))
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("## Notification policy\n\n")
	b.WriteString("Which events raise a desktop notification is configured per tab and per tab section.\n")
	b.WriteString("Every PR is placed in one tab-level scope and one section-level scope per tab it appears in\n")
	b.WriteString("(Focus in addition to its home tab: Mine for your PRs, Priority or Inbox for incoming ones).\n")
	b.WriteString("An event notifies when *any* scope the PR occupies subscribes to its trigger.\n")
	b.WriteString("The event stream (`pronto watch` / `pronto wait`) is unaffected: every event is always emitted,\n")
	b.WriteString("and only policy-selected ones carry a `notify` payload.\n\n")

	b.WriteString("### Triggers\n\n")
	b.WriteString("| Trigger | Fires when |\n| ------- | ---------- |\n")
	for _, c := range eventsInfo.Consts {
		if c.Type == "Type" && slices.Contains(events.TriggerStrings(), c.Value) {
			fmt.Fprintf(&b, "| `%s` | %s |\n", c.Value, strings.TrimPrefix(c.Doc, "indicates "))
		}
	}
	b.WriteString("\n")

	b.WriteString("### Tokens\n\n")
	b.WriteString("Each key is a list of tokens evaluated left to right into a set of triggers.\n\n")
	b.WriteString("| Token | Effect |\n| ----- | ------ |\n")
	b.WriteString("| `<trigger>` | add that trigger |\n")
	b.WriteString("| `!<trigger>` | remove that trigger |\n")
	b.WriteString("| `all` | add every trigger |\n")
	b.WriteString("| `inherit` | add the tab's resolved set (section keys only) |\n")
	b.WriteString("| `[]` | empty set: nothing notifies |\n\n")
	b.WriteString("`!all` and `inherit` on a tab key are rejected. Unset section keys default to `[\"inherit\"]`.\n")
	b.WriteString("Tab keys can also be set from the environment as comma-separated lists, e.g.\n")
	b.WriteString("`PRONTO_NOTIFICATIONS_MINE_TRIGGERS=\"all,!new_commits\"`.\n\n")

	b.WriteString("### Sections\n\n")
	b.WriteString("| Section | Tabs | Holds |\n| ------- | ---- | ----- |\n")
	tabsOf := map[string][]string{}
	for _, sc := range config.NotificationScopes() {
		if sc.Section != "" {
			tabsOf[string(sc.Section)] = append(tabsOf[string(sc.Section)], string(sc.Tab))
		}
	}
	for _, c := range modelInfo.Consts {
		if c.Type != "Section" {
			continue
		}
		tabs := make([]string, len(tabsOf[c.Value]))
		for i, t := range tabsOf[c.Value] {
			tabs[i] = "`" + t + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", c.Value, strings.Join(tabs, ", "), strings.TrimPrefix(c.Doc, "holds "))
	}
	b.WriteString("\n")

	b.WriteString("### Defaults\n\n")
	b.WriteString("| Scope | Default tokens | Resolves to |\n| ----- | -------------- | ----------- |\n")
	policy := config.NotificationConfig{}.Policy()
	for _, sc := range config.NotificationScopes() {
		name := string(sc.Tab)
		if sc.Section != "" {
			name += " › " + string(sc.Section)
		}
		var resolved []string
		for _, t := range events.TriggerTypes {
			if policy[notify.Scope{Tab: sc.Tab, Section: sc.Section}][t] {
				resolved = append(resolved, "`"+string(t)+"`")
			}
		}
		res := strings.Join(resolved, ", ")
		switch {
		case len(resolved) == 0:
			res = "nothing"
		case len(resolved) == len(events.TriggerTypes):
			res = "everything"
		case len(resolved) > len(events.TriggerTypes)/2:
			res = "everything except " + exceptList(resolved)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, formatDefault(config.DefaultScopeTokens(sc)), res)
	}
	b.WriteString("\n")

	b.WriteString("### Examples\n\n```toml\n")
	b.WriteString("# Only CI results and merges for your own PRs.\n")
	b.WriteString("[notifications.mine]\n")
	b.WriteString("triggers = [\"ci_failed\", \"ci_passed\", \"pr_merged\"]\n\n")
	b.WriteString("# Additionally, review activity only while a PR is in review.\n")
	b.WriteString("in_review = [\"inherit\", \"review_received\"]\n\n")
	b.WriteString("# Ping when a PR becomes ready for your review, in Inbox too.\n")
	b.WriteString("[notifications.inbox]\n")
	b.WriteString("attention = [\"entered\"]\n\n")
	b.WriteString("# Silence Focus entirely.\n")
	b.WriteString("[notifications.focus]\n")
	b.WriteString("triggers = []\n")
	b.WriteString("```\n\n")
	return b.String(), nil
}

// exceptList names the triggers missing from resolved.
func exceptList(resolved []string) string {
	var missing []string
	for _, t := range events.TriggerTypes {
		q := "`" + string(t) + "`"
		if !slices.Contains(resolved, q) {
			missing = append(missing, q)
		}
	}
	return strings.Join(missing, ", ")
}
