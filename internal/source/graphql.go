package source

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"sync/atomic"
	"time"

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
	discoveryPageSize   = 100
	defaultFetchTimeout = 45 * time.Second
	// ColdFetchTimeout is the recommended deadline for fetches that may need
	// to hydrate a large queue from an empty cache: TUI startup and one-shot
	// commands. Steady-state fetches fit comfortably in defaultFetchTimeout;
	// cold ones at scale need minutes of server time.
	ColdFetchTimeout = 4 * time.Minute
	// hydrateBatchSize is capped at 10: each alias expands deep fragments
	// (files, checks, timeline, reviews), and GitHub's GraphQL gateway 502s
	// when a batched query grows too expensive (empirically fails at ~20).
	hydrateBatchSize          = 10
	discoveryLimit            = 3
	defaultHydrateConcurrency = 4
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

// WithDiscoveryConcurrency configures the concurrency limit for discovery searches.
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

// WithMaxReuseAge configures the base TTL for reusing fully-hydrated cached PRs.
func WithMaxReuseAge(d time.Duration) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if d > 0 {
			s.maxReuseAge = d
		}
	}
}

// WithStaleActivityAfter configures the inactivity age beyond which a cached
// PR is reused indefinitely, ignoring the max reuse age: a PR untouched for
// this long cannot have changed, so rehydrating it wastes the most expensive
// queries in the fetch.
func WithStaleActivityAfter(d time.Duration) GraphQLSourceOption {
	return func(s *GraphQLSource) {
		if d > 0 {
			s.staleActivityAfter = d
		}
	}
}

// GraphQLSource retrieves pull request queues via GitHub GraphQL API using a
// two-phase fetch: light discovery searches, then batched hydration of the
// deduplicated results.
type GraphQLSource struct {
	client             GraphQLClient
	logger             zerolog.Logger
	cache              cache.Store
	fetchTimeout       time.Duration
	discoveryLimit     int
	hydrateLimit       int
	hydrateBatchSize   int
	cacheRetention     time.Duration
	maxReuseAge        time.Duration
	staleActivityAfter time.Duration
	pruneOnce          sync.Once
}

// NewGraphQLSource constructs a GraphQLSource.
func NewGraphQLSource(client GraphQLClient, opts ...GraphQLSourceOption) *GraphQLSource {
	s := &GraphQLSource{
		client:             client,
		fetchTimeout:       defaultFetchTimeout,
		discoveryLimit:     discoveryLimit,
		hydrateLimit:       defaultHydrateConcurrency,
		hydrateBatchSize:   hydrateBatchSize,
		cacheRetention:     defaultCacheRetention,
		maxReuseAge:        defaultMaxReuseAge,
		staleActivityAfter: defaultStaleActivityAfter,
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

	//go:embed queries/fragments/discovery_fields.graphql
	discoveryFragment string

	//go:embed queries/fragments/stable_fields.graphql
	stableFragment string

	//go:embed queries/fragments/fresh_fields.graphql
	freshFragment string

	discoveryQuery = rawDiscoveryQuery + "\n" + discoveryFragment

	//go:embed queries/hydrate_fresh.graphql
	rawHydrateFreshQuery string

	//go:embed queries/hydrate_full.graphql
	rawHydrateFullQuery string

	hydrateFreshQuery = rawHydrateFreshQuery + "\n" + freshFragment
	hydrateFullQuery  = rawHydrateFullQuery + "\n" + stableFragment + "\n" + freshFragment
)

// searchTask is one GitHub search whose results feed the queue. Primary tasks
// (authored, direct review-requested) fail the whole fetch on error; the rest
// (assignee) degrade to a warning and are skipped.
type searchTask struct {
	Query      string
	IsAuthored bool
	Primary    bool
	Assigned   bool
}

// discoveredPR is a light identity from discovery plus the task flags that
// found it.
type discoveredPR struct {
	ident    rawIdentity
	authored bool
	assigned bool
}

func (d discoveredPR) key() model.PRKey {
	return model.PRKey{Repo: d.ident.Repository.NameWithOwner, Number: d.ident.Number}
}

// Fetch retrieves the authored and inbox pull request queue from GitHub.
// A caller-imposed context deadline governs the fetch; the configured
// timeout (see WithFetchTimeout) is the default for callers, like the TUI
// refresh tick, that do not set one — so a cold-start caller can pass a
// longer budget without being capped at the tick default.
func (s *GraphQLSource) Fetch(ctx context.Context) (model.Queue, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && s.fetchTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.fetchTimeout)
		defer cancel()
	}

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
	g.Go(func() error {
		var err error
		discovered, err = s.discover(gctx)
		return err
	})
	if err := g.Wait(); err != nil {
		return model.Queue{}, err
	}
	if progress := ProgressFromContext(ctx); progress != nil {
		progress(0, len(discovered))
	}

	prs, err := s.hydrate(ctx, discovered)
	if err != nil {
		return model.Queue{}, err
	}

	var authoredPRs, inboxPRs []model.PullRequest
	for _, pr := range prs {
		if pr.authored {
			authoredPRs = append(authoredPRs, pr.PullRequest)
		} else {
			inboxPRs = append(inboxPRs, pr.PullRequest)
		}
	}

	queue := model.MergeQueue(authoredPRs, inboxPRs)
	queue.Viewer = ident.Login
	queue.Teams = ident.Teams

	s.logger.Info().
		Int("authored_total", len(queue.Authored)).
		Int("inbox_total", len(queue.Inbox)).
		Msg("completed queue build")

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

// discover runs the search tasks concurrently (bounded), returning the
// deduplicated set of pull requests ordered by first discovery.
func (s *GraphQLSource) discover(ctx context.Context) ([]discoveredPR, error) {
	tasks := []searchTask{
		{
			Query:      "is:open is:pr author:@me archived:false",
			IsAuthored: true,
			Primary:    true,
		},
		{
			Query:   "is:open is:pr review-requested:@me archived:false",
			Primary: true,
		},
		{
			Query:    "is:open is:pr assignee:@me archived:false",
			Assigned: true,
		},
	}

	s.logger.Info().Int("tasks_total", len(tasks)).Msg("assembled search tasks")

	limit := s.discoveryLimit
	if limit <= 0 {
		limit = discoveryLimit
	}

	results := make([][]discoveredPR, len(tasks))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)

	for i, task := range tasks {
		g.Go(func() error {
			s.logger.Info().Str("query", task.Query).Msg("executing discovery search")
			prs, err := s.discoverTask(gctx, task)
			if err != nil {
				if !task.Primary {
					s.logger.Warn().Err(err).Str("query", task.Query).Msg("discovery task failed; skipping")
					return nil
				}
				return fmt.Errorf("search %q: %w", task.Query, err)
			}
			results[i] = prs
			s.logger.Info().Str("query", task.Query).Int("prs_found", len(prs)).Msg("completed discovery search")
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Merge in task order so authored discoveries win and ordering is deterministic.
	seen := make(map[model.PRKey]int)
	var merged []discoveredPR
	for _, taskPRs := range results {
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
	return merged, nil
}

// discoverTask paginates a single light discovery search.
func (s *GraphQLSource) discoverTask(ctx context.Context, task searchTask) ([]discoveredPR, error) {
	var prs []discoveredPR
	var cursor *string
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
			Search    struct {
				IssueCount int      `json:"issueCount"`
				PageInfo   pageInfo `json:"pageInfo"`
				Nodes      []struct {
					rawIdentity
				} `json:"nodes"`
			} `json:"search"`
		}
		if err := s.client.DoWithContext(ctx, discoveryQuery, vars, &resp); err != nil {
			return nil, err
		}

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

		for _, node := range resp.Search.Nodes {
			prs = append(prs, discoveredPR{
				ident:    node.rawIdentity,
				authored: task.IsAuthored,
				assigned: task.Assigned,
			})
		}

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
	RateLimit rawRateLimit   `json:"rateLimit"`
	Nodes     []*rawHydrated `json:"nodes"`
}

func (s *GraphQLSource) maxReuseAgeFor(repo string, num int) time.Duration {
	base := s.maxReuseAge
	if base <= 0 {
		base = defaultMaxReuseAge
	}
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s#%d", repo, num)
	val := h.Sum32()
	jitterFraction := 0.75 + 0.50*(float64(val)/float64(math.MaxUint32))
	return time.Duration(float64(base) * jitterFraction)
}

func (s *GraphQLSource) canReuse(
	cached model.PullRequest,
	savedAt time.Time,
	discovered discoveredPR,
	now time.Time,
) bool {
	// A PR with no activity beyond the stale cutoff cannot have changed since
	// it was cached, so the max reuse age does not apply to it.
	stale := !discovered.ident.UpdatedAt.IsZero() &&
		now.Sub(discovered.ident.UpdatedAt) >= s.staleActivityAfter

	return cached.UpdatedAt.Equal(discovered.ident.UpdatedAt) &&
		cached.HeadRefOID == discovered.ident.HeadRefOID &&
		cached.IsInMergeQueue == discovered.ident.IsInMergeQueue &&
		cached.Mergeable != "" && cached.Mergeable != "UNKNOWN" &&
		cached.MergeStateStatus != "" && cached.MergeStateStatus != "UNKNOWN" &&
		cached.Checks.IsSettled() &&
		(stale || now.Sub(savedAt) < s.maxReuseAgeFor(cached.RepoNameWithOwner, cached.Number))
}

// hydrateTarget pairs a discovered PR with cached stable fields when available.
type hydrateTarget struct {
	discoveredPR
	stable    stableFields
	stableHit bool
}

func (s *GraphQLSource) planHydration(
	ctx context.Context,
	discovered []discoveredPR,
) (map[model.PRKey]flaggedPR, []hydrateTarget) {
	now := time.Now()
	reused := make(map[model.PRKey]flaggedPR)
	var toHydrate []hydrateTarget

	for _, d := range discovered {
		if s.cache != nil {
			cached, savedAt, ok := s.cache.PR(
				ctx,
				d.ident.Repository.NameWithOwner,
				d.ident.Number,
			)
			if ok && s.canReuse(cached, savedAt, d, now) {
				cached.Title = d.ident.Title
				cached.IsDraft = d.ident.IsDraft
				cached.IsInMergeQueue = d.ident.IsInMergeQueue
				cached.UpdatedAt = d.ident.UpdatedAt
				cached.Author = d.ident.Author.Login
				cached.URL = d.ident.URL
				cached.Assigned = d.assigned
				cached.Stack = convertStack(d.ident.Stack, d.ident.StackEntry)
				cached.MergeStatus = model.ComputeMergeStatus(
					cached.Mergeable,
					cached.MergeStateStatus,
					d.ident.IsDraft,
				)
				cached.MergeStatus.IsInMergeQueue = d.ident.IsInMergeQueue
				staleThreshold := s.staleActivityAfter
				if staleThreshold <= 0 {
					staleThreshold = defaultStaleActivityAfter
				}
				cached.Stale = !d.ident.UpdatedAt.IsZero() && time.Since(d.ident.UpdatedAt) >= staleThreshold

				reused[d.key()] = flaggedPR{
					PullRequest: cached,
					authored:    d.authored,
				}

				// Touch file mtime so retention prune does not evict an open PR,
				// while leaving SavedAt intact to track actual hydration time.
				if err := s.cache.TouchPR(
					ctx,
					d.ident.Repository.NameWithOwner,
					d.ident.Number,
				); err != nil {
					s.logger.Warn().Err(err).Msg("touching reused PR cache failed; continuing")
				}
				continue
			}

			target := hydrateTarget{discoveredPR: d}
			if ok && cached.HeadRefOID == d.ident.HeadRefOID {
				target.stable = stableFromCached(cached)
				target.stableHit = true
			}
			toHydrate = append(toHydrate, target)
			continue
		}
		toHydrate = append(toHydrate, hydrateTarget{discoveredPR: d})
	}
	return reused, toHydrate
}

// hydrate fetches full PR data for the discovered set in parallel aliased batches,
// bisecting failing batches down to singletons to isolate cost-driven 502s.
func (s *GraphQLSource) hydrate(ctx context.Context, discovered []discoveredPR) ([]flaggedPR, error) {
	if len(discovered) == 0 {
		return nil, nil
	}

	reused, toHydrate := s.planHydration(ctx, discovered)

	s.logger.Info().
		Int("discovered_total", len(discovered)).
		Int("reused_total", len(reused)).
		Int("to_hydrate_total", len(toHydrate)).
		Msg("planned hydration")

	if len(toHydrate) == 0 {
		if progress := ProgressFromContext(ctx); progress != nil {
			progress(len(discovered), len(discovered))
		}
		return s.assembleHydrated(discovered, reused, nil, false), nil
	}

	batchSize := s.hydrateBatchSize
	if batchSize <= 0 {
		batchSize = hydrateBatchSize
	}
	limit := s.hydrateLimit
	if limit <= 0 {
		limit = defaultHydrateConcurrency
	}

	batches := prepareHydrateBatches(toHydrate, batchSize)
	results := make([][]flaggedPR, len(batches))

	var completedCount atomic.Int32
	// #nosec G115 -- reused count is bounded by discovery size
	completedCount.Store(int32(len(reused)))
	if len(reused) > 0 {
		if progress := ProgressFromContext(ctx); progress != nil {
			progress(len(reused), len(discovered))
		}
	}

	var lastErrMu sync.Mutex
	var lastErr error
	recordErr := func(err error) {
		lastErrMu.Lock()
		lastErr = err
		lastErrMu.Unlock()
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)

	for bIdx, b := range batches {
		g.Go(func() error {
			prs, err := s.hydrateBatchBisect(gctx, b.targets, b.full, recordErr)
			if err != nil {
				return err
			}
			results[bIdx] = prs
			// #nosec G115 -- batch size is small and bounded
			done := completedCount.Add(int32(len(b.targets)))
			if progress := ProgressFromContext(gctx); progress != nil {
				progress(int(done), len(discovered))
			}
			return nil
		})
	}

	waitErr := g.Wait()
	if waitErr != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	hydratedMap := make(map[model.PRKey]flaggedPR)
	for _, batchResult := range results {
		for _, pr := range batchResult {
			hydratedMap[pr.Key()] = pr
		}
	}

	isSecondary := waitErr != nil && errors.Is(waitErr, ErrSecondaryRateLimit)
	if waitErr != nil && !isSecondary {
		return nil, waitErr
	}

	if isSecondary && len(reused) == 0 && len(hydratedMap) == 0 {
		return nil, waitErr
	}

	if isSecondary {
		s.logger.Warn().
			Err(waitErr).
			Int("hydrated_total", len(hydratedMap)).
			Int("reused_total", len(reused)).
			Int("discovered_total", len(discovered)).
			Msg("secondary rate limit hit during hydration; returning degraded queue with discovery fallback")
	}

	finalPRs := s.assembleHydrated(discovered, reused, hydratedMap, isSecondary)
	if len(discovered) > 0 && len(finalPRs) == 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("all pull requests failed hydration: %w", lastErr)
		}
		return nil, errors.New("all pull requests failed hydration")
	}

	return finalPRs, nil
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

func (s *GraphQLSource) assembleHydrated(
	discovered []discoveredPR,
	reused map[model.PRKey]flaggedPR,
	hydratedMap map[model.PRKey]flaggedPR,
	isSecondary bool,
) []flaggedPR {
	finalPRs := make([]flaggedPR, 0, len(discovered))
	for _, d := range discovered {
		if pr, ok := reused[d.key()]; ok {
			finalPRs = append(finalPRs, pr)
		} else if pr, ok := hydratedMap[d.key()]; ok {
			finalPRs = append(finalPRs, pr)
		} else if isSecondary {
			finalPRs = append(finalPRs, flaggedPR{
				PullRequest: convertPR(d.ident, stableFields{}, rawFresh{}, d.assigned, convertOpts{
					staleActivityAfter: s.staleActivityAfter,
				}),
				authored: d.authored,
			})
		}
	}
	return finalPRs
}

func (s *GraphQLSource) hydrateBatchBisect(
	ctx context.Context,
	batch []hydrateTarget,
	full bool,
	recordErr func(error),
) ([]flaggedPR, error) {
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
			Msg("PR hydration failed persistently; dropping from queue")
		return nil, nil
	}
	return prs, nil
}

func (s *GraphQLSource) executeHydrateQuery(
	ctx context.Context,
	batch []hydrateTarget,
	full bool,
) ([]flaggedPR, error) {
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
		return nil, fmt.Errorf("hydrate PR batch: %w", err)
	}
	elapsedMs := time.Since(startHydrate).Milliseconds()

	s.logger.Info().
		Int("cost", resp.RateLimit.Cost).
		Int("remaining", resp.RateLimit.Remaining).
		Int("batch_count", len(batch)).
		Int64("elapsed_ms", elapsedMs).
		Msg("hydrated pr batch")

	prs := make([]flaggedPR, 0, len(batch))
	for i, d := range batch {
		if i >= len(resp.Nodes) {
			break
		}
		node := resp.Nodes[i]
		if node == nil {
			s.logger.Warn().
				Str("repo", d.ident.Repository.NameWithOwner).
				Int("number", d.ident.Number).
				Msg("PR vanished between discovery and hydration; skipping")
			continue
		}

		stable := d.stable
		if full {
			stable = stableFromWire(node.rawStable)
		}

		pr := flaggedPR{
			PullRequest: convertPR(d.ident, stable, node.rawFresh, d.assigned, convertOpts{
				staleActivityAfter: s.staleActivityAfter,
			}),
			authored: d.authored,
		}
		if s.cache != nil {
			if err := s.cache.SavePR(
				ctx,
				d.ident.Repository.NameWithOwner,
				d.ident.Number,
				pr.PullRequest,
			); err != nil {
				s.logger.Warn().Err(err).Msg("saving PR cache failed; continuing")
			}
		}

		prs = append(prs, pr)
	}

	return prs, nil
}

// flaggedPR is a hydrated pull request with its discovery flags.
type flaggedPR struct {
	model.PullRequest
	authored bool
}
