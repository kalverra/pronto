package notify

import (
	"slices"
	"strconv"
	"strings"

	"github.com/kalverra/pronto/internal/model"
)

// Tab identifies a TUI tab for notification scoping.
type Tab string

// Tabs that notification policy can address.
const (
	TabFocus    Tab = "focus"
	TabMine     Tab = "mine"
	TabPriority Tab = "priority"
	TabInbox    Tab = "inbox"
)

// Tabs lists every tab in policy order.
var Tabs = []Tab{TabFocus, TabMine, TabPriority, TabInbox}

// Scope is a tab, optionally narrowed to one section. Section "" is tab-level.
type Scope struct {
	Tab     Tab           `json:"tab"`
	Section model.Section `json:"section,omitempty"`
}

// Observed is a PR plus every scope it currently occupies (tab-level and
// section-level entries, e.g. {priority,""} and {priority,attention}).
type Observed struct {
	PR     model.PullRequest
	Scopes []Scope
}

// TriggerSet is a set of triggers.
type TriggerSet map[Trigger]bool

// Policy maps every scope to the triggers that notify for it. A missing scope
// notifies nothing.
type Policy map[Scope]TriggerSet

// Wants reports whether any of n.Scopes subscribes n.Trigger.
func (p Policy) Wants(n Notification) bool {
	for _, s := range n.Scopes {
		if p[s][n.Trigger] {
			return true
		}
	}
	return false
}

// Select filters notes by p, then collapses per PR the pr_opened/entered
// duplicates to the single most specific wanted one: section-level entered >
// tab-level entered > pr_opened. Ties between tabs go to the earlier tab in
// Tabs. Other triggers pass through untouched, in input order.
func (p Policy) Select(notes []Notification) []Notification {
	type rank struct {
		idx   int
		score int
	}
	best := make(map[model.PRKey]rank)
	wanted := make([]Notification, 0, len(notes))
	for _, n := range notes {
		if p.Wants(n) {
			wanted = append(wanted, n)
		}
	}
	for i, n := range wanted {
		sc, ok := collapseScore(n)
		if !ok {
			continue
		}
		k := model.PRKey{Repo: n.Repo, Number: n.PRNumber}
		if cur, seen := best[k]; !seen || sc > cur.score {
			best[k] = rank{idx: i, score: sc}
		}
	}
	out := make([]Notification, 0, len(wanted))
	for i, n := range wanted {
		if _, ok := collapseScore(n); ok && best[model.PRKey{Repo: n.Repo, Number: n.PRNumber}].idx != i {
			continue
		}
		out = append(out, n)
	}
	return out
}

// collapseScore ranks collapsible notifications; higher wins.
func collapseScore(n Notification) (int, bool) {
	switch n.Trigger {
	case TriggerPROpened:
		return 0, true
	case TriggerEntered:
		if n.Entered == nil {
			return 1, true
		}
		// Earlier tabs win ties: subtract the tab's index.
		tab := slices.Index(Tabs, n.Entered.Tab)
		if tab < 0 {
			tab = len(Tabs)
		}
		base := 10
		if n.Entered.Section != "" {
			base = 100
		}
		return base - tab, true
	}
	return 0, false
}

// enteredText renders the title and message for an entered notification.
func enteredText(scope Scope, pr model.PullRequest) (title, message string) {
	num := pr.Number
	switch {
	case scope.Section == "" && scope.Tab == TabPriority:
		return titled("🔥 Priority", num), "Now in Priority: " + quoted(pr)
	case scope.Section == model.SectionAttention && (scope.Tab == TabPriority || scope.Tab == TabInbox):
		return titled("👀 Ready for Review", num), "Ready for your review: " + quoted(pr)
	case scope.Tab == TabMine && scope.Section == model.SectionReadyToMerge:
		return titled("🚀 Ready to Merge", num), "Ready to merge: " + quoted(pr)
	case scope.Section == model.SectionActionRequired:
		return titled(
				"⚡ Action Required",
				num,
			), "Now in " + titleWords(
				string(scope.Tab),
			) + " › Action Required: " + quoted(
				pr,
			)
	case scope.Section == "":
		name := titleWords(string(scope.Tab))
		return titled("📌 "+name, num), "Now in " + name + ": " + quoted(pr)
	}
	sec := titleWords(string(scope.Section))
	return titled("📌 "+sec, num), "Now in " + titleWords(string(scope.Tab)) + " › " + sec + ": " + quoted(pr)
}

func titled(name string, num int) string {
	return name + " (#" + strconv.Itoa(num) + ")"
}

func quoted(pr model.PullRequest) string {
	return strconv.Quote(pr.Title) + " (" + pr.Key().String() + ")"
}

// titleWords turns "action_required" into "Action Required".
func titleWords(s string) string {
	words := strings.Split(s, "_")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
