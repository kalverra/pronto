// Package events defines the pronto event vocabulary, subscription filters,
// and the in-process event bus used to fan events out to socket subscribers.
package events

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Type identifies an emitted event. Trigger-named types mirror the
// notification trigger vocabulary in internal/notify.
type Type string

const (
	// TypeCIPassed indicates CI checks on a PR transitioned to passing.
	TypeCIPassed Type = "ci_passed"
	// TypeCIFailed indicates one or more CI checks on a PR transitioned to failing.
	TypeCIFailed Type = "ci_failed"
	// TypeConflict indicates a PR newly has merge conflicts.
	TypeConflict Type = "conflict"
	// TypeReviewReceived indicates a new review was submitted on a PR.
	TypeReviewReceived Type = "review_received"
	// TypePRMerged indicates a PR was merged.
	TypePRMerged Type = "pr_merged"
	// TypePRAdded indicates a PR newly appeared in the queue.
	TypePRAdded Type = "pr_added"
	// TypePRRemoved indicates a PR left the queue without merging.
	TypePRRemoved Type = "pr_removed"
	// TypeQueueRefreshed reports the outcome of a queue poll. It is not
	// scoped to a single PR.
	TypeQueueRefreshed Type = "queue_refreshed"
)

// ValidTypes lists every known event type.
var ValidTypes = map[Type]bool{
	TypeCIPassed:       true,
	TypeCIFailed:       true,
	TypeConflict:       true,
	TypeReviewReceived: true,
	TypePRMerged:       true,
	TypePRAdded:        true,
	TypePRRemoved:      true,
	TypeQueueRefreshed: true,
}

// TriggerTypes lists event types that represent PR notification triggers.
var TriggerTypes = []Type{
	TypeCIPassed,
	TypeCIFailed,
	TypeConflict,
	TypeReviewReceived,
	TypePRMerged,
}

// TriggerStrings returns TriggerTypes as a slice of strings.
func TriggerStrings() []string {
	res := make([]string, len(TriggerTypes))
	for i, t := range TriggerTypes {
		res[i] = string(t)
	}
	return res
}

// ReviewPayload carries details for review_received events.
type ReviewPayload struct {
	Author      string    `json:"author"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// QueueRefreshedPayload carries details for queue_refreshed events.
type QueueRefreshedPayload struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Authored int    `json:"authored"`
	Inbox    int    `json:"inbox"`
}

// Event is a single emitted pronto event. Seq is monotonic per server
// lifetime; Repo, PR, and Title are set for PR-scoped events.
type Event struct {
	Seq     uint64    `json:"seq"`
	Type    Type      `json:"type"`
	TS      time.Time `json:"ts"`
	Repo    string    `json:"repo,omitempty"`
	PR      int       `json:"pr,omitempty"`
	Title   string    `json:"title,omitempty"`
	Payload any       `json:"payload,omitempty"`
}

// Subscription filters the event stream. Empty fields match anything; set
// fields must all match.
type Subscription struct {
	Types []Type `json:"types,omitempty"`
	Repo  string `json:"repo,omitempty"`
	PR    int    `json:"pr,omitempty"`
}

// Matches reports whether the event passes every set filter.
func (s Subscription) Matches(e Event) bool {
	if len(s.Types) > 0 {
		matched := slices.Contains(s.Types, e.Type)
		if !matched {
			return false
		}
	}
	if s.Repo != "" && e.Repo != s.Repo {
		return false
	}
	if s.PR != 0 && e.PR != s.PR {
		return false
	}
	return true
}

// ParseTypes parses a comma-separated event type list. An empty string yields
// an empty slice (subscribe to everything). Unknown types error.
func ParseTypes(s string) ([]Type, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var types []Type
	for part := range strings.SplitSeq(s, ",") {
		typ := Type(strings.TrimSpace(part))
		if !ValidTypes[typ] {
			return nil, fmt.Errorf("unknown event type %q", string(typ))
		}
		types = append(types, typ)
	}
	return types, nil
}
