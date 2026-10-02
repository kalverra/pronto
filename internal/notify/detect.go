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

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
)

// defaultCheckerTimeout bounds one batched vanished-PR state lookup.
const defaultCheckerTimeout = 10 * time.Second

// Delta is everything one poll changed between two queue snapshots.
type Delta struct {
	// Events lists every trigger (Notify set only when the policy selected
	// it), then pr_added/pr_removed sorted by (repo, PR, type).
	Events []events.Event
	// Notifications are the policy-selected triggers, ready to deliver.
	Notifications []Notification
	// Boosted holds the PRs in the Focus or Priority tab: the ones the user
	// watches most closely, which the source refreshes a tier sooner.
	Boosted map[model.PRKey]bool
}

// DetectOption configures a single call to Detect or Seed.
type DetectOption func(*detectConfig)

type detectConfig struct {
	focusOverride FocusOverride
	now           time.Time
}

// WithFocusOverride supplies a manual focus override lookup for a detection run.
func WithFocusOverride(fo FocusOverride) DetectOption {
	return func(c *detectConfig) {
		c.focusOverride = fo
	}
}

// WithNow overrides the current timestamp for classification in a detection run.
func WithNow(now time.Time) DetectOption {
	return func(c *detectConfig) {
		c.now = now
	}
}

// Detector identifies notification-worthy transitions between pull request states.
type Detector struct {
	checker        PRStatusChecker
	checkerTimeout time.Duration
	filterBots     bool
	viewer         string
	assets         Assets
	clock          func() time.Time
	lastDetect     time.Time
	seenKeys       map[string]bool
	pendingChecks  map[model.PRKey]Observed
	mu             sync.Mutex

	policy Policy
	prio   PriorityPartitioner
	focus  FocusMatcher
}

// WithPolicy sets the notification policy used by Detect. Without one, no
// trigger notifies (a nil Policy subscribes no scope).
func WithPolicy(p Policy) DetectorOption {
	return func(d *Detector) {
		d.policy = p
	}
}

// WithPriority sets the priority tab partitioner used for queue classification.
func WithPriority(p PriorityPartitioner) DetectorOption {
	return func(d *Detector) {
		d.prio = p
	}
}

// WithFocus sets the auto-focus matcher used for queue classification.
func WithFocus(f FocusMatcher) DetectorOption {
	return func(d *Detector) {
		d.focus = f
	}
}

// WithStatusChecker configures the batched vanished PR checker.
func WithStatusChecker(c PRStatusChecker) DetectorOption {
	return func(d *Detector) {
		d.checker = c
	}
}

// NewDetector creates a new Detector with options.
func NewDetector(opts ...DetectorOption) *Detector {
	d := &Detector{
		checkerTimeout: defaultCheckerTimeout,
		seenKeys:       make(map[string]bool),
		pendingChecks:  make(map[model.PRKey]Observed),
		clock:          time.Now,
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.checkerTimeout <= 0 {
		d.checkerTimeout = defaultCheckerTimeout
	}
	return d
}

// SeenKeysCount returns the number of tracked deduplication keys.
func (d *Detector) SeenKeysCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seenKeys)
}

func (d *Detector) resolveDetectOptions(opts []DetectOption) detectConfig {
	var cfg detectConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// Seed records q as the baseline so its current state does not notify, and
// returns q's boosted PRs (see Delta.Boosted).
func (d *Detector) Seed(q model.Queue, opts ...DetectOption) map[model.PRKey]bool {
	obs := d.classify(q, d.resolveDetectOptions(opts))
	d.seedObserved(obs)
	return boostedOf(obs)
}

// Detect classifies both snapshots, reports every transition between them,
// marks the ones the policy selects, and diffs queue membership.
func (d *Detector) Detect(ctx context.Context, prev, curr model.Queue, opts ...DetectOption) Delta {
	cfg := d.resolveDetectOptions(opts)
	currObs := d.classify(curr, cfg)
	notes := d.detectChanges(ctx, d.classify(prev, cfg), currObs)

	selected := d.policy.Select(notes)
	wanted := make(map[noteKey]bool, len(selected))
	for _, n := range selected {
		wanted[keyOf(n)] = true
	}

	merged := make(map[model.PRKey]bool)
	evs := make([]events.Event, 0, len(notes))
	for _, n := range notes {
		if n.Trigger == TriggerPRMerged {
			merged[model.PRKey{Repo: n.Repo, Number: n.PRNumber}] = true
		}
		evs = append(evs, notificationEvent(n, wanted[keyOf(n)]))
	}
	evs = append(evs, diffEvents(prev, curr, merged)...)

	return Delta{
		Events:        evs,
		Notifications: selected,
		Boosted:       boostedOf(currObs),
	}
}

// classify places q's PRs into scopes using the detector's priority and
// focus rules, cfg's manual overrides, and cfg's clock (detector clock when
// unset).
func (d *Detector) classify(q model.Queue, cfg detectConfig) []Observed {
	now := cfg.now
	if now.IsZero() {
		now = d.clock()
	}
	return Classify(q, d.prio, d.focus, cfg.focusOverride, now)
}

// boostedOf returns the observed PRs in the Focus or Priority tab.
func boostedOf(obs []Observed) map[model.PRKey]bool {
	boosted := make(map[model.PRKey]bool)
	for _, o := range obs {
		for _, sc := range o.Scopes {
			if sc.Tab == TabFocus || sc.Tab == TabPriority {
				boosted[o.PR.Key()] = true
				break
			}
		}
	}
	return boosted
}

// seedObserved marks every trigger obs already satisfies as seen.
func (d *Detector) seedObserved(obs []Observed) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.lastDetect = d.clock()
	for _, o := range obs {
		pr := o.PR
		for _, sc := range o.Scopes {
			d.seenKeys[enteredKey(pr, sc)] = true
		}
		if pr.InMergeQueue() {
			d.seenKeys[mergeQueueKey(pr.RepoNameWithOwner, pr.Number)] = true
		}
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

// noteKey identifies one notification within a poll.
type noteKey struct {
	trigger Trigger
	repo    string
	pr      int
	entered Scope
}

func keyOf(n Notification) noteKey {
	k := noteKey{trigger: n.Trigger, repo: n.Repo, pr: n.PRNumber}
	if n.Entered != nil {
		k.entered = *n.Entered
	}
	return k
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

// applyAssets resolves n's image and sound, falling back to the default
// trigger icon when neither the caller nor configured assets set one.
func (d *Detector) applyAssets(n *Notification) {
	d.assets.Apply(n)
}

// detectChanges returns every notification-worthy transition between prev
// and curr, unfiltered by policy. Each notification carries the scopes a
// Policy evaluates it against.
func (d *Detector) detectChanges(ctx context.Context, prev, curr []Observed) []Notification {
	now := d.clock()
	d.mu.Lock()
	lastDetect := d.lastDetect
	d.lastDetect = now
	d.mu.Unlock()

	prevMap := make(map[model.PRKey]Observed, len(prev))
	for _, o := range prev {
		prevMap[o.PR.Key()] = o
	}

	currMap := make(map[model.PRKey]Observed, len(curr))
	for _, o := range curr {
		currMap[o.PR.Key()] = o
	}

	var results []Notification

	// 1. Inspect PRs currently open
	for _, currObs := range curr {
		prevObs, exists := prevMap[currObs.PR.Key()]
		results = append(results, d.detectPR(prevObs, currObs, exists, lastDetect)...)
	}

	// 2. Inspect disappeared PRs (potential merges or closes)
	d.mu.Lock()
	for key := range d.pendingChecks {
		if _, ok := currMap[key]; ok {
			delete(d.pendingChecks, key)
		}
	}

	vanished := make(map[model.PRKey]Observed)
	for key, prevObs := range prevMap {
		if _, ok := currMap[key]; !ok {
			vanished[key] = prevObs
		}
	}
	for key, pending := range d.pendingChecks {
		if _, ok := currMap[key]; !ok {
			vanished[key] = pending
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

	return results
}

// detectPR reports every transition between prev and curr for one PR that is
// present in the current poll. exists is false for a PR new to the queue.
func (d *Detector) detectPR(prevObs, currObs Observed, exists bool, lastDetect time.Time) []Notification {
	var results []Notification
	currPR, prevPR := currObs.PR, prevObs.PR
	scopes := unionScopes(prevObs.Scopes, currObs.Scopes)

	// Opened
	if !exists && !currPR.CreatedAt.IsZero() && currPR.CreatedAt.After(lastDetect) &&
		d.tryMarkSeen(openedKey(currPR.RepoNameWithOwner, currPR.Number)) {
		results = append(results, d.note(currPR, TriggerPROpened, currPR.URL, currPR.Author,
			fmt.Sprintf("✨ PR Opened (#%d)", currPR.Number),
			fmt.Sprintf("@%s opened %q (%s)", currPR.Author, currPR.Title, currPR.Key()),
			currObs.Scopes))
	}

	// Entered scopes
	results = append(results, d.detectEntered(prevObs, currObs)...)

	// CI Passed
	if !prevPR.Checks.IsPassing() && currPR.Checks.IsPassing() &&
		d.markCISeen(currPR.RepoNameWithOwner, currPR.Number, currPR.HeadRefOID, "passed") {
		results = append(results, d.note(currPR, TriggerCIPassed, checksURL(currPR.URL), currPR.Author,
			fmt.Sprintf("✅ CI Passed (#%d)", currPR.Number),
			fmt.Sprintf("Checks passed for %q (%s)", currPR.Title, currPR.Key()),
			scopes))
	}

	// CI Failed
	if !prevPR.Checks.IsFailing() && currPR.Checks.IsFailing() &&
		d.markCISeen(currPR.RepoNameWithOwner, currPR.Number, currPR.HeadRefOID, "failed") {
		results = append(results, d.note(currPR, TriggerCIFailed, failedCheckURL(currPR), currPR.Author,
			fmt.Sprintf("❌ CI Failed (#%d)", currPR.Number),
			fmt.Sprintf("CI failed for %q (%s)", currPR.Title, currPR.Key()),
			scopes))
	}

	// Conflicts
	if !prevPR.MergeStatus.HasConflict() && currPR.MergeStatus.HasConflict() {
		if d.tryMarkSeen(conflictKey(currPR.RepoNameWithOwner, currPR.Number)) {
			results = append(results, d.note(currPR, TriggerConflict, currPR.URL, currPR.Author,
				fmt.Sprintf("⚠️ Merge Conflict (#%d)", currPR.Number),
				fmt.Sprintf("Merge conflict in %q (%s)", currPR.Title, currPR.Key()),
				scopes))
		}
	} else if !currPR.MergeStatus.HasConflict() {
		d.clearConflictSeen(currPR.RepoNameWithOwner, currPR.Number)
	}

	if exists {
		results = append(results, d.detectMergeQueue(prevPR, currPR, scopes)...)

		// New commits
		if prevPR.HeadRefOID != "" && currPR.HeadRefOID != prevPR.HeadRefOID && !d.isViewer(currPR.Author) &&
			d.tryMarkSeen(commitKey(currPR.RepoNameWithOwner, currPR.Number, currPR.HeadRefOID)) {
			results = append(results, d.note(currPR, TriggerNewCommits, commitsURL(currPR.URL), currPR.Author,
				fmt.Sprintf("🔄 New Commits (#%d)", currPR.Number),
				fmt.Sprintf("New commits on %q (%s)", currPR.Title, currPR.Key()),
				scopes))
		}
	}

	// Reviews
	return append(results, d.detectPRReviews(currPR, prevPR, exists, scopes)...)
}

// note builds a Notification for pr and resolves its assets.
func (d *Detector) note(
	pr model.PullRequest,
	trigger Trigger,
	url, author, title, message string,
	scopes []Scope,
) Notification {
	n := Notification{
		Trigger:     trigger,
		PRNumber:    pr.Number,
		PRTitle:     pr.Title,
		Repo:        pr.RepoNameWithOwner,
		URL:         url,
		Author:      author,
		CommitOID:   pr.HeadRefOID,
		SubmittedAt: d.clock(),
		Title:       title,
		Message:     message,
		Scopes:      scopes,
	}
	d.applyAssets(&n)
	return n
}

func (d *Detector) isViewer(login string) bool {
	return d.viewer != "" && strings.EqualFold(d.viewer, login)
}

// detectEntered reports scopes curr occupies that prev did not.
func (d *Detector) detectEntered(prev, curr Observed) []Notification {
	var results []Notification
	for _, sc := range curr.Scopes {
		if slices.Contains(prev.Scopes, sc) {
			continue
		}
		if !d.tryMarkSeen(enteredKey(curr.PR, sc)) {
			continue
		}
		title, message := enteredText(sc, curr.PR)
		n := d.note(curr.PR, TriggerEntered, curr.PR.URL, curr.PR.Author, title, message, []Scope{sc})
		n.Entered = &sc
		results = append(results, n)
	}
	return results
}

func (d *Detector) detectMergeQueue(prevPR, currPR model.PullRequest, scopes []Scope) []Notification {
	repo, num := currPR.RepoNameWithOwner, currPR.Number
	switch {
	case !prevPR.InMergeQueue() && currPR.InMergeQueue():
		if d.tryMarkSeen(mergeQueueKey(repo, num)) {
			return []Notification{d.note(currPR, TriggerMergeQueueEntered, currPR.URL, currPR.Author,
				fmt.Sprintf("⏳ Merge Queue (#%d)", num),
				fmt.Sprintf("Entered merge queue: %q (%s)", currPR.Title, currPR.Key()),
				scopes)}
		}
	case prevPR.InMergeQueue() && !currPR.InMergeQueue():
		d.mu.Lock()
		delete(d.seenKeys, mergeQueueKey(repo, num))
		d.mu.Unlock()

		url := currPR.URL
		detail := ""
		switch {
		case currPR.Checks.IsFailing():
			detail = " (checks failed)"
			if fURL := failedCheckURL(currPR); fURL != "" {
				url = fURL
			}
		case currPR.MergeStatus.HasConflict():
			detail = " (conflicts)"
		}

		return []Notification{d.note(currPR, TriggerMergeQueueLeft, url, currPR.Author,
			fmt.Sprintf("🚨 Kicked out of Merge Queue (#%d)", num),
			fmt.Sprintf("Kicked out of merge queue%s: %q (%s)", detail, currPR.Title, currPR.Key()),
			scopes)}
	}
	return nil
}

// checksURL links to the PR's Checks tab; empty when the PR URL is unknown.
func checksURL(prURL string) string {
	if prURL == "" {
		return ""
	}
	return strings.TrimSuffix(prURL, "/") + "/checks"
}

// failedCheckURL links to the failed check itself, falling back to the
// Checks tab when no failed check reported a details URL.
func failedCheckURL(pr model.PullRequest) string {
	if pr.Checks.FailedURL != "" {
		return pr.Checks.FailedURL
	}
	return checksURL(pr.URL)
}

// reviewURL links to the review itself, falling back to the PR.
func reviewURL(pr model.PullRequest, rev model.Review) string {
	if rev.URL != "" {
		return rev.URL
	}
	return pr.URL
}

func (d *Detector) detectPRReviews(currPR, prevPR model.PullRequest, exists bool, scopes []Scope) []Notification {
	var results []Notification
	for _, rev := range currPR.LatestReviews {
		if rev.Author == currPR.Author {
			continue
		}
		if d.isViewer(rev.Author) {
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
		title := fmt.Sprintf("💬 Review on #%d", currPR.Number)
		switch rev.State {
		case "APPROVED":
			title = fmt.Sprintf("🟢 Approved (#%d)", currPR.Number)
		case "CHANGES_REQUESTED":
			title = fmt.Sprintf("🔴 Changes Requested (#%d)", currPR.Number)
		case "COMMENTED":
			title = fmt.Sprintf("💬 Commented (#%d)", currPR.Number)
		}

		n := Notification{
			Trigger:     TriggerReviewReceived,
			PRNumber:    currPR.Number,
			PRTitle:     currPR.Title,
			Repo:        currPR.RepoNameWithOwner,
			URL:         reviewURL(currPR, rev),
			Author:      rev.Author,
			ReviewState: rev.State,
			CommitOID:   rev.CommitOID,
			SubmittedAt: rev.SubmittedAt,
			Title:       title,
			Message: fmt.Sprintf(
				"@%s %s: %q (%s)",
				rev.Author,
				stateText,
				currPR.Title,
				currPR.Key(),
			),
			Scopes: scopes,
		}
		d.applyAssets(&n)
		results = append(results, n)
	}
	return results
}

func (d *Detector) detectVanishedPRs(ctx context.Context, vanished map[model.PRKey]Observed) []Notification {
	if d.checker == nil || len(vanished) == 0 {
		return nil
	}

	targets := make(map[model.PRKey]Observed, len(vanished))
	d.mu.Lock()
	for key, vObs := range vanished {
		if d.seenKeys[mergeKey(vObs.PR.RepoNameWithOwner, vObs.PR.Number)] ||
			d.seenKeys[closedKey(vObs.PR.RepoNameWithOwner, vObs.PR.Number)] {
			delete(d.pendingChecks, key)
			continue
		}
		targets[key] = vObs
	}
	d.mu.Unlock()

	if len(targets) == 0 {
		return nil
	}

	keys := make([]model.PRKey, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b model.PRKey) int {
		if c := strings.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		return cmp.Compare(a.Number, b.Number)
	})

	checkCtx, cancel := context.WithTimeout(ctx, d.checkerTimeout)
	states, err := d.checker(checkCtx, keys)
	cancel()

	d.mu.Lock()
	for key, obs := range targets {
		if err != nil {
			// Retry the whole lookup next poll. A PR missing from a
			// successful lookup (deleted, access lost) is dropped instead:
			// retrying cannot help.
			d.pendingChecks[key] = obs
		} else {
			delete(d.pendingChecks, key)
		}
	}
	d.mu.Unlock()
	if err != nil {
		return nil
	}

	var results []Notification
	for _, key := range keys {
		pr := targets[key].PR
		scopes := targets[key].Scopes
		var n Notification
		switch states[key] {
		case PRStateMerged:
			if !pr.IsDefaultBranch() {
				continue
			}
			if !d.tryMarkSeen(mergeKey(pr.RepoNameWithOwner, pr.Number)) {
				continue
			}
			targetBranch := pr.BaseRefName
			if targetBranch == "" {
				targetBranch = pr.DefaultBranch
			}
			msg := fmt.Sprintf("Merged: %q (%s)", pr.Title, pr.Key())
			if targetBranch != "" {
				msg = fmt.Sprintf("Merged to %s: %q (%s)", targetBranch, pr.Title, pr.Key())
			}
			n = d.note(pr, TriggerPRMerged, pr.URL, pr.Author,
				fmt.Sprintf("🟣 PR Merged (#%d)", pr.Number),
				msg,
				scopes)
			n.CommitOID = ""
		case PRStateClosed:
			if !d.tryMarkSeen(closedKey(pr.RepoNameWithOwner, pr.Number)) {
				continue
			}
			n = d.note(pr, TriggerPRClosed, pr.URL, pr.Author,
				fmt.Sprintf("⚪ PR Closed (#%d)", pr.Number),
				fmt.Sprintf("Closed: %q (%s)", pr.Title, pr.Key()),
				scopes)
			n.CommitOID = ""
		default:
			// Still open (it left the queue; pr_removed covers that) or
			// unknown to GitHub.
			continue
		}
		results = append(results, n)
	}
	return results
}

// unionScopes returns the scopes in a followed by those in b not already present.
func unionScopes(a, b []Scope) []Scope {
	out := slices.Clone(a)
	for _, sc := range b {
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	return out
}

// commitsURL links to the PR's Commits tab; empty when the PR URL is unknown.
func commitsURL(prURL string) string {
	if prURL == "" {
		return ""
	}
	return strings.TrimSuffix(prURL, "/") + "/commits"
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

func closedKey(repo string, num int) string {
	return fmt.Sprintf("closed:%s", model.PRKey{Repo: repo, Number: num})
}

func openedKey(repo string, num int) string {
	return fmt.Sprintf("opened:%s", model.PRKey{Repo: repo, Number: num})
}

func mergeQueueKey(repo string, num int) string {
	return fmt.Sprintf("mq:%s", model.PRKey{Repo: repo, Number: num})
}

func commitKey(repo string, num int, oid string) string {
	return fmt.Sprintf("commit:%s:%s", model.PRKey{Repo: repo, Number: num}, oid)
}

func enteredKey(pr model.PullRequest, sc Scope) string {
	return fmt.Sprintf("entered:%s:%s:%s:%s", pr.Key(), sc.Tab, sc.Section, pr.HeadRefOID)
}
