package notify

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/kalverra/pronto/internal/model"
)

const (
	defaultCheckerTimeout     = 5 * time.Second
	defaultCheckerParallelism = 4
)

// Detector identifies notification-worthy transitions between pull request states.
type Detector struct {
	checker            PRStatusChecker
	checkerTimeout     time.Duration
	checkerParallelism int
	filterBots         bool
	imageResolver      ImageResolver
	soundResolver      SoundResolver
	seenKeys           map[string]bool
	pendingChecks      map[model.PRKey]model.PullRequest
	mu                 sync.Mutex
}

// NewDetector creates a new Detector with options.
func NewDetector(checker PRStatusChecker, opts ...DetectorOption) *Detector {
	d := &Detector{
		checker:            checker,
		checkerTimeout:     defaultCheckerTimeout,
		checkerParallelism: defaultCheckerParallelism,
		seenKeys:           make(map[string]bool),
		pendingChecks:      make(map[model.PRKey]model.PullRequest),
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.checkerTimeout <= 0 {
		d.checkerTimeout = defaultCheckerTimeout
	}
	if d.checkerParallelism <= 0 {
		d.checkerParallelism = defaultCheckerParallelism
	}
	return d
}

// SeenKeysCount returns the number of tracked deduplication keys.
func (d *Detector) SeenKeysCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seenKeys)
}

// Seed records initial baseline pull requests so startup does not notify.
func (d *Detector) Seed(prs []model.PullRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, pr := range prs {
		if pr.Checks.IsPassing() {
			d.seenKeys[ciKey(pr.RepoNameWithOwner, pr.Number, pr.HeadRefOID, "passed")] = true
		}
		if pr.Checks.IsFailing() {
			d.seenKeys[ciKey(pr.RepoNameWithOwner, pr.Number, pr.HeadRefOID, "failed")] = true
		}
		if pr.MergeStatus.HasConflict() {
			d.seenKeys[conflictKey(pr.RepoNameWithOwner, pr.Number)] = true
		}
		for _, r := range pr.LatestReviews {
			d.seenKeys[reviewKey(pr.RepoNameWithOwner, pr.Number, r.Author, r.State, r.SubmittedAt)] = true
		}
	}
}

func (d *Detector) tryMarkSeen(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seenKeys[key] {
		return false
	}
	d.seenKeys[key] = true
	return true
}

func (d *Detector) markCISeen(repo string, num int, oid, state string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := ciKey(repo, num, oid, state)
	if d.seenKeys[key] {
		return false
	}
	d.seenKeys[key] = true
	opposite := "failed"
	if state == "failed" {
		opposite = "passed"
	}
	delete(d.seenKeys, ciKey(repo, num, oid, opposite))
	return true
}

func (d *Detector) applyImage(n *Notification) {
	if d.imageResolver != nil {
		n.ImagePath = d.imageResolver(*n)
	}
	if n.ImagePath == "" {
		n.ImagePath = defaultTriggerImage(*n)
	}
}

func (d *Detector) applySound(n *Notification) {
	if d.soundResolver != nil {
		n.SoundPath = d.soundResolver(*n)
	}
}

// DetectMineChanges inspects changes to authored pull requests and returns new notifications.
func (d *Detector) DetectMineChanges(ctx context.Context, prev, curr []model.PullRequest) ([]Notification, error) {
	prevMap := make(map[model.PRKey]model.PullRequest, len(prev))
	for _, pr := range prev {
		prevMap[pr.Key()] = pr
	}

	currMap := make(map[model.PRKey]model.PullRequest, len(curr))
	for _, pr := range curr {
		currMap[pr.Key()] = pr
	}

	var results []Notification

	// 1. Inspect PRs currently open
	for _, currPR := range curr {
		key := currPR.Key()
		prevPR, exists := prevMap[key]

		// CI Passed
		if !prevPR.Checks.IsPassing() && currPR.Checks.IsPassing() {
			if d.markCISeen(currPR.RepoNameWithOwner, currPR.Number, currPR.HeadRefOID, "passed") {
				n := Notification{
					Trigger:   TriggerCIPassed,
					PRNumber:  currPR.Number,
					PRTitle:   currPR.Title,
					Repo:      currPR.RepoNameWithOwner,
					URL:       currPR.URL,
					Author:    currPR.Author,
					CommitOID: currPR.HeadRefOID,
					Title:     fmt.Sprintf("CI Passed (#%d)", currPR.Number),
					Message: fmt.Sprintf(
						"Checks passed for %q (%s)",
						currPR.Title,
						currPR.Key(),
					),
				}
				d.applyImage(&n)
				d.applySound(&n)
				results = append(results, n)
			}
		}

		// CI Failed
		if !prevPR.Checks.IsFailing() && currPR.Checks.IsFailing() {
			if d.markCISeen(currPR.RepoNameWithOwner, currPR.Number, currPR.HeadRefOID, "failed") {
				n := Notification{
					Trigger:   TriggerCIFailed,
					PRNumber:  currPR.Number,
					PRTitle:   currPR.Title,
					Repo:      currPR.RepoNameWithOwner,
					URL:       currPR.URL,
					Author:    currPR.Author,
					CommitOID: currPR.HeadRefOID,
					Title:     fmt.Sprintf("CI Failed (#%d)", currPR.Number),
					Message: fmt.Sprintf(
						"CI failed for %q (%s)",
						currPR.Title,
						currPR.Key(),
					),
				}
				d.applyImage(&n)
				d.applySound(&n)
				results = append(results, n)
			}
		}

		// Conflicts
		if !prevPR.MergeStatus.HasConflict() && currPR.MergeStatus.HasConflict() {
			if d.tryMarkSeen(conflictKey(currPR.RepoNameWithOwner, currPR.Number)) {
				n := Notification{
					Trigger:   TriggerConflict,
					PRNumber:  currPR.Number,
					PRTitle:   currPR.Title,
					Repo:      currPR.RepoNameWithOwner,
					URL:       currPR.URL,
					Author:    currPR.Author,
					CommitOID: currPR.HeadRefOID,
					Title:     fmt.Sprintf("Merge Conflict (#%d)", currPR.Number),
					Message: fmt.Sprintf(
						"Merge conflict in %q (%s)",
						currPR.Title,
						currPR.Key(),
					),
				}
				d.applyImage(&n)
				d.applySound(&n)
				results = append(results, n)
			}
		} else if !currPR.MergeStatus.HasConflict() {
			d.clearConflictSeen(currPR.RepoNameWithOwner, currPR.Number)
		}

		// Reviews
		results = append(results, d.detectPRReviews(currPR, prevPR, exists)...)
	}

	// 2. Inspect disappeared PRs (potential merges)
	d.mu.Lock()
	for key := range d.pendingChecks {
		if _, ok := currMap[key]; ok {
			delete(d.pendingChecks, key)
		}
	}

	vanished := make(map[model.PRKey]model.PullRequest)
	for key, prevPR := range prevMap {
		if _, ok := currMap[key]; !ok {
			vanished[key] = prevPR
		}
	}
	for key, pendingPR := range d.pendingChecks {
		if _, ok := currMap[key]; !ok {
			vanished[key] = pendingPR
		}
	}
	d.mu.Unlock()

	results = append(results, d.detectVanishedPRs(ctx, vanished)...)

	// Bound seenKeys: prune keys for PRs no longer in either prev nor curr (and not pending).
	active := make(map[model.PRKey]bool, len(prevMap)+len(currMap)+len(d.pendingChecks))
	for k := range prevMap {
		active[k] = true
	}
	for k := range currMap {
		active[k] = true
	}
	d.mu.Lock()
	for k := range d.pendingChecks {
		active[k] = true
	}
	d.mu.Unlock()
	d.pruneSeenKeys(active)

	return results, nil
}

func (d *Detector) detectPRReviews(currPR, prevPR model.PullRequest, exists bool) []Notification {
	var results []Notification
	for _, rev := range currPR.LatestReviews {
		if rev.Author == currPR.Author {
			continue
		}
		if d.filterBots && model.IsBotLogin(rev.Author) {
			continue
		}
		if exists && hasReview(prevPR.LatestReviews, rev) {
			continue
		}
		rKey := reviewKey(currPR.RepoNameWithOwner, currPR.Number, rev.Author, rev.State, rev.SubmittedAt)
		if !d.tryMarkSeen(rKey) {
			continue
		}

		stateText := formatReviewState(rev.State)
		n := Notification{
			Trigger:     TriggerReviewReceived,
			PRNumber:    currPR.Number,
			PRTitle:     currPR.Title,
			Repo:        currPR.RepoNameWithOwner,
			URL:         currPR.URL,
			Author:      rev.Author,
			ReviewState: rev.State,
			CommitOID:   rev.CommitOID,
			SubmittedAt: rev.SubmittedAt,
			Title:       fmt.Sprintf("Review on #%d", currPR.Number),
			Message: fmt.Sprintf(
				"@%s %s: %q (%s)",
				rev.Author,
				stateText,
				currPR.Title,
				currPR.Key(),
			),
		}
		d.applyImage(&n)
		d.applySound(&n)
		results = append(results, n)
	}
	return results
}

func (d *Detector) detectVanishedPRs(ctx context.Context, vanished map[model.PRKey]model.PullRequest) []Notification {
	if d.checker == nil || len(vanished) == 0 {
		return nil
	}

	type checkTarget struct {
		key  model.PRKey
		pr   model.PullRequest
		mKey string
	}

	var targets []checkTarget
	d.mu.Lock()
	for key, vPR := range vanished {
		mKey := mergeKey(vPR.RepoNameWithOwner, vPR.Number)
		if d.seenKeys[mKey] {
			delete(d.pendingChecks, key)
			continue
		}
		targets = append(targets, checkTarget{key: key, pr: vPR, mKey: mKey})
	}
	d.mu.Unlock()

	if len(targets) == 0 {
		return nil
	}

	var (
		resMu   sync.Mutex
		results []Notification
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(d.checkerParallelism)

	for _, target := range targets {
		g.Go(func() error {
			checkCtx, cancel := context.WithTimeout(gctx, d.checkerTimeout)
			defer cancel()

			merged, err := d.checker(checkCtx, target.pr.RepoNameWithOwner, target.pr.Number)
			if err != nil {
				d.mu.Lock()
				d.pendingChecks[target.key] = target.pr
				d.mu.Unlock()
				return nil
			}

			d.mu.Lock()
			delete(d.pendingChecks, target.key)
			d.mu.Unlock()

			if merged && d.tryMarkSeen(target.mKey) {
				n := Notification{
					Trigger:  TriggerPRMerged,
					PRNumber: target.pr.Number,
					PRTitle:  target.pr.Title,
					Repo:     target.pr.RepoNameWithOwner,
					URL:      target.pr.URL,
					Author:   target.pr.Author,
					Title:    fmt.Sprintf("PR Merged (#%d)", target.pr.Number),
					Message: fmt.Sprintf(
						"Merged: %q (%s)",
						target.pr.Title,
						target.pr.Key(),
					),
				}
				d.applyImage(&n)
				d.applySound(&n)

				resMu.Lock()
				results = append(results, n)
				resMu.Unlock()
			}
			return nil
		})
	}

	_ = g.Wait()
	slices.SortFunc(results, func(a, b Notification) int {
		if c := strings.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		return cmp.Compare(a.PRNumber, b.PRNumber)
	})
	return results
}

func prKeyFromSeenKey(k string) (model.PRKey, bool) {
	_, after, ok := strings.Cut(k, ":")
	if !ok {
		return model.PRKey{}, false
	}
	rest := after
	before0, after0, ok0 := strings.Cut(rest, "#")
	if !ok0 {
		return model.PRKey{}, false
	}
	repo := before0
	numStr := after0
	if idxNext := strings.IndexByte(numStr, ':'); idxNext >= 0 {
		numStr = numStr[:idxNext]
	}
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return model.PRKey{}, false
	}
	return model.PRKey{Repo: repo, Number: num}, true
}

func (d *Detector) pruneSeenKeys(active map[model.PRKey]bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k := range d.seenKeys {
		pk, ok := prKeyFromSeenKey(k)
		if !ok || !active[pk] {
			delete(d.seenKeys, k)
		}
	}
}

func hasReview(reviews []model.Review, target model.Review) bool {
	for _, r := range reviews {
		if r.Author == target.Author && r.State == target.State && r.SubmittedAt.Equal(target.SubmittedAt) {
			return true
		}
	}
	return false
}

func formatReviewState(state string) string {
	switch state {
	case "APPROVED":
		return "Approved"
	case "CHANGES_REQUESTED":
		return "Changes requested"
	case "COMMENTED":
		return "Commented"
	default:
		return state
	}
}

func ciKey(repo string, num int, oid, state string) string {
	if oid == "" {
		oid = "head"
	}
	return fmt.Sprintf("ci:%s:%s:%s", model.PRKey{Repo: repo, Number: num}, oid, state)
}

func reviewKey(repo string, num int, author, state string, submittedAt time.Time) string {
	if !submittedAt.IsZero() {
		return fmt.Sprintf(
			"review:%s:%s:%s:%d",
			model.PRKey{Repo: repo, Number: num},
			author,
			state,
			submittedAt.UnixNano(),
		)
	}
	return fmt.Sprintf("review:%s:%s:%s", model.PRKey{Repo: repo, Number: num}, author, state)
}

func mergeKey(repo string, num int) string {
	return fmt.Sprintf("merge:%s", model.PRKey{Repo: repo, Number: num})
}

func conflictKey(repo string, num int) string {
	return fmt.Sprintf("conflict:%s", model.PRKey{Repo: repo, Number: num})
}

func (d *Detector) clearConflictSeen(repo string, num int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.seenKeys, conflictKey(repo, num))
}
