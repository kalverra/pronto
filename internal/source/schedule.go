package source

import (
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// Pacing defaults. The discovery interval is the steady-state search cadence;
// the hot interval is the fast lane for PRs whose state is actively moving;
// the idle interval caps how far discovery backs off when nothing changes.
const (
	DefaultDiscoveryInterval = 60 * time.Second
	DefaultHotInterval       = 20 * time.Second
	DefaultIdleInterval      = 3 * time.Minute
)

const (
	// quietDiscoveries is how many consecutive unchanged discoveries run at
	// the base interval before backoff toward the idle interval begins.
	quietDiscoveries = 3
	// lowBudgetFraction is the remaining share of the hourly GraphQL budget
	// below which every interval stretches by lowBudgetFactor and the hot
	// lane slows to match.
	lowBudgetFraction = 0.2
	lowBudgetFactor   = 3

	// hotPushWindow is how long after a push a PR whose checks have not yet
	// settled stays hot (workflows dispatching, required checks appearing).
	hotPushWindow = 10 * time.Minute
	// hotRunningWindow bounds how long running checks keep a PR hot, so a
	// hung or never-scheduled job cannot pin it to the fast lane forever.
	hotRunningWindow = 2 * time.Hour
	activeWindow     = time.Hour
	recentWindow     = 24 * time.Hour
	activeReuse      = 2 * time.Minute
	recentReuse      = 10 * time.Minute
)

// tier buckets a tracked PR by how likely it is to change soon. Lower is
// hotter.
type tier int

const (
	tierHot tier = iota
	tierActive
	tierRecent
	tierQuiet
	tierStale
)

func (t tier) String() string {
	switch t {
	case tierHot:
		return "hot"
	case tierActive:
		return "active"
	case tierRecent:
		return "recent"
	case tierQuiet:
		return "quiet"
	default:
		return "stale"
	}
}

// classify places pr in a tier from its own state at now. Boosted PRs
// (focused or Priority) move one tier hotter, but only live signals make a
// PR hot.
func classify(pr model.PullRequest, now time.Time, staleAfter time.Duration, boosted bool) tier {
	headAge := now.Sub(headCommitDate(pr))
	unknownMerge := !definite(pr.Mergeable) || !definite(pr.MergeStateStatus)
	switch {
	case pr.Partial, pr.IsInMergeQueue:
		return tierHot
	case pr.Checks.IsRunning() && now.Sub(checksStarted(pr)) < hotRunningWindow:
		return tierHot
	case !pr.Checks.IsSettled() && headAge < hotPushWindow:
		return tierHot
	case unknownMerge && now.Sub(lastActivity(pr)) < activeWindow:
		return tierHot
	}

	t := tierStale
	switch age := now.Sub(lastActivity(pr)); {
	case age < activeWindow:
		t = tierActive
	case age < recentWindow:
		t = tierRecent
	case age < staleAfter:
		t = tierQuiet
	}
	if boosted && t > tierActive {
		t--
	}
	if unknownMerge && t > tierActive {
		// GitHub computes merge state lazily; the next look usually has it.
		t = tierActive
	}
	return t
}

// definite reports whether a GitHub merge field has been computed.
func definite(v string) bool {
	return v != "" && v != "UNKNOWN"
}

// headCommitDate is when the PR's head commit was made, falling back to its
// last update when the commit is unknown.
func headCommitDate(pr model.PullRequest) time.Time {
	if len(pr.Commits) > 0 && !pr.Commits[0].CommittedDate.IsZero() {
		return pr.Commits[0].CommittedDate
	}
	return pr.UpdatedAt
}

// checksStarted is when the head commit's current check runs began, falling
// back to the head commit date when unknown.
func checksStarted(pr model.PullRequest) time.Time {
	if pr.Checks.StartedAt != nil && !pr.Checks.StartedAt.IsZero() {
		return *pr.Checks.StartedAt
	}
	return headCommitDate(pr)
}

// lastActivity is the latest of the PR's update, head commit, and reviews.
func lastActivity(pr model.PullRequest) time.Time {
	latest := pr.UpdatedAt
	if c := headCommitDate(pr); c.After(latest) {
		latest = c
	}
	for _, r := range pr.LatestReviews {
		if r.SubmittedAt.After(latest) {
			latest = r.SubmittedAt
		}
	}
	return latest
}

// reuseFor is how long a PR hydrated in tier t stays fresh enough to skip
// re-hydration. Stale PRs are reused until their fingerprint moves (ok is
// false: no deadline). Non-hot tiers are jittered per PR so PRs hydrated
// together don't all come due on the same tick.
func (s *GraphQLSource) reuseFor(pr model.PullRequest, t tier) (time.Duration, bool) {
	var base time.Duration
	switch t {
	case tierHot:
		return s.hotInterval, true
	case tierActive:
		base = activeReuse
	case tierRecent:
		base = recentReuse
	case tierQuiet:
		base = s.maxReuseAge
		if base <= 0 {
			base = defaultMaxReuseAge
		}
	default:
		return 0, false
	}
	return jitter(base, pr.RepoNameWithOwner, pr.Number), true
}

// jitter scales d by a stable per-PR factor in [0.75, 1.25].
func jitter(d time.Duration, repo string, num int) time.Duration {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s#%d", repo, num)
	frac := 0.75 + 0.50*(float64(h.Sum32())/float64(math.MaxUint32))
	return time.Duration(float64(d) * frac)
}

// dueAt reports when tracked PR t next needs hydration, scaled by the budget
// factor. ok is false when the PR is only refreshed by a fingerprint change.
func (s *GraphQLSource) dueAt(t *trackedPR, now time.Time, boosted bool, factor int) (time.Time, bool) {
	reuse, ok := s.reuseFor(t.pr, classify(t.pr, now, s.staleActivityAfter, boosted))
	if !ok {
		return time.Time{}, false
	}
	return t.hydratedAt.Add(reuse * time.Duration(factor)), true
}

// fingerprintChanged reports whether discovery shows the PR moved since pr
// was hydrated. Undetermined values from search (UNKNOWN merge state, empty
// review decision) never count as a change.
func fingerprintChanged(id rawIdentity, pr model.PullRequest) bool {
	switch {
	case !id.UpdatedAt.Equal(pr.UpdatedAt),
		id.HeadRefOID != pr.HeadRefOID,
		id.IsInMergeQueue != pr.IsInMergeQueue,
		id.IsDraft != pr.IsDraft:
		return true
	case definite(id.FPMergeable) && id.FPMergeable != pr.Mergeable,
		definite(id.FPMergeStateStatus) && id.FPMergeStateStatus != pr.MergeStateStatus,
		id.FPReviewDecision != "" && id.FPReviewDecision != pr.ReviewDecision:
		return true
	}
	return id.rollupState() != pr.Checks.State
}

// discoveryBackoff returns the wait before the next discovery after quiet
// consecutive unchanged discoveries: base until quietDiscoveries, then
// doubling up to idle.
func discoveryBackoff(base, idle time.Duration, quiet int) time.Duration {
	if quiet <= quietDiscoveries {
		return base
	}
	shift := min(quiet-quietDiscoveries, 16)
	return min(base<<shift, max(idle, base))
}
