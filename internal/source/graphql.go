package source

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/model"
)

// ErrSearchOverflow is reported when a GitHub search query hits or exceeds the 1000 results limit.
var ErrSearchOverflow = errors.New("search results overflowed 1000 item limit")

// identityTTL bounds how long a cached viewer identity (login + teams) is
// trusted before re-resolving.
const identityTTL = 24 * time.Hour

const (
	discoveryPageSize = 100
	// DefaultFetchTimeout is the baseline budget for steady-state queue fetches.
	DefaultFetchTimeout = 45 * time.Second
	// ColdFetchTimeout is the recommended deadline for fetches that may need
	// to hydrate a large queue from an empty cache: TUI startup and one-shot
	// commands. Steady-state fetches fit comfortably in DefaultFetchTimeout;
	// cold ones at scale need minutes of server time.
	ColdFetchTimeout = 4 * time.Minute
	// hydrateBatchSize is capped at 10: each alias expands deep fragments
	// (files, checks, timeline, reviews), and GitHub's GraphQL gateway 502s
	// when a batched query grows too expensive (empirically fails at ~20).
	hydrateBatchSize = 10
	// discoveryLimit bounds concurrent searches when discovery pages past the
	// first combined request or falls back to one request per search.
	discoveryLimit            = 3
	defaultHydrateConcurrency = 4
	// primaryRateLimitFallback bounds the backoff when a primary rate limit
	// rejection arrives before any successful response has reported the
	// window's resetAt: one retry per minute until the deadline is learned.
	primaryRateLimitFallback = time.Minute
	// touchEvery throttles cache retention touches for reused PRs; far
	// shorter than the retention window, far longer than a poll.
	touchEvery = 24 * time.Hour
)

// GraphQLClient represents a client capable of executing GraphQL queries against GitHub.
type GraphQLClient interface {
	DoWithContext(ctx context.Context, query string, variables map[string]any, response any) error
}

// GraphQLSourceOption configures a GraphQLSource.
type GraphQLSourceOption func(*GraphQLSource)

// WithLogger configures a zerolog logger for progress reporting.
func WithLogger(l zerolog.Logger) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		s.logger = l
	}
}

// WithCache enables on-disk caching of identity and head-OID-stable PR fields.
func WithCache(store cache.Store) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		s.cache = store
	}
}

// WithDiscoveryConcurrency configures the concurrency limit for follow-up
// discovery searches (pages past the first, or the per-search fallback).
func WithDiscoveryConcurrency(n int) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if n > 0 {
			s.discoveryLimit = n
		}
	}
}

// WithHydrateConcurrency configures the concurrency limit for PR hydration.
func WithHydrateConcurrency(n int) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if n > 0 {
			s.hydrateLimit = n
		}
	}
}

// WithHydrateBatchSize configures the batch size for PR hydration queries.
func WithHydrateBatchSize(n int) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if n > 0 {
			s.hydrateBatchSize = n
		}
	}
}

const (
	defaultCacheRetention     = 14 * 24 * time.Hour
	defaultMaxReuseAge        = 45 * time.Minute
	defaultStaleActivityAfter = model.StaleThreshold
)

// WithCacheRetention configures how long cached PR files are retained before pruning.
func WithCacheRetention(d time.Duration) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if d > 0 {
			s.cacheRetention = d
		}
	}
}

// WithMaxReuseAge configures the base TTL for reusing a hydrated PR in the
// quiet tier (no activity for a day, not yet stale).
func WithMaxReuseAge(d time.Duration) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if d > 0 {
			s.sched.cfg.maxReuseAge = d
		}
	}
}

// WithStaleActivityAfter configures the inactivity age beyond which a cached
// PR is reused until its discovery fingerprint changes, ignoring reuse ages:
// a PR untouched for this long rarely changes, so rehydrating it wastes the
// most expensive queries in the fetch.
func WithStaleActivityAfter(d time.Duration) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if d > 0 {
			s.sched.cfg.staleActivityAfter = d
		}
	}
}

// WithDiscoveryInterval configures the base cadence of discovery searches
// (new, updated, and departed PRs).
func WithDiscoveryInterval(d time.Duration) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if d > 0 {
			s.sched.cfg.discoveryInterval = d
		}
	}
}

// WithHotInterval configures the fast-lane cadence for PRs whose state is
// actively moving (CI running, in a merge queue, merge state computing).
func WithHotInterval(d time.Duration) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if d > 0 {
			s.sched.cfg.hotInterval = d
		}
	}
}

// WithIdleInterval configures the longest wait between discovery searches
// once several in a row found nothing new.
func WithIdleInterval(d time.Duration) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if d > 0 {
			s.sched.cfg.idleInterval = d
		}
	}
}

// WithClock overrides how the source reads the current time. Intended for
// tests; the default is time.Now.
func WithClock(now func() time.Time) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if now != nil {
			s.nowFn = now
		}
	}
}

// GraphQLSource retrieves pull request queues via GitHub GraphQL API and
// schedules its own refresh work (see Pacer). Each Fetch is one tick:
//
//   - A discovery tick runs one combined search request for new, changed, and
//     departed PRs, then hydrates new PRs, PRs whose cheap change fingerprint
//     moved, and PRs whose activity tier says they are due.
//   - Between discoveries, a hot tick skips the searches and re-hydrates only
//     due PRs by node ID — typically those with CI running or in a merge
//     queue.
//
// Discovery backs off toward the idle interval while nothing changes, and
// every cadence stretches when the hourly GraphQL budget runs low.
type GraphQLSource struct {
	client           GraphQLClient
	logger           zerolog.Logger
	cache            cache.Store
	fetchTimeout     time.Duration
	discoveryLimit   int
	hydrateLimit     int
	hydrateBatchSize int
	cacheRetention   time.Duration
	pruneOnce        sync.Once

	// nowFn overrides the clock (tests); guarded by immutability after
	// construction.
	nowFn func() time.Time
	// rlMu guards the rate limit state below.
	rlMu        sync.Mutex
	rlResetAt   time.Time // last resetAt observed on a successful response
	rlRemaining int       // last remaining points observed
	rlLimit     int       // last hourly points limit observed
	rlUntil     time.Time // backoff deadline armed on a primary limit rejection

	// fetchMu serializes Fetch and guards the scheduling state below.
	fetchMu sync.Mutex
	tracked map[model.PRKey]*trackedPR
	order   []model.PRKey // discovery order of tracked PRs
	sched   scheduler
}

// trackedPR is the source's memory of one open pull request between fetches.
type trackedPR struct {
	id         string // GraphQL node ID, for hydrating without a search
	pr         model.PullRequest
	authored   bool
	hydratedAt time.Time // when pr's fresh fields were fetched; zero forces a refresh
	touchedAt  time.Time // last cache write or retention touch
	stableOID  string    // head OID the stable fields (files, sizes) belong to
}

// target returns the hydration request for a tracked PR on a tick without
// discovery: the response carries fresh identity fields, so only the node ID
// and the head the cached stable fields belong to matter here.
func (t *trackedPR) target() hydrateTarget {
	ident := rawIdentity{ID: t.id, Number: t.pr.Number, HeadRefOID: t.pr.HeadRefOID}
	ident.Repository.NameWithOwner = t.pr.RepoNameWithOwner
	return hydrateTarget{
		ident:         ident,
		authored:      t.authored,
		assigned:      t.pr.Assigned,
		directRequest: t.pr.DirectRequest,
		stable:        stableFromCached(t.pr),
		stableHit:     t.stableOID == t.pr.HeadRefOID,
	}
}

// NewGraphQLSource constructs a GraphQLSource.
func NewGraphQLSource(client GraphQLClient, opts ...GraphQLSourceOption) *GraphQLSource {
	s := &GraphQLSource{
		client:           client,
		fetchTimeout:     DefaultFetchTimeout,
		discoveryLimit:   discoveryLimit,
		hydrateLimit:     defaultHydrateConcurrency,
		hydrateBatchSize: hydrateBatchSize,
		cacheRetention:   defaultCacheRetention,
		nowFn:            time.Now,
		tracked:          make(map[model.PRKey]*trackedPR),
		sched:            scheduler{cfg: defaultSchedulerConfig()},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

var (
	//go:embed queries/login.graphql
	loginQuery string

	//go:embed queries/teams.graphql
	teamsQuery string

	//go:embed queries/search_discovery.graphql
	rawDiscoveryQuery string

	//go:embed queries/search_page.graphql
	rawSearchPageQuery string

	//go:embed queries/fragments/discovery_page.graphql
	discoveryPageFragment string

	//go:embed queries/fragments/discovery_fields.graphql
	discoveryFragment string

	//go:embed queries/fragments/stable_fields.graphql
	stableFragment string

	//go:embed queries/fragments/fresh_fields.graphql
	rawFreshFragment string

	//go:embed queries/fragments/status_check_rollup_fields.graphql
	statusCheckRollupFragment string

	freshFragment = rawFreshFragment + "\n" + statusCheckRollupFragment

	discoveryQuery  = rawDiscoveryQuery + "\n" + discoveryPageFragment + "\n" + discoveryFragment
	searchPageQuery = rawSearchPageQuery + "\n" + discoveryPageFragment + "\n" + discoveryFragment

	//go:embed queries/hydrate_fresh.graphql
	rawHydrateFreshQuery string

	//go:embed queries/hydrate_full.graphql
	rawHydrateFullQuery string

	hydrateFreshQuery = rawHydrateFreshQuery + "\n" + discoveryFragment + "\n" + freshFragment
	hydrateFullQuery  = rawHydrateFullQuery + "\n" + discoveryFragment + "\n" + stableFragment + "\n" + freshFragment
)

// searchTask is one GitHub search whose results feed the queue. Alias names
// its field in the combined discovery query. Primary tasks (authored,
// review-requested) fail the whole fetch on error; the rest (assignee,
// user-only review-requested) degrade to a warning and are skipped.
type searchTask struct {
	Alias         string
	Query         string
	IsAuthored    bool
	Primary       bool
	Assigned      bool
	DirectRequest bool
}

// discoveryTasks are the searches behind every discovery, in merge order:
// authored discoveries win.
var discoveryTasks = []searchTask{
	{
		Alias:      "authored",
		Query:      "is:open is:pr author:@me archived:false",
		IsAuthored: true,
		Primary:    true,
	},
	{
		Alias:   "requested",
		Query:   "is:open is:pr review-requested:@me archived:false",
		Primary: true,
	},
	{
		Alias:    "assigned",
		Query:    "is:open is:pr assignee:@me archived:false",
		Assigned: true,
	},
	{
		// review-requested:@me also matches team requests; this narrower
		// search flags the PRs requested from the viewer personally. It is a
		// subset of "requested", so it contributes flags, never new PRs.
		Alias:         "direct",
		Query:         "is:open is:pr user-review-requested:@me archived:false",
		DirectRequest: true,
	},
}

// discoveredPR is a light identity from discovery plus the task flags that
// found it.
type discoveredPR struct {
	ident         rawIdentity
	authored      bool
	assigned      bool
	directRequest bool
}

func (d discoveredPR) key() model.PRKey {
	return model.PRKey{Repo: d.ident.Repository.NameWithOwner, Number: d.ident.Number}
}

// now reads the current time, honoring the WithClock override.
func (s *GraphQLSource) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// observeRateLimit records the budget reported by a successful response: the
// reset time backs off a later primary limit rejection, and the remaining
// share drives the low-budget slowdown.
func (s *GraphQLSource) observeRateLimit(rl rawRateLimit) {
	if rl.ResetAt.IsZero() {
		return
	}
	s.rlMu.Lock()
	s.rlResetAt = rl.ResetAt
	s.rlRemaining = rl.Remaining
	s.rlLimit = rl.Limit
	s.rlMu.Unlock()
}

// budgetFactor is the multiplier on every cadence: lowBudgetFactor while the
// remaining share of the current hourly window is below lowBudgetFraction,
// otherwise 1.
func (s *GraphQLSource) budgetFactor(now time.Time) int {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()
	if s.rlLimit > 0 && now.Before(s.rlResetAt) &&
		float64(s.rlRemaining) < lowBudgetFraction*float64(s.rlLimit) {
		return lowBudgetFactor
	}
	return 1
}

// notePrimaryRateLimit arms the backoff after a primary rate limit rejection:
// until the observed resetAt when one is known, otherwise for the fallback
// window so the deadline can be learned on a later attempt.
func (s *GraphQLSource) notePrimaryRateLimit() {
	now := s.now()
	s.rlMu.Lock()
	until := s.rlResetAt
	if !until.After(now) {
		until = now.Add(primaryRateLimitFallback)
	}
	s.rlUntil = until
	s.rlMu.Unlock()
	s.logger.Warn().Time("until", until).Msg("primary rate limit hit; backing off")
}

// rateLimitedUntil reports the active primary rate limit backoff deadline.
func (s *GraphQLSource) rateLimitedUntil() (time.Time, bool) {
	s.rlMu.Lock()
	defer s.rlMu.Unlock()
	if s.rlUntil.After(s.now()) {
		return s.rlUntil, true
	}
	return time.Time{}, false
}

// NextFetch reports when the next Fetch has work to do: the earlier of the
// next discovery and the soonest per-PR refresh, never sooner than the
// shorter of the hot and discovery intervals after the last fetch, and never
// before an armed primary rate limit backoff expires. Before the first Fetch
// it reports now.
func (s *GraphQLSource) NextFetch() time.Time {
	until, limited := s.rateLimitedUntil()
	return s.sched.nextFetchAt(s.now(), until, limited)
}

// scheduleDiscovery records a completed discovery with the scheduler and
// logs the resulting backoff. Callers hold fetchMu.
func (s *GraphQLSource) scheduleDiscovery(now time.Time, changed bool, factor int) {
	quietStreak, wait := s.sched.recordDiscovery(now, changed, factor)
	s.logger.Debug().
		Int("quiet_streak", quietStreak).
		Dur("next_discovery_in", wait).
		Msg("scheduled discovery")
}

// trackedPRs yields tracked PRs in discovery order, keyed by their tracked
// key. Callers hold fetchMu.
func (s *GraphQLSource) trackedPRs() iter.Seq2[model.PRKey, *trackedPR] {
	return func(yield func(model.PRKey, *trackedPR) bool) {
		for _, key := range s.order {
			if t := s.tracked[key]; t != nil && !yield(key, t) {
				return
			}
		}
	}
}

// Fetch runs one tick and returns the full queue of tracked open PRs. A
// caller-imposed context deadline governs the fetch; the configured timeout
// (see WithFetchTimeout) is the default for callers that do not set one — so
// a cold-start caller can pass a longer budget without being capped at the
// tick default. See WithForceRefresh and WithBoosted for per-call hints.
func (s *GraphQLSource) Fetch(ctx context.Context) (model.Queue, error) {
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()

	if until, limited := s.rateLimitedUntil(); limited {
		return model.Queue{}, fmt.Errorf(
			"%w: waiting until %s",
			ErrPrimaryRateLimit,
			until.UTC().Format(time.RFC3339),
		)
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && s.fetchTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.fetchTimeout)
		defer cancel()
	}

	now := s.now()
	force := ForceRefreshFromContext(ctx)
	boosted := BoostedFromContext(ctx)
	factor := s.budgetFactor(now)
	if factor > 1 {
		s.logger.Warn().Int("factor", factor).Msg("GraphQL budget low; stretching refresh cadence")
	}

	queue, err := s.tick(ctx, now, force, boosted, factor)
	if err != nil {
		s.sched.scheduleRetry(now, factor)
		return model.Queue{}, err
	}
	s.sched.scheduleNext(now, s.trackedPRs(), boosted, factor)

	if s.cache != nil {
		s.pruneOnce.Do(func() {
			pruneCtx := context.WithoutCancel(ctx)
			if removed, err := s.cache.PrunePRs(pruneCtx, s.cacheRetention); err != nil {
				s.logger.Warn().Err(err).Msg("pruning stale PR cache failed; continuing")
			} else if removed > 0 {
				s.logger.Info().Int("pruned_prs", removed).Msg("pruned stale cached pull requests")
			}
		})
	}
	return queue, nil
}

// tick performs one Fetch's network work and updates the tracked set.
// Callers hold fetchMu.
func (s *GraphQLSource) tick(
	ctx context.Context,
	now time.Time,
	force bool,
	boosted map[model.PRKey]bool,
	factor int,
) (model.Queue, error) {
	discovering := s.sched.discoveryDue(now, force)

	var (
		ident      cache.Identity
		discovered []discoveredPR
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		ident, err = s.resolveIdentity(gctx)
		return err
	})
	if discovering {
		g.Go(func() error {
			var err error
			discovered, err = s.discover(gctx)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return model.Queue{}, err
	}

	var (
		targets []hydrateTarget
		changed bool
	)
	if discovering {
		targets, changed = s.reconcile(ctx, discovered, now, boosted, force, factor)
	} else {
		for _, key := range s.order {
			if t := s.tracked[key]; t != nil && s.sched.isDue(t, now, boosted[key], false, factor) {
				targets = append(targets, t.target())
			}
		}
	}

	s.logger.Info().
		Bool("discovery", discovering).
		Int("tracked_total", len(s.order)).
		Int("to_hydrate_total", len(targets)).
		Msg("planned hydration")

	if progress := ProgressFromContext(ctx); progress != nil {
		progress(len(s.order)-len(targets), len(s.order))
	}
	results, lastErr, err := s.hydrate(ctx, targets, len(s.order))
	secondary := errors.Is(err, ErrSecondaryRateLimit)
	if err != nil && !secondary {
		return model.Queue{}, err
	}
	if secondary && len(results) == 0 && !s.anyHydrated() {
		return model.Queue{}, err
	}
	if secondary {
		s.logger.Warn().
			Err(err).
			Int("hydrated_total", len(results)).
			Int("tracked_total", len(s.order)).
			Msg("secondary rate limit hit during hydration; returning degraded queue")
	}
	if s.apply(targets, results, now, secondary) {
		changed = true
	}

	wanted := len(s.order)
	queue := s.assemble()
	queue.Viewer = ident.Login
	queue.Teams = ident.Teams
	if wanted > 0 && len(queue.Authored)+len(queue.Inbox) == 0 {
		if lastErr != nil {
			return model.Queue{}, fmt.Errorf("all pull requests failed hydration: %w", lastErr)
		}
		return model.Queue{}, errors.New("all pull requests failed hydration")
	}
	if discovering {
		s.scheduleDiscovery(now, changed || force || s.sched.anyHot(s.trackedPRs(), now), factor)
	}

	s.logger.Info().
		Int("authored_total", len(queue.Authored)).
		Int("inbox_total", len(queue.Inbox)).
		Msg("completed queue build")
	return queue, nil
}

// reconcile folds one discovery into the tracked set: it adopts new PRs
// (seeding them from the on-disk cache when possible), refreshes discovery
// fields and flags on unchanged ones, forgets PRs that left every search,
// and returns the PRs needing hydration plus whether anything changed.
// Callers hold fetchMu.
func (s *GraphQLSource) reconcile(
	ctx context.Context,
	discovered []discoveredPR,
	now time.Time,
	boosted map[model.PRKey]bool,
	force bool,
	factor int,
) ([]hydrateTarget, bool) {
	changed := false
	seen := make(map[model.PRKey]bool, len(discovered))
	order := make([]model.PRKey, 0, len(discovered))
	var targets []hydrateTarget

	for _, d := range discovered {
		key := d.key()
		seen[key] = true
		order = append(order, key)

		t := s.tracked[key]
		if t == nil {
			changed = true
			t = s.loadCached(ctx, d)
		}
		if t == nil {
			targets = append(targets, hydrateTarget{discoveredPR: d})
			continue
		}
		t.id = d.ident.ID
		t.authored = d.authored
		t.pr.Assigned = d.assigned
		t.pr.DirectRequest = d.directRequest

		moved := fingerprintChanged(d.ident, t.pr)
		if moved {
			changed = true
		}
		if !moved && !s.sched.isDue(t, now, boosted[key], force, factor) {
			s.applyDiscovery(&t.pr, d, now)
			s.touch(ctx, key, t, now)
			continue
		}
		target := hydrateTarget{discoveredPR: d}
		if t.stableOID == d.ident.HeadRefOID {
			target.stable = stableFromCached(t.pr)
			target.stableHit = true
		}
		targets = append(targets, target)
	}

	for key := range s.tracked {
		if !seen[key] {
			delete(s.tracked, key)
			changed = true
		}
	}
	s.order = order
	return targets, changed
}

// loadCached seeds tracking for a newly discovered PR from the on-disk cache,
// or returns nil on a miss. Callers hold fetchMu.
func (s *GraphQLSource) loadCached(ctx context.Context, d discoveredPR) *trackedPR {
	if s.cache == nil {
		return nil
	}
	cached, savedAt, ok := s.cache.PR(ctx, d.ident.Repository.NameWithOwner, d.ident.Number)
	if !ok {
		return nil
	}
	t := &trackedPR{
		id:         d.ident.ID,
		pr:         cached,
		authored:   d.authored,
		hydratedAt: savedAt,
		stableOID:  cached.HeadRefOID,
	}
	s.tracked[d.key()] = t
	return t
}

// applyDiscovery refreshes a reused PR's discovery-sourced fields.
func (s *GraphQLSource) applyDiscovery(pr *model.PullRequest, d discoveredPR, now time.Time) {
	pr.Title = d.ident.Title
	pr.IsDraft = d.ident.IsDraft
	pr.IsInMergeQueue = d.ident.IsInMergeQueue
	pr.UpdatedAt = d.ident.UpdatedAt
	pr.Author = d.ident.Author.Login
	pr.URL = d.ident.URL
	pr.BaseRefName = d.ident.BaseRefName
	pr.DefaultBranch = d.ident.Repository.DefaultBranchRef.Name
	pr.Assigned = d.assigned
	pr.DirectRequest = d.directRequest
	pr.Stack = convertStack(d.ident.Stack, d.ident.StackEntry)
	pr.MergeStatus = model.ComputeMergeStatus(pr.Mergeable, pr.MergeStateStatus, d.ident.IsDraft)
	pr.MergeStatus.IsInMergeQueue = d.ident.IsInMergeQueue
	pr.Stale = !d.ident.UpdatedAt.IsZero() && now.Sub(d.ident.UpdatedAt) >= s.sched.cfg.staleActivityAfter
}

// touch bumps a reused PR's cache file mtime so retention pruning does not
// evict an open PR, at most once per touchEvery; the cached SavedAt is left
// intact to keep tracking actual hydration time.
func (s *GraphQLSource) touch(ctx context.Context, key model.PRKey, t *trackedPR, now time.Time) {
	if s.cache == nil || now.Sub(t.touchedAt) < touchEvery {
		return
	}
	if err := s.cache.TouchPR(ctx, key.Repo, key.Number); err != nil {
		s.logger.Warn().Err(err).Msg("touching reused PR cache failed; continuing")
		return
	}
	t.touchedAt = now
}

// anyHydrated reports whether any tracked PR has fully hydrated data to
// fall back on. Callers hold fetchMu.
func (s *GraphQLSource) anyHydrated() bool {
	for _, key := range s.order {
		if t := s.tracked[key]; t != nil && !t.pr.Partial {
			return true
		}
	}
	return false
}

// apply records hydration results in the tracked set and reports whether a
// PR left the queue (closed or merged since it was last seen). A target that failed keeps its last good data; a new
// one is dropped, or — when a secondary rate limit cut hydration short —
// kept as a Partial PR built from discovery data. Callers hold fetchMu.
func (s *GraphQLSource) apply(
	targets []hydrateTarget,
	results map[model.PRKey]hydrateResult,
	now time.Time,
	secondary bool,
) bool {
	removed := false
	for _, target := range targets {
		key := target.key()
		r, ok := results[key]
		switch {
		case ok && r.gone:
			delete(s.tracked, key)
			s.order = slices.DeleteFunc(s.order, func(k model.PRKey) bool { return k == key })
			removed = true
		case ok:
			t := s.tracked[key]
			if t == nil {
				t = &trackedPR{}
				s.tracked[key] = t
			}
			t.id = r.id
			t.pr = r.PullRequest
			t.authored = r.authored
			t.stableOID = r.stableOID
			t.hydratedAt = now
			if r.stableOID != r.HeadRefOID {
				// Pushed since the stable fields were fetched: refresh them
				// on the next tick.
				t.hydratedAt = time.Time{}
			}
			if r.saved {
				t.touchedAt = now
			}
		case s.tracked[key] != nil:
		case secondary:
			pr := convertPR(target.ident, stableFields{}, rawFresh{}, target.assigned, convertOpts{
				staleActivityAfter: s.sched.cfg.staleActivityAfter,
			})
			pr.Partial = true
			pr.DirectRequest = target.directRequest
			s.tracked[key] = &trackedPR{id: target.ident.ID, pr: pr, authored: target.authored}
		}
	}
	return removed
}

// assemble builds the queue from tracked PRs in discovery order, dropping
// order entries for PRs no longer tracked. Callers hold fetchMu.
func (s *GraphQLSource) assemble() model.Queue {
	var authored, inbox []model.PullRequest
	order := s.order[:0]
	for _, key := range s.order {
		t := s.tracked[key]
		if t == nil {
			continue
		}
		order = append(order, key)
		if t.authored {
			authored = append(authored, t.pr)
		} else {
			inbox = append(inbox, t.pr)
		}
	}
	s.order = order
	return model.MergeQueue(authored, inbox)
}

// resolveIdentity returns the viewer login and org-qualified teams, consulting
// the cache first. There is no safe fallback for the teams query: without the
// userLogins filter it returns every team in every org, not just the viewer's —
// so a cache-miss query failure fails the fetch.
func (s *GraphQLSource) resolveIdentity(ctx context.Context) (cache.Identity, error) {
	if s.cache != nil {
		if id, savedAt, ok := s.cache.Identity(ctx); ok && time.Since(savedAt) < identityTTL {
			s.logger.Debug().Str("login", id.Login).Msg("using cached identity")
			return id, nil
		}
	}

	var vLogin struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}
	if err := s.client.DoWithContext(ctx, loginQuery, nil, &vLogin); err != nil {
		return cache.Identity{}, fmt.Errorf("resolve viewer login: %w", err)
	}

	var vResp viewerTeamsData
	if err := s.client.DoWithContext(
		ctx,
		teamsQuery,
		map[string]any{"login": vLogin.Viewer.Login},
		&vResp,
	); err != nil {
		return cache.Identity{}, fmt.Errorf("resolve viewer teams: %w", err)
	}

	if vResp.Viewer.Organizations.PageInfo.HasNextPage {
		s.logger.Warn().Msg("viewer organizations truncated (>100 orgs)")
	}

	id := cache.Identity{Login: vLogin.Viewer.Login}
	for _, org := range vResp.Viewer.Organizations.Nodes {
		if org.Teams.PageInfo.HasNextPage {
			s.logger.Warn().Str("org", org.Login).Msg("viewer teams truncated (>100 teams)")
		}
		for _, team := range org.Teams.Nodes {
			id.Teams = append(id.Teams, org.Login+"/"+team.Slug)
		}
	}

	s.logger.Info().Str("login", id.Login).Strs("teams", id.Teams).Msg("resolved GitHub viewer")

	if s.cache != nil {
		if err := s.cache.SaveIdentity(ctx, id); err != nil {
			s.logger.Warn().Err(err).Msg("saving identity cache failed; continuing")
		}
	}
	return id, nil
}

type viewerTeamsData struct {
	Viewer struct {
		Organizations struct {
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
			Nodes []struct {
				Login string `json:"login"`
				Teams struct {
					PageInfo struct {
						HasNextPage bool `json:"hasNextPage"`
					} `json:"pageInfo"`
					Nodes []struct {
						Slug string `json:"slug"`
					} `json:"nodes"`
				} `json:"teams"`
			} `json:"nodes"`
		} `json:"organizations"`
	} `json:"viewer"`
}

// searchConn is one search connection page.
type searchConn struct {
	IssueCount int           `json:"issueCount"`
	PageInfo   pageInfo      `json:"pageInfo"`
	Nodes      []rawIdentity `json:"nodes"`
}

// discover runs every discovery search in one combined request, following up
// per search only for pages past the first, and returns the deduplicated set
// of pull requests ordered by first discovery. If the combined request fails
// outright, each search is retried on its own so a non-primary failure only
// costs its own results.
func (s *GraphQLSource) discover(ctx context.Context) ([]discoveredPR, error) {
	vars := map[string]any{"first": discoveryPageSize}
	for _, task := range discoveryTasks {
		vars[task.Alias] = task.Query
	}

	var resp map[string]json.RawMessage
	failed := map[string]error{}
	err := s.client.DoWithContext(ctx, discoveryQuery, vars, &resp)
	var gqlErr *api.GraphQLError
	switch {
	case err == nil:
	case errors.As(err, &gqlErr):
		// Partial success: data for the aliases that resolved is decoded.
		for _, item := range gqlErr.Errors {
			if len(item.Path) == 0 {
				return s.discoverEach(ctx, err)
			}
			if alias, ok := item.Path[0].(string); ok {
				failed[alias] = fmt.Errorf("%s", item.Message)
			}
		}
	case isPrimaryRateLimitErr(err):
		s.notePrimaryRateLimit()
		return nil, fmt.Errorf("%w: %w", ErrPrimaryRateLimit, err)
	case isSecondaryRateLimitErr(err):
		// More requests while limited risk a ban: no per-search fallback.
		return nil, fmt.Errorf("%w: %w", ErrSecondaryRateLimit, err)
	case ctx.Err() != nil:
		return nil, err
	default:
		return s.discoverEach(ctx, err)
	}

	var rl rawRateLimit
	if raw, ok := resp["rateLimit"]; ok {
		_ = json.Unmarshal(raw, &rl)
		s.observeRateLimit(rl)
	}

	results := make([][]discoveredPR, len(discoveryTasks))
	cursors := make([]*string, len(discoveryTasks))
	for i, task := range discoveryTasks {
		conn, taskErr := decodeSearch(resp[task.Alias], failed[task.Alias], task)
		if taskErr != nil {
			if err := s.skipTask(task, taskErr); err != nil {
				return nil, err
			}
			continue
		}
		s.logger.Info().
			Str("query", task.Query).
			Int("prs_returned", len(conn.Nodes)).
			Int("matching_total", conn.IssueCount).
			Int("cost", rl.Cost).
			Int("remaining", rl.Remaining).
			Msg("retrieved discovery page")
		results[i] = toDiscovered(conn.Nodes, task)
		if conn.PageInfo.HasNextPage {
			cursors[i] = conn.PageInfo.EndCursor
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.discoveryLimit)
	for i, task := range discoveryTasks {
		cursor := cursors[i]
		if cursor == nil {
			continue
		}
		g.Go(func() error {
			rest, err := s.discoverTask(gctx, task, cursor)
			if err != nil {
				return s.skipTask(task, err)
			}
			results[i] = append(results[i], rest...)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return mergeDiscovered(results), nil
}

// discoverEach is the per-search fallback for a failed combined discovery.
func (s *GraphQLSource) discoverEach(ctx context.Context, cause error) ([]discoveredPR, error) {
	s.logger.Warn().Err(cause).Msg("combined discovery failed; retrying each search")
	results := make([][]discoveredPR, len(discoveryTasks))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.discoveryLimit)
	for i, task := range discoveryTasks {
		g.Go(func() error {
			prs, err := s.discoverTask(gctx, task, nil)
			if err != nil {
				return s.skipTask(task, err)
			}
			results[i] = prs
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return mergeDiscovered(results), nil
}

// skipTask returns err for a primary search, which fails the fetch, and
// logs and swallows it otherwise.
func (s *GraphQLSource) skipTask(task searchTask, err error) error {
	if task.Primary {
		return fmt.Errorf("search %q: %w", task.Query, err)
	}
	s.logger.Warn().Err(err).Str("query", task.Query).Msg("discovery task failed; skipping")
	return nil
}

// decodeSearch decodes one alias of the combined discovery response.
func decodeSearch(raw json.RawMessage, aliasErr error, task searchTask) (searchConn, error) {
	if aliasErr != nil {
		return searchConn{}, aliasErr
	}
	var conn searchConn
	if len(raw) == 0 || string(raw) == "null" {
		return conn, errors.New("search missing from response")
	}
	if err := json.Unmarshal(raw, &conn); err != nil {
		return conn, fmt.Errorf("decode search: %w", err)
	}
	if conn.IssueCount >= 1000 {
		return conn, fmt.Errorf("%w: %s", ErrSearchOverflow, task.Query)
	}
	return conn, nil
}

func toDiscovered(nodes []rawIdentity, task searchTask) []discoveredPR {
	prs := make([]discoveredPR, 0, len(nodes))
	for _, node := range nodes {
		prs = append(prs, discoveredPR{
			ident:         node,
			authored:      task.IsAuthored,
			assigned:      task.Assigned,
			directRequest: task.DirectRequest,
		})
	}
	return prs
}

// mergeDiscovered dedupes per-task results in task order, so authored
// discoveries win and ordering is deterministic. Direct-request results only
// flag PRs found by the other searches: the combined query fetches just their
// IDs.
func mergeDiscovered(results [][]discoveredPR) []discoveredPR {
	seen := make(map[model.PRKey]int)
	direct := make(map[string]bool)
	var merged []discoveredPR
	for i, taskPRs := range results {
		if discoveryTasks[i].DirectRequest {
			for _, d := range taskPRs {
				direct[d.ident.ID] = true
			}
			continue
		}
		for _, d := range taskPRs {
			if idx, ok := seen[d.key()]; ok {
				merged[idx].authored = merged[idx].authored || d.authored
				merged[idx].assigned = merged[idx].assigned || d.assigned
				continue
			}
			seen[d.key()] = len(merged)
			merged = append(merged, d)
		}
	}
	for i := range merged {
		merged[i].directRequest = direct[merged[i].ident.ID]
	}
	return merged
}

// discoverTask paginates a single discovery search starting after cursor
// (nil: from the first page).
func (s *GraphQLSource) discoverTask(ctx context.Context, task searchTask, cursor *string) ([]discoveredPR, error) {
	var prs []discoveredPR
	page := 1

	for {
		vars := map[string]any{
			"query": task.Query,
			"first": discoveryPageSize,
		}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		var resp struct {
			RateLimit rawRateLimit `json:"rateLimit"`
			Search    searchConn   `json:"search"`
		}
		if err := s.client.DoWithContext(ctx, searchPageQuery, vars, &resp); err != nil {
			if isPrimaryRateLimitErr(err) {
				s.notePrimaryRateLimit()
				return nil, fmt.Errorf("%w: %w", ErrPrimaryRateLimit, err)
			}
			return nil, err
		}
		s.observeRateLimit(resp.RateLimit)

		if resp.Search.IssueCount >= 1000 {
			return nil, fmt.Errorf("%w: %s", ErrSearchOverflow, task.Query)
		}

		s.logger.Info().
			Str("query", task.Query).
			Int("page", page).
			Int("prs_returned", len(resp.Search.Nodes)).
			Int("matching_total", resp.Search.IssueCount).
			Int("cost", resp.RateLimit.Cost).
			Int("remaining", resp.RateLimit.Remaining).
			Msg("retrieved discovery page")
		page++

		prs = append(prs, toDiscovered(resp.Search.Nodes, task)...)

		if !resp.Search.PageInfo.HasNextPage || resp.Search.PageInfo.EndCursor == nil {
			break
		}
		cursor = resp.Search.PageInfo.EndCursor
	}

	return prs, nil
}

type pageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

type rawRateLimit struct {
	Cost      int       `json:"cost"`
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

type hydrateResponse struct {
	RateLimit rawRateLimit `json:"rateLimit"`
	Nodes     []*rawPR     `json:"nodes"`
}

// hydrateTarget pairs a PR to hydrate with cached stable fields when they
// belong to its current head.
type hydrateTarget struct {
	discoveredPR
	stable    stableFields
	stableHit bool
}

// hydrateResult is one PR's hydration outcome.
type hydrateResult struct {
	flaggedPR
	key       model.PRKey
	id        string
	stableOID string // head OID the stable fields belong to
	saved     bool   // persisted to the on-disk cache
	gone      bool   // no longer open (or no longer visible): drop it
}

// hydrate fetches full PR data for targets in parallel aliased batches,
// bisecting failing batches down to singletons to isolate cost-driven 502s.
// total is the queue size, for progress reporting. A secondary rate limit
// returns the results gathered so far alongside ErrSecondaryRateLimit;
// lastErr is the last persistent per-PR failure.
func (s *GraphQLSource) hydrate(
	ctx context.Context,
	targets []hydrateTarget,
	total int,
) (results map[model.PRKey]hydrateResult, lastErr, err error) {
	results = make(map[model.PRKey]hydrateResult, len(targets))
	if len(targets) == 0 {
		return results, nil, nil
	}

	batches := prepareHydrateBatches(targets, s.hydrateBatchSize)
	batchResults := make([][]hydrateResult, len(batches))

	var completedCount atomic.Int32
	// #nosec G115 -- counts are bounded by discovery size
	completedCount.Store(int32(total - len(targets)))

	var lastErrMu sync.Mutex
	recordErr := func(err error) {
		lastErrMu.Lock()
		lastErr = err
		lastErrMu.Unlock()
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.hydrateLimit)

	for bIdx, b := range batches {
		g.Go(func() error {
			prs, err := s.hydrateBatchBisect(gctx, b.targets, b.full, recordErr)
			if err != nil {
				return err
			}
			batchResults[bIdx] = prs
			// #nosec G115 -- batch size is small and bounded
			done := completedCount.Add(int32(len(b.targets)))
			if progress := ProgressFromContext(gctx); progress != nil {
				progress(int(done), total)
			}
			return nil
		})
	}

	waitErr := g.Wait()
	if waitErr != nil && ctx.Err() != nil {
		return nil, lastErr, ctx.Err()
	}
	for _, batch := range batchResults {
		for _, r := range batch {
			results[r.key] = r
		}
	}
	if waitErr != nil && !errors.Is(waitErr, ErrSecondaryRateLimit) {
		return nil, lastErr, waitErr
	}
	return results, lastErr, waitErr
}

type hydrateBatch struct {
	targets []hydrateTarget
	full    bool
}

func prepareHydrateBatches(toHydrate []hydrateTarget, batchSize int) []hydrateBatch {
	var misses, hits []hydrateTarget
	for _, target := range toHydrate {
		if target.stableHit {
			hits = append(hits, target)
		} else {
			misses = append(misses, target)
		}
	}

	var batches []hydrateBatch
	for i := 0; i < len(misses); i += batchSize {
		end := min(i+batchSize, len(misses))
		batches = append(batches, hydrateBatch{targets: misses[i:end], full: true})
	}
	for i := 0; i < len(hits); i += batchSize {
		end := min(i+batchSize, len(hits))
		batches = append(batches, hydrateBatch{targets: hits[i:end], full: false})
	}
	return batches
}

func (s *GraphQLSource) hydrateBatchBisect(
	ctx context.Context,
	batch []hydrateTarget,
	full bool,
	recordErr func(error),
) ([]hydrateResult, error) {
	if len(batch) == 0 {
		return nil, nil
	}

	if len(batch) > 1 {
		reqCtx := ContextWithRetryAttempts(ctx, 1)
		prs, err := s.executeHydrateQuery(reqCtx, batch, full)
		if err == nil {
			return prs, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A primary rate limit is account-global: bisecting only burns more
		// of a budget that is already exhausted. Abort the whole fetch.
		if errors.Is(err, ErrPrimaryRateLimit) {
			s.logger.Error().Err(err).Msg("primary rate limit hit; aborting fetch")
			return nil, err
		}
		// A secondary rate limit is account-global: bisecting only multiplies
		// requests made while limited, which GitHub's docs warn risks a ban.
		// Abort the whole fetch; the next refresh (>= 1 minute out) retries.
		if isSecondaryRateLimitErr(err) {
			s.logger.Error().Err(err).Msg("secondary rate limit hit; aborting fetch")
			return nil, fmt.Errorf("%w: %w", ErrSecondaryRateLimit, err)
		}
		s.logger.Warn().
			Err(err).
			Int("batch_size", len(batch)).
			Msg("hydrate batch failed; bisecting")

		mid := len(batch) / 2
		left, err := s.hydrateBatchBisect(ctx, batch[:mid], full, recordErr)
		if err != nil {
			return nil, err
		}
		right, err := s.hydrateBatchBisect(ctx, batch[mid:], full, recordErr)
		if err != nil {
			return nil, err
		}
		return append(left, right...), nil
	}

	prs, err := s.executeHydrateQuery(ctx, batch, full)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrPrimaryRateLimit) {
			s.logger.Error().Err(err).Msg("primary rate limit hit; aborting fetch")
			return nil, err
		}
		if isSecondaryRateLimitErr(err) {
			s.logger.Error().Err(err).Msg("secondary rate limit hit; aborting fetch")
			return nil, fmt.Errorf("%w: %w", ErrSecondaryRateLimit, err)
		}
		if recordErr != nil {
			recordErr(err)
		}
		s.logger.Warn().
			Err(err).
			Str("repo", batch[0].ident.Repository.NameWithOwner).
			Int("number", batch[0].ident.Number).
			Msg("PR hydration failed persistently; keeping last known state")
		return nil, nil
	}
	return prs, nil
}

func (s *GraphQLSource) executeHydrateQuery(
	ctx context.Context,
	batch []hydrateTarget,
	full bool,
) ([]hydrateResult, error) {
	ids := make([]string, len(batch))
	for i, d := range batch {
		ids[i] = d.ident.ID
	}
	vars := map[string]any{
		"ids": ids,
	}

	query := hydrateFreshQuery
	if full {
		query = hydrateFullQuery
	}

	var resp hydrateResponse
	startHydrate := time.Now()
	if err := s.client.DoWithContext(ctx, query, vars, &resp); err != nil {
		if isPrimaryRateLimitErr(err) {
			s.notePrimaryRateLimit()
			return nil, fmt.Errorf("%w: %w", ErrPrimaryRateLimit, err)
		}
		return nil, fmt.Errorf("hydrate PR batch: %w", err)
	}
	s.observeRateLimit(resp.RateLimit)
	elapsedMs := time.Since(startHydrate).Milliseconds()

	s.logger.Info().
		Int("cost", resp.RateLimit.Cost).
		Int("remaining", resp.RateLimit.Remaining).
		Int("batch_count", len(batch)).
		Bool("full", full).
		Int64("elapsed_ms", elapsedMs).
		Msg("hydrated pr batch")

	out := make([]hydrateResult, 0, len(batch))
	for i, d := range batch {
		if i >= len(resp.Nodes) {
			break
		}
		node := resp.Nodes[i]
		if node == nil || (node.State != "" && node.State != "OPEN") {
			s.logger.Info().
				Str("repo", d.ident.Repository.NameWithOwner).
				Int("number", d.ident.Number).
				Msg("PR closed or vanished since discovery; dropping")
			out = append(out, hydrateResult{key: d.key(), gone: true})
			continue
		}

		stable, stableOID := d.stable, d.ident.HeadRefOID
		if full {
			stable, stableOID = stableFromWire(node.rawStable), node.HeadRefOID
		}
		converted := convertPR(node.rawIdentity, stable, node.rawFresh, d.assigned, convertOpts{
			staleActivityAfter: s.sched.cfg.staleActivityAfter,
		})
		converted.DirectRequest = d.directRequest
		r := hydrateResult{
			PullRequest: converted, authored: d.authored,
			key:       d.key(),
			id:        node.ID,
			stableOID: stableOID,
		}
		// Only a consistent snapshot (stable fields for the current head) is
		// worth persisting.
		if s.cache != nil && stableOID == node.HeadRefOID {
			if err := s.cache.SavePR(ctx, d.ident.Repository.NameWithOwner, d.ident.Number, converted); err != nil {
				s.logger.Warn().Err(err).Msg("saving PR cache failed; continuing")
			} else {
				r.saved = true
			}
		}
		out = append(out, r)
	}

	return out, nil
}

// flaggedPR is a hydrated pull request with its discovery flags.
type flaggedPR struct {
	model.PullRequest
	authored bool
}
