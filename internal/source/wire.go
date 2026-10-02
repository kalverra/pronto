// Package source provides interfaces and implementations for retrieving pull request queues.
package source

import (
	"slices"
	"strings"
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// rawPR and friends model GitHub's wire shape for a pull request, split by
// fetch phase:
//
//   - rawIdentity: light fields from the discovery search phase
//   - rawStable: fields immutable for a given head OID (cacheable)
//   - rawFresh: fields that can change without a new commit (never cached)
//
// These types are decoded by two different paths and must satisfy both:
//   - the live GraphQL client (go-gh) decodes via encoding/json, matching json
//     tags first, then field names case-insensitively,
//   - the fixture loader uses encoding/json with the same tags.
//
// Renaming a field or tag can silently break either path — keep field names
// mirroring the GraphQL schema and json tags mirroring the fixture format.

type rawIdentity struct {
	ID             string         `json:"id"`
	Number         int            `json:"number"`
	Title          string         `json:"title"`
	URL            string         `json:"url"`
	IsDraft        bool           `json:"isDraft"`
	IsInMergeQueue bool           `json:"isInMergeQueue"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	HeadRefName    string         `json:"headRefName"`
	HeadRefOID     string         `json:"headRefOid"`
	BaseRefName    string         `json:"baseRefName"`
	Author         rawLogin       `json:"author"`
	Repository     rawRepo        `json:"repository"`
	Stack          *rawStack      `json:"stack"`
	StackEntry     *rawStackEntry `json:"stackEntry"`
	State          string         `json:"state"`

	// Change fingerprint (aliased fp* in DiscoveryFields): cheap signals
	// that move without bumping updatedAt.
	FPMergeable        string        `json:"fpMergeable"`
	FPMergeStateStatus string        `json:"fpMergeStateStatus"`
	FPReviewDecision   string        `json:"fpReviewDecision"`
	FPLastCommit       rawRollupHead `json:"fpLastCommit"`
}

// rawRollupHead is the fingerprint's view of the head commit: only the
// overall status check rollup state.
type rawRollupHead struct {
	Nodes []struct {
		Commit struct {
			StatusCheckRollup *struct {
				State string `json:"state"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	} `json:"nodes"`
}

// rollupState returns the head commit's status check rollup state from the
// fingerprint, or "" when the commit has no checks.
func (id rawIdentity) rollupState() string {
	if len(id.FPLastCommit.Nodes) == 0 || id.FPLastCommit.Nodes[0].Commit.StatusCheckRollup == nil {
		return ""
	}
	return id.FPLastCommit.Nodes[0].Commit.StatusCheckRollup.State
}

type rawStable struct {
	CreatedAt    time.Time `json:"createdAt"`
	Additions    int       `json:"additions"`
	Deletions    int       `json:"deletions"`
	ChangedFiles int       `json:"changedFiles"`
	Files        rawFiles  `json:"files"`
}

type rawFresh struct {
	Mergeable        string              `json:"mergeable"`
	MergeStateStatus string              `json:"mergeStateStatus"`
	ReviewDecision   string              `json:"reviewDecision"`
	LatestReviews    rawReviews          `json:"latestReviews"`
	TimelineItems    rawTimeline         `json:"timelineItems"`
	BaseRef          *rawBaseRef         `json:"baseRef"`
	Commits          rawCommits          `json:"commits"`
	MergeQueueEntry  *rawMergeQueueEntry `json:"mergeQueueEntry"`
}

type rawStack struct {
	ID          string `json:"id"`
	Number      int    `json:"number"`
	Size        int    `json:"size"`
	BaseRefName string `json:"baseRefName"`
}

type rawStackEntry struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
}

// rawPR is the full wire shape: a hydration response node (stable fields are
// zero-valued when the query skipped them on a cache hit) and the fixture
// loader's record.
type rawPR struct {
	rawIdentity
	rawStable
	rawFresh
}

type rawLogin struct {
	Typename string `json:"__typename"`
	Login    string `json:"login"`
}

type rawRepo struct {
	NameWithOwner    string `json:"nameWithOwner"`
	DefaultBranchRef struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
}

type rawFiles struct {
	Nodes []struct {
		Path string `json:"path"`
	} `json:"nodes"`
}

type rawReviews struct {
	Nodes []struct {
		URL    string   `json:"url"`
		Author rawLogin `json:"author"`
		State  string   `json:"state"`
		Commit struct {
			OID string `json:"oid"`
		} `json:"commit"`
		SubmittedAt time.Time `json:"submittedAt"`
	} `json:"nodes"`
}

type rawTimeline struct {
	Nodes []struct {
		Typename          string    `json:"__typename"`
		CreatedAt         time.Time `json:"createdAt"`
		RequestedReviewer struct {
			Login        string `json:"login"`
			Slug         string `json:"slug"`
			Organization struct {
				Login string `json:"login"`
			} `json:"organization"`
		} `json:"requestedReviewer"`
		Commit struct {
			OID           string    `json:"oid"`
			CommittedDate time.Time `json:"committedDate"`
		} `json:"commit"`
	} `json:"nodes"`
}

type rawBaseRef struct {
	BranchProtectionRule *struct {
		RequiredStatusCheckContexts []string `json:"requiredStatusCheckContexts"`
	} `json:"branchProtectionRule"`
	Rules *struct {
		Nodes []struct {
			Type              string `json:"type"`
			RepositoryRuleset *struct {
				Enforcement string `json:"enforcement"`
			} `json:"repositoryRuleset"`
			Parameters *struct {
				RequiredStatusChecks []struct {
					Context string `json:"context"`
				} `json:"requiredStatusChecks"`
			} `json:"parameters"`
		} `json:"nodes"`
	} `json:"rules"`
}

// rawStatusCheckRollup mirrors the StatusCheckRollupFields fragment, shared by
// a PR's own head commit and a merge queue entry's temporary head commit.
type rawStatusCheckRollup struct {
	State    string `json:"state"`
	Contexts struct {
		TotalCount            int `json:"totalCount"`
		CheckRunCountsByState []struct {
			State string `json:"state"`
			Count int    `json:"count"`
		} `json:"checkRunCountsByState"`
		StatusContextCountsByState []struct {
			State string `json:"state"`
			Count int    `json:"count"`
		} `json:"statusContextCountsByState"`
		Nodes []struct {
			Typename    string     `json:"__typename"`
			Name        string     `json:"name"`
			Status      string     `json:"status"`
			Conclusion  string     `json:"conclusion"`
			Context     string     `json:"context"`
			State       string     `json:"state"`
			StartedAt   *time.Time `json:"startedAt"`
			CompletedAt *time.Time `json:"completedAt"`
			CreatedAt   *time.Time `json:"createdAt"`
			DetailsURL  string     `json:"detailsUrl"`
			TargetURL   string     `json:"targetUrl"`
		} `json:"nodes"`
	} `json:"contexts"`
}

type rawCommits struct {
	Nodes []struct {
		Commit struct {
			OID               string                `json:"oid"`
			CommittedDate     time.Time             `json:"committedDate"`
			StatusCheckRollup *rawStatusCheckRollup `json:"statusCheckRollup"`
		} `json:"commit"`
	} `json:"nodes"`
}

// rawMergeQueueEntry mirrors PullRequest.mergeQueueEntry: the PR's position and
// state in its repository's merge queue, plus the temporary head commit GitHub
// creates to run CI for the merge group (distinct from the PR's own head commit).
type rawMergeQueueEntry struct {
	Position   int       `json:"position"`
	State      string    `json:"state"`
	EnqueuedAt time.Time `json:"enqueuedAt"`
	HeadCommit *struct {
		OID               string                `json:"oid"`
		StatusCheckRollup *rawStatusCheckRollup `json:"statusCheckRollup"`
	} `json:"headCommit"`
}

// stableFields is the phase-agnostic form of head-OID-stable data, composable
// from either a wire response or the on-disk cache.
type stableFields struct {
	CreatedAt    time.Time
	Additions    int
	Deletions    int
	ChangedFiles int
	Files        []string
}

func stableFromWire(raw rawStable) stableFields {
	files := make([]string, 0, len(raw.Files.Nodes))
	for _, fn := range raw.Files.Nodes {
		files = append(files, fn.Path)
	}
	return stableFields{
		CreatedAt:    raw.CreatedAt,
		Additions:    raw.Additions,
		Deletions:    raw.Deletions,
		ChangedFiles: raw.ChangedFiles,
		Files:        files,
	}
}

func stableFromCached(cached model.PullRequest) stableFields {
	return stableFields{
		CreatedAt:    cached.CreatedAt,
		Additions:    cached.Additions,
		Deletions:    cached.Deletions,
		ChangedFiles: cached.ChangedFiles,
		Files:        cached.Files,
	}
}

func convertStack(rawS *rawStack, rawE *rawStackEntry) *model.PRStack {
	if rawS == nil {
		return nil
	}
	pos := 0
	if rawE != nil {
		pos = rawE.Position
	}
	return &model.PRStack{
		ID:          rawS.ID,
		Number:      rawS.Number,
		Size:        rawS.Size,
		Position:    pos,
		BaseRefName: rawS.BaseRefName,
	}
}

func convertRequiredContexts(baseRef *rawBaseRef) []string {
	if baseRef == nil {
		return nil
	}
	var requiredContexts []string
	if baseRef.BranchProtectionRule != nil {
		for _, ctx := range baseRef.BranchProtectionRule.RequiredStatusCheckContexts {
			if ctx != "" && !slices.Contains(requiredContexts, ctx) {
				requiredContexts = append(requiredContexts, ctx)
			}
		}
	}
	if baseRef.Rules != nil {
		for _, node := range baseRef.Rules.Nodes {
			if node.RepositoryRuleset != nil && node.RepositoryRuleset.Enforcement != "ACTIVE" {
				continue
			}
			if node.Parameters != nil {
				for _, sc := range node.Parameters.RequiredStatusChecks {
					if sc.Context != "" && !slices.Contains(requiredContexts, sc.Context) {
						requiredContexts = append(requiredContexts, sc.Context)
					}
				}
			}
		}
	}
	return requiredContexts
}

func convertChecks(commits rawCommits) (model.CheckRollup, []model.ContextCheck) {
	if len(commits.Nodes) == 0 {
		return model.CheckRollup{}, nil
	}
	return convertChecksFromRollup(commits.Nodes[0].Commit.StatusCheckRollup)
}

func convertChecksFromRollup(ru *rawStatusCheckRollup) (model.CheckRollup, []model.ContextCheck) {
	var rollup model.CheckRollup
	var checks []model.ContextCheck
	if ru == nil {
		return rollup, checks
	}

	rollup.State = ru.State
	rollup.TotalCount = ru.Contexts.TotalCount
	if len(ru.Contexts.CheckRunCountsByState) > 0 {
		rollup.RunCounts = make(map[string]int, len(ru.Contexts.CheckRunCountsByState))
		for _, c := range ru.Contexts.CheckRunCountsByState {
			rollup.RunCounts[c.State] = c.Count
		}
	}
	if len(ru.Contexts.StatusContextCountsByState) > 0 {
		rollup.StatusCounts = make(map[string]int, len(ru.Contexts.StatusContextCountsByState))
		for _, c := range ru.Contexts.StatusContextCountsByState {
			rollup.StatusCounts[c.State] = c.Count
		}
	}
	for _, node := range ru.Contexts.Nodes {
		name := node.Name
		if name == "" {
			name = node.Context
		}
		status := node.Status
		if status == "" {
			status = node.State
		}
		startedAt := node.StartedAt
		if (startedAt == nil || startedAt.IsZero()) && node.CreatedAt != nil && !node.CreatedAt.IsZero() {
			startedAt = node.CreatedAt
		}
		var finalStartedAt, finalCompletedAt *time.Time
		if startedAt != nil && !startedAt.IsZero() {
			t := *startedAt
			finalStartedAt = &t
		}
		if node.CompletedAt != nil && !node.CompletedAt.IsZero() {
			t := *node.CompletedAt
			finalCompletedAt = &t
		}
		url := node.DetailsURL
		if url == "" {
			url = node.TargetURL
		}
		checks = append(checks, model.ContextCheck{
			URL:         url,
			Name:        name,
			Status:      status,
			Conclusion:  node.Conclusion,
			StartedAt:   finalStartedAt,
			CompletedAt: finalCompletedAt,
		})
	}
	return rollup, checks
}

type convertOpts struct {
	staleActivityAfter time.Duration
}

// convertPR converts identity, stable, and fresh parts into the domain model.
func convertPR(
	ident rawIdentity,
	stable stableFields,
	fresh rawFresh,
	assigned bool,
	opts convertOpts,
) model.PullRequest {
	repoParts := strings.Split(ident.Repository.NameWithOwner, "/")
	var owner, name string
	if len(repoParts) == 2 {
		owner = repoParts[0]
		name = repoParts[1]
	}

	reviews := make([]model.Review, 0, len(fresh.LatestReviews.Nodes))
	for _, r := range fresh.LatestReviews.Nodes {
		reviews = append(reviews, model.Review{
			Author:      r.Author.Login,
			State:       r.State,
			CommitOID:   r.Commit.OID,
			SubmittedAt: r.SubmittedAt,
			URL:         r.URL,
		})
	}

	timelineItems := make([]model.TimelineItem, 0, len(fresh.TimelineItems.Nodes))
	var commits []model.Commit
	if len(fresh.Commits.Nodes) > 0 {
		c := fresh.Commits.Nodes[0].Commit
		if c.OID != "" || !c.CommittedDate.IsZero() {
			commits = append(commits, model.Commit{
				OID:           c.OID,
				CommittedDate: c.CommittedDate,
			})
		}
	}
	fromTimeline := len(commits) == 0
	for _, item := range fresh.TimelineItems.Nodes {
		switch item.Typename {
		case "ReviewRequestedEvent":
			// Team requests are org-qualified so they can be matched against
			// the viewer's org-qualified team list.
			team := item.RequestedReviewer.Slug
			if org := item.RequestedReviewer.Organization.Login; org != "" && team != "" {
				team = org + "/" + team
			}
			timelineItems = append(timelineItems, model.TimelineItem{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    item.CreatedAt,
				ReviewerUser: item.RequestedReviewer.Login,
				ReviewerTeam: team,
			})
		case "PullRequestCommit":
			timelineItems = append(timelineItems, model.TimelineItem{
				Type:          model.TimelineItemCommit,
				CommitOID:     item.Commit.OID,
				CommittedDate: item.Commit.CommittedDate,
			})
			if fromTimeline {
				commits = append(commits, model.Commit{
					OID:           item.Commit.OID,
					CommittedDate: item.Commit.CommittedDate,
				})
			}
		}
	}

	requiredContexts := convertRequiredContexts(fresh.BaseRef)

	rollup, checks := convertChecks(fresh.Commits)

	var mergeQueueInfo *model.MergeQueueInfo
	var mergeQueueChecks model.ChecksSummary
	if mqe := fresh.MergeQueueEntry; mqe != nil {
		enqueuedAt := mqe.EnqueuedAt
		mergeQueueInfo = &model.MergeQueueInfo{
			Position:   mqe.Position,
			State:      mqe.State,
			EnqueuedAt: &enqueuedAt,
		}
		if mqe.HeadCommit != nil {
			mqRollup, mqChecks := convertChecksFromRollup(mqe.HeadCommit.StatusCheckRollup)
			mergeQueueChecks = model.ComputeChecksSummaryWithRollup(requiredContexts, mqChecks, mqRollup)
		}
	}

	isInMergeQueue := ident.IsInMergeQueue || (fresh.MergeQueueEntry != nil)
	mergeStatus := model.ComputeMergeStatus(fresh.Mergeable, fresh.MergeStateStatus, ident.IsDraft)
	mergeStatus.IsInMergeQueue = isInMergeQueue
	checksSummary := model.ComputeChecksSummaryWithRollup(requiredContexts, checks, rollup)
	isBot := ident.Author.Typename == "Bot" || model.IsBotLogin(ident.Author.Login)
	staleThreshold := opts.staleActivityAfter
	if staleThreshold <= 0 {
		staleThreshold = defaultStaleActivityAfter
	}
	isStale := !ident.UpdatedAt.IsZero() && time.Since(ident.UpdatedAt) >= staleThreshold
	prStack := convertStack(ident.Stack, ident.StackEntry)

	return model.PullRequest{
		Number:            ident.Number,
		Title:             ident.Title,
		URL:               ident.URL,
		IsDraft:           ident.IsDraft,
		IsInMergeQueue:    isInMergeQueue,
		CreatedAt:         stable.CreatedAt,
		UpdatedAt:         ident.UpdatedAt,
		HeadRefName:       ident.HeadRefName,
		HeadRefOID:        ident.HeadRefOID,
		BaseRefName:       ident.BaseRefName,
		DefaultBranch:     ident.Repository.DefaultBranchRef.Name,
		Stack:             prStack,
		Additions:         stable.Additions,
		Deletions:         stable.Deletions,
		ChangedFiles:      stable.ChangedFiles,
		FilesTruncated:    stable.ChangedFiles > len(stable.Files),
		Files:             stable.Files,
		Author:            ident.Author.Login,
		AuthorIsBot:       isBot,
		Stale:             isStale,
		RepoOwner:         owner,
		RepoName:          name,
		RepoNameWithOwner: ident.Repository.NameWithOwner,
		Mergeable:         fresh.Mergeable,
		MergeStateStatus:  fresh.MergeStateStatus,
		ReviewDecision:    fresh.ReviewDecision,
		Assigned:          assigned,
		MergeStatus:       mergeStatus,
		Checks:            checksSummary,
		MergeQueue:        mergeQueueInfo,
		MergeQueueChecks:  mergeQueueChecks,
		LatestReviews:     reviews,
		TimelineItems:     timelineItems,
		Commits:           commits,
	}
}
