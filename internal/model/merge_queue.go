package model

import (
	"fmt"
	"time"
)

// MergeQueueInfo contains metadata about a pull request's entry in a merge queue.
type MergeQueueInfo struct {
	Position   int        `json:"position"`
	State      string     `json:"state"`
	EnqueuedAt *time.Time `json:"enqueued_at,omitempty"`
}

// Badge returns the badge string for display in the UI.
// When position is positive, it returns "[queued #<position>]".
// When position is <= 0, position is omitted and it returns "[queued]".
func (q MergeQueueInfo) Badge() string {
	if q.Position > 0 {
		return fmt.Sprintf("[queued #%d]", q.Position)
	}
	return "[queued]"
}
