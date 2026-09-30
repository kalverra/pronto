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
	// TypePROpened indicates a PR was created since the previous poll.
	TypePROpened Type = "pr_opened"
	// TypePRClosed indicates a PR was closed without merging.
	TypePRClosed Type = "pr_closed"
	// TypeMergeQueueEntered indicates a PR entered a merge queue.
	TypeMergeQueueEntered Type = "merge_queue_entered"
	// TypeMergeQueueLeft indicates a PR left a merge queue but is still open.
	TypeMergeQueueLeft Type = "merge_queue_left"
	// TypeNewCommits indicates new commits were pushed to a PR by someone other than the viewer.
	TypeNewCommits Type = "new_commits"
	// TypeEntered indicates a PR gained a tab or tab section it was not in on the previous poll, e.g. a review request landing in Priority or a PR becoming ready for your review (see EnteredPayload).
	TypeEntered Type = "entered"
	// TypePRAdded indicates a PR newly appeared in the queue.
	TypePRAdded Type = "pr_added"
	// TypePRRemoved indicates a PR left the queue without merging.
	TypePRRemoved Type = "pr_removed"
	// TypeQueueRefreshed reports the outcome of a queue poll. It is not
	// scoped to a single PR.
	TypeQueueRefreshed Type = "queue_refreshed"
	// TypeFetchProgress reports incremental progress of an in-flight queue
	// fetch. It is not scoped to a single PR and is not a notification trigger.
	TypeFetchProgress Type = "fetch_progress"
)

// ValidTypes lists every known event type.
var ValidTypes = map[Type]bool{
	TypeCIPassed:          true,
	TypeCIFailed:          true,
	TypeConflict:          true,
	TypeReviewReceived:    true,
	TypePRMerged:          true,
	TypePROpened:          true,
	TypePRClosed:          true,
	TypeMergeQueueEntered: true,
	TypeMergeQueueLeft:    true,
	TypeNewCommits:        true,
	TypeEntered:           true,
	TypePRAdded:           true,
	TypePRRemoved:         true,
	TypeQueueRefreshed:    true,
	TypeFetchProgress:     true,
}

// TriggerTypes lists event types that represent PR notification triggers.
var TriggerTypes = []Type{
	TypeCIPassed,
	TypeCIFailed,
	TypeConflict,
	TypeReviewReceived,
	TypePRMerged,
	TypePROpened,
	TypePRClosed,
	TypeMergeQueueEntered,
	TypeMergeQueueLeft,
	TypeNewCommits,
	TypeEntered,
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

// EnteredPayload carries the scope a PR entered (entered events).
type EnteredPayload struct {
	Tab     string `json:"tab"`
	Section string `json:"section,omitempty"`
}

// NotificationPayload is the rendered desktop notification for an event the
// daemon's notification policy selected. Nil means "do not notify".
type NotificationPayload struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	URL     string `json:"url,omitempty"`
	Image   string `json:"image,omitempty"`
	Sound   string `json:"sound,omitempty"`
}

// QueueRefreshedPayload carries details for queue_refreshed events.
type QueueRefreshedPayload struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Authored int    `json:"authored"`
	Inbox    int    `json:"inbox"`
}

// FetchProgressPayload carries details for fetch_progress events.
type FetchProgressPayload struct {
	Loaded int `json:"loaded"`
	Total  int `json:"total"`
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
	// Notify is set when the notification policy selected this event for
	// desktop delivery; clients deliver it verbatim.
	Notify *NotificationPayload `json:"notify,omitempty"`
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
