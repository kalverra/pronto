package source

import (
	"iter"
	"sync/atomic"
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// schedulerConfig holds the pacing knobs set through GraphQLSourceOptions.
type schedulerConfig struct {
	discoveryInterval  time.Duration
	hotInterval        time.Duration
	idleInterval       time.Duration
	staleActivityAfter time.Duration
	maxReuseAge        time.Duration
}

// defaultSchedulerConfig is the single source of pacing defaults.
func defaultSchedulerConfig() schedulerConfig {
	return schedulerConfig{
		discoveryInterval:  DefaultDiscoveryInterval,
		hotInterval:        DefaultHotInterval,
		idleInterval:       DefaultIdleInterval,
		staleActivityAfter: defaultStaleActivityAfter,
		maxReuseAge:        defaultMaxReuseAge,
	}
}

// scheduler owns the source's pacing decisions: activity tiers, per-PR
// reuse deadlines, discovery backoff, and the NextFetch deadline. Every
// factor argument is the budget stretch (1, or lowBudgetFactor when the
// hourly GraphQL budget runs low).
//
// Callers serialize all methods (GraphQLSource holds fetchMu) except
// nextFetchAt, which is safe to call concurrently.
type scheduler struct {
	cfg           schedulerConfig
	quietStreak   int
	lastDiscovery time.Time
	nextDiscovery time.Time
	// nextFetch is the UnixNano time nextFetchAt reports; 0 before any tick.
	nextFetch atomic.Int64
}

// nextFetchAt reports the next planned fetch: rateLimitUntil when limited,
// else the deadline recorded by the last tick, else now.
func (s *scheduler) nextFetchAt(now, rateLimitUntil time.Time, limited bool) time.Time {
	if limited {
		return rateLimitUntil
	}
	if n := s.nextFetch.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return now
}

// discoveryDue reports whether discovery should run on this tick.
func (s *scheduler) discoveryDue(now time.Time, force bool) bool {
	return force || s.lastDiscovery.IsZero() || !now.Before(s.nextDiscovery)
}

// recordDiscovery sets the next discovery deadline after one ran: the base
// interval while anything is changing, backing off toward the idle interval
// after quietDiscoveries unchanged ones in a row.
func (s *scheduler) recordDiscovery(now time.Time, changed bool, factor int) (quietStreak int, wait time.Duration) {
	if changed {
		s.quietStreak = 0
	} else {
		s.quietStreak++
	}
	wait = discoveryBackoff(s.cfg.discoveryInterval, s.cfg.idleInterval, s.quietStreak) * time.Duration(factor)
	s.lastDiscovery = now
	s.nextDiscovery = now.Add(wait)
	return s.quietStreak, wait
}

// tierFor places pr in its activity tier at now.
func (s *scheduler) tierFor(pr model.PullRequest, now time.Time, boosted bool) tier {
	return classify(pr, now, s.cfg.staleActivityAfter, boosted)
}

// reuseFor is how long a PR hydrated in tier t stays fresh enough to skip
// re-hydration. Stale PRs are reused until their fingerprint moves (ok is
// false: no deadline). Non-hot tiers are jittered per PR so PRs hydrated
// together don't all come due on the same tick.
func (s *scheduler) reuseFor(pr model.PullRequest, t tier) (time.Duration, bool) {
	var base time.Duration
	switch t {
	case tierHot:
		return s.cfg.hotInterval, true
	case tierActive:
		base = activeReuse
	case tierRecent:
		base = recentReuse
	case tierQuiet:
		base = s.cfg.maxReuseAge
	default:
		return 0, false
	}
	return jitter(base, pr.RepoNameWithOwner, pr.Number), true
}

// dueAt reports when tracked PR t next needs hydration, scaled by factor.
// ok is false when the PR is only refreshed by a fingerprint change.
func (s *scheduler) dueAt(t *trackedPR, now time.Time, boosted bool, factor int) (time.Time, bool) {
	reuse, ok := s.reuseFor(t.pr, s.tierFor(t.pr, now, boosted))
	if !ok {
		return time.Time{}, false
	}
	return t.hydratedAt.Add(reuse * time.Duration(factor)), true
}

// isDue reports whether tracked PR t needs hydration this tick. A forced
// tick refreshes every hot PR regardless of when it was last hydrated.
func (s *scheduler) isDue(t *trackedPR, now time.Time, boosted, force bool, factor int) bool {
	if force && s.tierFor(t.pr, now, boosted) == tierHot {
		return true
	}
	due, ok := s.dueAt(t, now, boosted, factor)
	return ok && !now.Before(due)
}

// anyHot reports whether any tracked PR is in the hot tier.
func (s *scheduler) anyHot(tracked iter.Seq2[model.PRKey, *trackedPR], now time.Time) bool {
	for _, t := range tracked {
		if s.tierFor(t.pr, now, false) == tierHot {
			return true
		}
	}
	return false
}

// scheduleNext records the NextFetch deadline after a successful tick: the
// earlier of the next discovery and the soonest per-PR deadline, never
// sooner than the shorter of the hot and discovery intervals. boosted is
// keyed by the tracked key, matching isDue.
func (s *scheduler) scheduleNext(
	now time.Time,
	tracked iter.Seq2[model.PRKey, *trackedPR],
	boosted map[model.PRKey]bool,
	factor int,
) {
	next := s.nextDiscovery
	for key, t := range tracked {
		if due, ok := s.dueAt(t, now, boosted[key], factor); ok && due.Before(next) {
			next = due
		}
	}
	floor := now.Add(min(s.cfg.hotInterval, s.cfg.discoveryInterval) * time.Duration(factor))
	if next.Before(floor) {
		next = floor
	}
	s.nextFetch.Store(next.UnixNano())
}

// scheduleRetry records the NextFetch deadline after a failed tick: one base
// discovery interval out, so a persistent failure polls no faster than the
// steady state. A due discovery stays due.
func (s *scheduler) scheduleRetry(now time.Time, factor int) {
	s.nextFetch.Store(now.Add(s.cfg.discoveryInterval * time.Duration(factor)).UnixNano())
}
