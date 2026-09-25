package model

import (
	"slices"
	"strings"
	"time"
)

// Review represents a review on a pull request.
type Review struct {
	Author      string    `json:"author"`
	State       string    `json:"state"`
	CommitOID   string    `json:"commit_oid"`
	SubmittedAt time.Time `json:"submitted_at"`
	// URL links to the review on the pull request page.
	URL string `json:"url,omitempty"`
}

// TimelineItemType distinguishes timeline item kinds.
type TimelineItemType string

const (
	// TimelineItemReviewRequested represents a review requested event.
	TimelineItemReviewRequested TimelineItemType = "ReviewRequestedEvent"
	// TimelineItemCommit represents a pull request commit.
	TimelineItemCommit TimelineItemType = "PullRequestCommit"
)

// TimelineItem represents a timeline event on a pull request.
type TimelineItem struct {
	Type          TimelineItemType `json:"type"`
	CreatedAt     time.Time        `json:"created_at"`
	ReviewerUser  string           `json:"reviewer_user"`
	ReviewerTeam  string           `json:"reviewer_team"`
	CommitOID     string           `json:"commit_oid"`
	CommittedDate time.Time        `json:"committed_date"`
}

// Commit represents a commit on a pull request.
type Commit struct {
	OID           string    `json:"oid"`
	CommittedDate time.Time `json:"committed_date"`
}

// PullRequest represents a pull request in the review queue.
type PullRequest struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	URL               string    `json:"url"`
	IsDraft           bool      `json:"is_draft"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	HeadRefName       string    `json:"head_ref_name,omitempty"`
	HeadRefOID        string    `json:"head_ref_oid"`
	BaseRefName       string    `json:"base_ref_name,omitempty"`
	Additions         int       `json:"additions"`
	Deletions         int       `json:"deletions"`
	ChangedFiles      int       `json:"changed_files"`
	FilesTruncated    bool      `json:"files_truncated"`
	Files             []string  `json:"files"`
	Author            string    `json:"author"`
	AuthorIsBot       bool      `json:"author_is_bot,omitempty"`
	Stale             bool      `json:"stale,omitempty"`
	RepoOwner         string    `json:"repo_owner"`
	RepoName          string    `json:"repo_name"`
	RepoNameWithOwner string    `json:"repo_name_with_owner"`
	Mergeable         string    `json:"mergeable"`
	MergeStateStatus  string    `json:"merge_state_status"`
	ReviewDecision    string    `json:"review_decision"`
	Assigned          bool      `json:"assigned"`
	// DirectRequest marks a PR whose review is requested from the viewer
	// personally, not only through one of their teams.
	DirectRequest  bool `json:"direct_request,omitempty"`
	Starred        bool `json:"starred"`
	IsInMergeQueue bool `json:"is_in_merge_queue,omitempty"`
	// Partial marks a PR built from discovery data only (hydration skipped,
	// e.g. due to a rate limit): merge status, checks, and diff size are
	// unknown until a later poll completes them.
	Partial bool `json:"partial,omitempty"`

	MergeStatus      MergeStatus     `json:"merge_status"`
	Checks           ChecksSummary   `json:"checks"`
	MergeQueue       *MergeQueueInfo `json:"merge_queue,omitempty"`
	MergeQueueChecks ChecksSummary   `json:"merge_queue_checks,omitempty"` //nolint:modernize // omitempty matches model spec

	Stack *PRStack `json:"stack,omitempty"`

	LatestReviews []Review       `json:"latest_reviews"`
	TimelineItems []TimelineItem `json:"timeline_items"`
	Commits       []Commit       `json:"commits"`
}

// PRStack holds metadata about a pull request stack.
type PRStack struct {
	ID          string `json:"id"`
	Number      int    `json:"number"`
	Size        int    `json:"size"`
	Position    int    `json:"position"`
	BaseRefName string `json:"base_ref_name,omitempty"`
}

// IsPartOfStack reports whether the PR belongs to a pull request stack.
func (pr PullRequest) IsPartOfStack() bool {
	return pr.Stack != nil && pr.Stack.Size > 1
}

// InMergeQueue reports whether the pull request has entered a merge queue.
func (pr PullRequest) InMergeQueue() bool {
	return pr.IsInMergeQueue || pr.MergeStateStatus == "QUEUED" || pr.MergeStatus.IsQueued() || pr.MergeQueue != nil
}

// DisplayChecks returns the checks summary that best represents the PR's
// current CI progress. While queued, GitHub runs checks against a temporary
// merge-group commit distinct from the PR's own head commit, so MergeQueueChecks
// is preferred once it has data; the PR's own Checks (e.g. from before it
// entered the queue) is used as a fallback until the merge queue run reports in.
func (pr PullRequest) DisplayChecks() ChecksSummary {
	if pr.InMergeQueue() && (pr.MergeQueueChecks.Total > 0 || pr.MergeQueueChecks.ReqTotal > 0) {
		return pr.MergeQueueChecks
	}
	return pr.Checks
}

// StackKey returns a composite identifier for grouping PRs in the same stack.
func (pr PullRequest) StackKey() string {
	if pr.Stack == nil || pr.Stack.ID == "" {
		return ""
	}
	if pr.RepoNameWithOwner != "" {
		return pr.RepoNameWithOwner + "#" + pr.Stack.ID
	}
	return pr.Stack.ID
}

// WaitHours returns the hours elapsed since review was requested for the viewer or their teams.
func (pr PullRequest) WaitHours(now time.Time, viewer string, teams []string) float64 {
	latestReq := pr.latestViewerRequest(viewer, teams)
	if latestReq.IsZero() {
		return 0
	}
	return now.Sub(latestReq).Hours()
}

// latestViewerRequest returns the most recent review request targeting the viewer or one of
// their teams. Zero time means the viewer was never requested.
func (pr PullRequest) latestViewerRequest(viewer string, teams []string) time.Time {
	var latestReq time.Time
	for _, item := range pr.TimelineItems {
		if item.Type != TimelineItemReviewRequested {
			continue
		}
		isViewer := item.ReviewerUser != "" && item.ReviewerUser == viewer
		isTeam := item.ReviewerTeam != "" && slices.Contains(teams, item.ReviewerTeam)
		if isViewer || isTeam {
			if item.CreatedAt.After(latestReq) {
				latestReq = item.CreatedAt
			}
		}
	}
	return latestReq
}

// AuthorIdle returns true if the author pushed nothing since the viewer (or one of their
// teams) was last requested as reviewer — the author is blocked on the viewer. Requests
// targeting other reviewers are ignored; if the viewer was never requested, idle is false.
func (pr PullRequest) AuthorIdle(viewer string, teams []string) bool {
	latestReq := pr.latestViewerRequest(viewer, teams)
	if latestReq.IsZero() {
		return false
	}

	for _, c := range pr.Commits {
		if c.CommittedDate.After(latestReq) {
			return false
		}
	}
	for _, item := range pr.TimelineItems {
		if item.Type == TimelineItemCommit {
			if item.CommittedDate.After(latestReq) {
				return false
			}
		}
	}

	return true
}

// LastReviewBy returns the latest review by the specified reviewer, or nil if none.
func (pr PullRequest) LastReviewBy(reviewer string) *Review {
	var latest *Review
	for i := range pr.LatestReviews {
		rev := &pr.LatestReviews[i]
		if rev.Author == reviewer {
			if latest == nil || rev.SubmittedAt.After(latest.SubmittedAt) {
				latest = rev
			}
		}
	}
	return latest
}

// HasNewCommitsSinceReview returns true if commits were pushed after the reviewer's last review.
func (pr PullRequest) HasNewCommitsSinceReview(reviewer string) bool {
	rev := pr.LastReviewBy(reviewer)
	if rev == nil {
		return false
	}

	for _, c := range pr.Commits {
		if c.CommittedDate.After(rev.SubmittedAt) {
			return true
		}
	}
	for _, item := range pr.TimelineItems {
		if item.Type == TimelineItemCommit {
			if item.CommittedDate.After(rev.SubmittedAt) {
				return true
			}
		}
	}

	return false
}

// IsBotLogin returns true if login matches common bot naming conventions.
func IsBotLogin(login string) bool {
	if login == "" {
		return false
	}
	lower := strings.ToLower(login)
	return strings.HasSuffix(lower, "[bot]") ||
		strings.HasSuffix(lower, "-bot") ||
		strings.HasSuffix(lower, "_bot") ||
		lower == "dependabot" ||
		lower == "renovate" ||
		strings.Contains(lower, "renovate") ||
		strings.HasPrefix(lower, "app-token-issuer")
}

// IsAuthorBot returns whether the pull request author is a bot account.
func (pr PullRequest) IsAuthorBot() bool {
	if pr.AuthorIsBot {
		return true
	}
	return IsBotLogin(pr.Author)
}

// StaleThreshold is the inactivity duration after which a pull request is considered stale.
const StaleThreshold = 30 * 24 * time.Hour

// IsStale reports whether the pull request is considered stale at the given reference time.
func (pr PullRequest) IsStale(now time.Time) bool {
	if !pr.UpdatedAt.IsZero() {
		if now.IsZero() {
			now = time.Now()
		}
		return now.Sub(pr.UpdatedAt) >= StaleThreshold
	}
	return pr.Stale
}
