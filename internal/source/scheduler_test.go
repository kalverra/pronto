package source

import (
	"iter"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
)

var schedNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// trackedSeq yields entries in order, keyed like GraphQLSource.trackedPRs.
func trackedSeq(entries ...trackedEntry) iter.Seq2[model.PRKey, *trackedPR] {
	return func(yield func(model.PRKey, *trackedPR) bool) {
		for _, e := range entries {
			if !yield(e.key, e.t) {
				return
			}
		}
	}
}

type trackedEntry struct {
	key model.PRKey
	t   *trackedPR
}

func entry(t *trackedPR) trackedEntry { return trackedEntry{key: t.pr.Key(), t: t} }

// settledPR is a mergeable PR with settled checks whose last activity was age
// before schedNow.
func settledPR(num int, age time.Duration) model.PullRequest {
	at := schedNow.Add(-age)
	return model.PullRequest{
		RepoNameWithOwner: "org/repo", Number: num,
		UpdatedAt: at, Commits: []model.Commit{{OID: "a", CommittedDate: at}},
		Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN",
		Checks: model.ChecksSummary{State: "SUCCESS", Total: 1, Done: 1},
	}
}

func hotPR(num int) model.PullRequest {
	pr := settledPR(num, 48*time.Hour)
	pr.IsInMergeQueue = true
	return pr
}

func newTestScheduler() *scheduler {
	return &scheduler{cfg: defaultSchedulerConfig()}
}

func TestSchedulerConfig_DefaultsAndOptions(t *testing.T) {
	t.Parallel()

	assert.Equal(t, schedulerConfig{
		discoveryInterval:  DefaultDiscoveryInterval,
		hotInterval:        DefaultHotInterval,
		idleInterval:       DefaultIdleInterval,
		staleActivityAfter: model.StaleThreshold,
		maxReuseAge:        defaultMaxReuseAge,
	}, NewGraphQLSource(nil).sched.cfg)

	s := NewGraphQLSource(nil,
		WithDiscoveryInterval(time.Second),
		WithHotInterval(2*time.Second),
		WithIdleInterval(3*time.Second),
		WithStaleActivityAfter(4*time.Second),
		WithMaxReuseAge(5*time.Second),
		WithHotInterval(0), // non-positive values keep the prior setting
	)
	assert.Equal(t, schedulerConfig{
		discoveryInterval:  time.Second,
		hotInterval:        2 * time.Second,
		idleInterval:       3 * time.Second,
		staleActivityAfter: 4 * time.Second,
		maxReuseAge:        5 * time.Second,
	}, s.sched.cfg)
}

func TestScheduler_NextFetchAt(t *testing.T) {
	t.Parallel()

	s := newTestScheduler()
	assert.Equal(t, schedNow, s.nextFetchAt(schedNow, time.Time{}, false), "before any tick: now")

	until := schedNow.Add(15 * time.Minute)
	assert.Equal(t, until, s.nextFetchAt(schedNow, until, true), "rate limit wins")

	s.scheduleRetry(schedNow, 1)
	assert.Equal(t, schedNow.Add(DefaultDiscoveryInterval), s.nextFetchAt(schedNow, time.Time{}, false).UTC())

	s.scheduleRetry(schedNow, lowBudgetFactor)
	assert.Equal(
		t,
		schedNow.Add(lowBudgetFactor*DefaultDiscoveryInterval),
		s.nextFetchAt(schedNow, time.Time{}, false).UTC(),
		"retry stretches with the budget factor",
	)
}

func TestScheduler_RecordDiscovery(t *testing.T) {
	t.Parallel()

	s := &scheduler{cfg: schedulerConfig{discoveryInterval: time.Minute, idleInterval: 3 * time.Minute}}
	assert.True(t, s.discoveryDue(schedNow, false), "first discovery is always due")

	streak, wait := s.recordDiscovery(schedNow, false, 1)
	assert.Equal(t, 1, streak)
	assert.Equal(t, time.Minute, wait)
	assert.False(t, s.discoveryDue(schedNow.Add(30*time.Second), false))
	assert.True(t, s.discoveryDue(schedNow.Add(30*time.Second), true), "force makes discovery due")
	assert.True(t, s.discoveryDue(schedNow.Add(time.Minute), false))

	for range 4 {
		streak, wait = s.recordDiscovery(schedNow, false, 1)
	}
	assert.Equal(t, 5, streak)
	assert.Equal(t, 3*time.Minute, wait, "quiet streak backs off to idle")

	streak, wait = s.recordDiscovery(schedNow, true, lowBudgetFactor)
	assert.Equal(t, 0, streak, "a change resets the streak")
	assert.Equal(t, lowBudgetFactor*time.Minute, wait, "wait stretches with the budget factor")
	assert.False(t, s.discoveryDue(schedNow.Add(2*time.Minute), false))
	assert.True(t, s.discoveryDue(schedNow.Add(3*time.Minute), false))
}

func TestScheduler_DueAt(t *testing.T) {
	t.Parallel()

	s := newTestScheduler()
	hydrated := schedNow.Add(-time.Minute)
	hot := &trackedPR{pr: hotPR(1), hydratedAt: hydrated}
	quiet := &trackedPR{pr: settledPR(2, 48*time.Hour), hydratedAt: hydrated}
	stale := &trackedPR{pr: settledPR(3, 60*24*time.Hour), hydratedAt: hydrated}

	due, ok := s.dueAt(hot, schedNow, false, 1)
	require.True(t, ok)
	assert.Equal(t, hydrated.Add(DefaultHotInterval), due, "hot is not jittered")

	due, ok = s.dueAt(hot, schedNow, false, lowBudgetFactor)
	require.True(t, ok)
	assert.Equal(t, hydrated.Add(lowBudgetFactor*DefaultHotInterval), due)

	due, ok = s.dueAt(quiet, schedNow, false, 1)
	require.True(t, ok)
	assert.Equal(t, hydrated.Add(jitter(defaultMaxReuseAge, "org/repo", 2)), due)

	_, ok = s.dueAt(stale, schedNow, false, 1)
	assert.False(t, ok, "stale PRs wait for a fingerprint change")
}

func TestScheduler_IsDue(t *testing.T) {
	t.Parallel()

	s := newTestScheduler()
	hot := &trackedPR{pr: hotPR(1), hydratedAt: schedNow.Add(-30 * time.Second)}
	quiet := &trackedPR{pr: settledPR(2, 48*time.Hour), hydratedAt: schedNow.Add(-30 * time.Second)}

	assert.True(t, s.isDue(hot, schedNow, false, false, 1))
	assert.False(t, s.isDue(hot, schedNow, false, false, lowBudgetFactor), "budget factor stretches the deadline")
	assert.False(t, s.isDue(quiet, schedNow, false, false, 1))

	fresh := &trackedPR{pr: hotPR(3), hydratedAt: schedNow}
	assert.False(t, s.isDue(fresh, schedNow, false, false, 1))
	assert.True(t, s.isDue(fresh, schedNow, false, true, 1), "force refreshes hot PRs")
	assert.False(t, s.isDue(quiet, schedNow, false, true, 1), "force leaves non-hot PRs alone")
}

func TestScheduler_AnyHot(t *testing.T) {
	t.Parallel()

	s := newTestScheduler()
	quiet := entry(&trackedPR{pr: settledPR(2, 48*time.Hour)})
	assert.True(t, s.anyHot(trackedSeq(entry(&trackedPR{pr: hotPR(1)}), quiet), schedNow))
	assert.False(t, s.anyHot(trackedSeq(quiet), schedNow))
	assert.False(t, s.anyHot(trackedSeq(), schedNow))
}

func TestScheduler_ScheduleNext(t *testing.T) {
	t.Parallel()

	// nextFetchAt reports local time; normalize so assert.Equal compares instants.
	next := func(s *scheduler) time.Time { return s.nextFetchAt(schedNow, time.Time{}, false).UTC() }

	t.Run("lands on the soonest PR deadline before discovery", func(t *testing.T) {
		t.Parallel()
		s := newTestScheduler()
		s.recordDiscovery(schedNow, true, 1)
		// Active tier, due 40s out: after the 20s floor, before the 60s
		// discovery.
		hydrated := schedNow.Add(40*time.Second - jitter(activeReuse, "org/repo", 1))
		active := &trackedPR{pr: settledPR(1, 30*time.Minute), hydratedAt: hydrated}
		s.scheduleNext(schedNow, trackedSeq(entry(active)), nil, 1)
		assert.Equal(t, schedNow.Add(40*time.Second), next(s))
	})

	t.Run("falls back to discovery; stale PRs never set a deadline", func(t *testing.T) {
		t.Parallel()
		s := newTestScheduler()
		s.recordDiscovery(schedNow, true, 1)
		stale := &trackedPR{pr: settledPR(1, 60*24*time.Hour), hydratedAt: schedNow.Add(-24 * time.Hour)}
		s.scheduleNext(schedNow, trackedSeq(entry(stale)), nil, 1)
		assert.Equal(t, schedNow.Add(DefaultDiscoveryInterval), next(s))
	})

	t.Run("an overdue PR is clamped to the floor", func(t *testing.T) {
		t.Parallel()
		s := newTestScheduler()
		s.recordDiscovery(schedNow, true, 1)
		overdue := &trackedPR{pr: hotPR(1), hydratedAt: schedNow.Add(-25 * time.Second)}
		s.scheduleNext(schedNow, trackedSeq(entry(overdue)), nil, 1)
		assert.Equal(t, schedNow.Add(DefaultHotInterval), next(s))
	})

	t.Run("floor stretches with the budget factor", func(t *testing.T) {
		t.Parallel()
		s := newTestScheduler()
		s.recordDiscovery(schedNow, true, lowBudgetFactor)
		overdue := &trackedPR{pr: hotPR(1), hydratedAt: schedNow.Add(-time.Hour)}
		s.scheduleNext(schedNow, trackedSeq(entry(overdue)), nil, lowBudgetFactor)
		assert.Equal(t, schedNow.Add(lowBudgetFactor*DefaultHotInterval), next(s))
	})

	t.Run("boosted is looked up by tracked key, not the PR's own key", func(t *testing.T) {
		t.Parallel()
		s := newTestScheduler()
		s.recordDiscovery(schedNow, true, 1)
		s.nextDiscovery = schedNow.Add(time.Hour) // keep discovery out of the way
		// Recent tier; boosted it becomes active. The PR's repo field drifted
		// from the key it is tracked under (rename, casing).
		pr := settledPR(1, 3*time.Hour)
		pr.RepoNameWithOwner = "Org/Repo"
		recent := &trackedPR{pr: pr, hydratedAt: schedNow}
		key := model.PRKey{Repo: "org/repo", Number: 1}
		s.scheduleNext(schedNow, trackedSeq(trackedEntry{key: key, t: recent}), map[model.PRKey]bool{key: true}, 1)
		assert.Equal(t, schedNow.Add(jitter(activeReuse, "Org/Repo", 1)), next(s))
		assert.True(t, s.isDue(recent, next(s), true, false, 1), "isDue agrees at the scheduled deadline")
	})
}
