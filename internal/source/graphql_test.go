package source_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/source"
)

// --- fake GitHub server ----------------------------------------------------

type discoveryPage struct {
	nodes       string // JSON array of light discovery nodes
	issueCount  int
	hasNextPage bool
}

type testReviewRequest struct {
	at   time.Time
	user string
	team string
}

type testReview struct {
	author      string
	state       string
	commitOID   string
	submittedAt time.Time
}

type testCommit struct {
	oid string
	at  time.Time
}

type testCheckContext struct {
	name        string
	status      string
	conclusion  string
	context     string
	state       string
	startedAt   *time.Time
	completedAt *time.Time
	createdAt   *time.Time
}

type checkSpec = testCheckContext

// prSpec describes a pull request across both fetch phases.
type prSpec struct {
	id                     string
	num                    int
	title                  string
	repo                   string // "owner/name"
	author                 string
	oid                    string
	isDraft                bool
	updatedAt              time.Time
	additions              int
	deletions              int
	changedFiles           int
	files                  []string
	mergeable              string
	mergeState             string
	reviewRequests         []testReviewRequest
	lastCommit             *testCommit
	latestReviews          []testReview
	requiredContexts       []string
	checkContexts          []testCheckContext
	omitCommitFromTimeline bool
	stackID                string
	stackNum               int
	stackSize              int
	stackPos               int
	stackBase              string
}

func (s prSpec) lightJSON() string {
	updatedAt := "2026-09-09T10:00:00Z"
	if !s.updatedAt.IsZero() {
		updatedAt = s.updatedAt.Format(time.RFC3339)
	}
	id := s.id
	if id == "" {
		id = fmt.Sprintf("%s#%d", s.repo, s.num)
	}
	stackJSON := `,"stack": null, "stackEntry": null`
	if s.stackID != "" {
		stackJSON = fmt.Sprintf(
			`,"stack": {"id": %q, "number": %d, "size": %d, "baseRefName": %q}, "stackEntry": {"id": "SE_%s", "position": %d}`,
			s.stackID,
			s.stackNum,
			s.stackSize,
			s.stackBase,
			s.stackID,
			s.stackPos,
		)
	}
	return fmt.Sprintf(`{
		"id": %q, "number": %d, "title": %q, "url": "https://github.com/%s/pull/%d",
		"isDraft": %t, "updatedAt": %q, "headRefOid": %q,
		"author": {"login": %q}, "repository": {"nameWithOwner": %q}%s
	}`, id, s.num, s.title, s.repo, s.num, s.isDraft, updatedAt, s.oid, s.author, s.repo, stackJSON)
}

func (s prSpec) hydrateJSON() string {
	fileNodes := make([]string, 0, len(s.files))
	for _, f := range s.files {
		fileNodes = append(fileNodes, fmt.Sprintf(`{"path": %q}`, f))
	}

	reviewNodes := make([]string, 0, len(s.latestReviews))
	for _, r := range s.latestReviews {
		reviewNodes = append(reviewNodes, fmt.Sprintf(`{
			"author": {"login": %q},
			"state": %q,
			"commit": {"oid": %q},
			"submittedAt": %q
		}`, r.author, r.state, r.commitOID, r.submittedAt.Format(time.RFC3339)))
	}

	timelineNodes := make([]string, 0, len(s.reviewRequests))
	for _, rr := range s.reviewRequests {
		reqReviewer := "{}"
		if rr.user != "" {
			reqReviewer = fmt.Sprintf(`{"login": %q}`, rr.user)
		} else if rr.team != "" {
			org, slug, found := strings.Cut(rr.team, "/")
			if !found {
				slug = rr.team
				org = ""
			}
			reqReviewer = fmt.Sprintf(`{"slug": %q, "organization": {"login": %q}}`, slug, org)
		}
		timelineNodes = append(timelineNodes, fmt.Sprintf(`{
			"__typename": "ReviewRequestedEvent",
			"createdAt": %q,
			"requestedReviewer": %s
		}`, rr.at.Format(time.RFC3339), reqReviewer))
	}
	if s.lastCommit != nil && !s.omitCommitFromTimeline {
		timelineNodes = append(timelineNodes, fmt.Sprintf(`{
			"__typename": "PullRequestCommit",
			"commit": {
				"oid": %q,
				"committedDate": %q
			}
		}`, s.lastCommit.oid, s.lastCommit.at.Format(time.RFC3339)))
	}

	baseRefJSON := "null"
	if s.requiredContexts != nil {
		ctxs := make([]string, 0, len(s.requiredContexts))
		for _, c := range s.requiredContexts {
			ctxs = append(ctxs, fmt.Sprintf("%q", c))
		}
		baseRefJSON = fmt.Sprintf(
			`{"branchProtectionRule": {"requiredStatusCheckContexts": [%s]}}`,
			strings.Join(ctxs, ","),
		)
	}

	commitsJSON := `{"nodes": []}`
	if s.lastCommit != nil || len(s.checkContexts) > 0 {
		var checkNodes []string
		for _, c := range s.checkContexts {
			if c.context != "" {
				extra := ""
				if c.createdAt != nil && !c.createdAt.IsZero() {
					extra += fmt.Sprintf(`, "createdAt": %q`, c.createdAt.Format(time.RFC3339))
				}
				checkNodes = append(checkNodes, fmt.Sprintf(`{
					"__typename": "StatusContext",
					"context": %q,
					"state": %q%s
				}`, c.context, c.state, extra))
			} else {
				extra := ""
				if c.startedAt != nil && !c.startedAt.IsZero() {
					extra += fmt.Sprintf(`, "startedAt": %q`, c.startedAt.Format(time.RFC3339))
				}
				if c.completedAt != nil && !c.completedAt.IsZero() {
					extra += fmt.Sprintf(`, "completedAt": %q`, c.completedAt.Format(time.RFC3339))
				}
				checkNodes = append(checkNodes, fmt.Sprintf(`{
					"__typename": "CheckRun",
					"name": %q,
					"status": %q,
					"conclusion": %q%s
				}`, c.name, c.status, c.conclusion, extra))
			}
		}
		rollupJSON := "null"
		if len(s.checkContexts) > 0 {
			rollupJSON = fmt.Sprintf(`{
				"state": "SUCCESS",
				"contexts": {"nodes": [%s]}
			}`, strings.Join(checkNodes, ","))
		}
		commitNodeJSON := `"statusCheckRollup": ` + rollupJSON
		if s.lastCommit != nil {
			commitNodeJSON = fmt.Sprintf(`"oid": %q, "committedDate": %q, %s`,
				s.lastCommit.oid, s.lastCommit.at.Format(time.RFC3339), commitNodeJSON)
		}
		commitsJSON = fmt.Sprintf(`{"nodes": [{"commit": {%s}}]}`, commitNodeJSON)
	}

	return fmt.Sprintf(
		`{
		"createdAt": "2026-09-08T10:00:00Z",
		"additions": %d, "deletions": %d, "changedFiles": %d,
		"files": {"nodes": [%s]},
		"mergeable": %q, "mergeStateStatus": %q, "reviewDecision": "",
		"latestReviews": {"nodes": [%s]},
		"timelineItems": {"nodes": [%s]},
		"baseRef": %s,
		"commits": %s
	}`, s.additions, s.deletions, s.changedFiles, strings.Join(fileNodes, ","),
		s.mergeable, s.mergeState,
		strings.Join(reviewNodes, ","),
		strings.Join(timelineNodes, ","),
		baseRefJSON,
		commitsJSON,
	)
}

// fakeGitHub serves identity, discovery, and hydration queries from static data.
type fakeGitHub struct {
	login      string
	orgTeams   map[string][]string // org login → team slugs
	searches   map[string][]discoveryPage
	searchErrs map[string]error
	prs        map[string]map[int]string // "owner/name" → number → hydrate node JSON

	// hydrateFailures is the number of leading Hydrate requests that fail with
	// hydrateFailStatus (502 when unset) before normal responses resume.
	// hydrateFailRetryAfter sets a Retry-After header on injected failures.
	hydrateFailures           atomic.Int32
	hydrateFailCode           atomic.Int32
	hydrateFailRetryAft       atomic.Bool
	hydrateFailSecondary      atomic.Bool
	hydrateFailSecondaryAfter atomic.Int32
	hydrateFailAboveAliases   atomic.Int32
	hydrateFailForPR          map[string]bool

	// discoveryFailPrimaryLimit fails every discovery query with GitHub's
	// primary rate limit 403 body.
	discoveryFailPrimaryLimit atomic.Bool
	// hydrateFailPrimaryLimit swaps the injected hydrate failure body for
	// GitHub's primary rate limit 403 message.
	hydrateFailPrimaryLimit atomic.Bool
	// rateLimitRemaining and rateLimitResetAt override the rateLimit block
	// served on discovery and hydrate responses; zero values keep defaults.
	rateLimitRemaining int
	rateLimitResetAt   string

	loginCalls           atomic.Int32
	teamsCalls           atomic.Int32
	hydrateCalls         atomic.Int32
	discoveryCalls       atomic.Int32
	discoveryDelay       time.Duration
	discoveryInFlight    atomic.Int32
	maxDiscoveryInFlight atomic.Int32
	hydrateDelay         time.Duration
	hydrateInFlight      atomic.Int32
	maxHydrateInFlight   atomic.Int32
	orgsHasNextPage      bool
	teamsHasNextPage     map[string]bool
	inFlight             atomic.Int32
	maxInFlight          atomic.Int32
	mu                   sync.Mutex
	hydrateQueries       []string
	hydrateVars          []map[string]any
	discoveryQuery       string
	discoveryVars        []map[string]any
}

func (f *fakeGitHub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := f.inFlight.Add(1)
		for {
			maxVal := f.maxInFlight.Load()
			if cur <= maxVal || f.maxInFlight.CompareAndSwap(maxVal, cur) {
				break
			}
		}
		defer f.inFlight.Add(-1)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}

		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			panic(err)
		}

		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(req.Query, "ViewerLogin"):
			f.handleLogin(w)
		case strings.Contains(req.Query, "ViewerTeams"):
			f.handleTeams(w)
		case strings.Contains(req.Query, "SearchDiscovery"):
			f.handleDiscovery(r.Context(), w, req.Query, req.Variables)
		case strings.Contains(req.Query, "Hydrate"):
			f.handleHydrate(r.Context(), w, req.Query, req.Variables)
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"errors":[{"message":"unexpected query"}]}`)
		}
	})
}

func (f *fakeGitHub) handleLogin(w http.ResponseWriter) {
	f.loginCalls.Add(1)
	_, _ = fmt.Fprintf(w, `{"data":{"viewer":{"login":%q}}}`, f.login)
}

func (f *fakeGitHub) handleTeams(w http.ResponseWriter) {
	f.teamsCalls.Add(1)
	orgs := make([]string, 0, len(f.orgTeams))
	for org, slugs := range f.orgTeams {
		nodes := make([]string, 0, len(slugs))
		for _, s := range slugs {
			nodes = append(nodes, fmt.Sprintf(`{"slug": %q}`, s))
		}
		teamsTruncated := f.teamsHasNextPage != nil && f.teamsHasNextPage[org]
		orgs = append(
			orgs,
			fmt.Sprintf(
				`{"login": %q, "teams": {"pageInfo": {"hasNextPage": %t}, "nodes": [%s]}}`,
				org,
				teamsTruncated,
				strings.Join(nodes, ","),
			),
		)
	}
	_, _ = fmt.Fprintf(
		w,
		`{"data":{"viewer":{"organizations":{"pageInfo": {"hasNextPage": %t}, "nodes":[%s]}}}}`,
		f.orgsHasNextPage,
		strings.Join(orgs, ","),
	)
}

func (f *fakeGitHub) handleDiscovery(ctx context.Context, w http.ResponseWriter, query string, vars map[string]any) {
	f.discoveryCalls.Add(1)
	cur := f.discoveryInFlight.Add(1)
	for {
		maxVal := f.maxDiscoveryInFlight.Load()
		if cur <= maxVal || f.maxDiscoveryInFlight.CompareAndSwap(maxVal, cur) {
			break
		}
	}
	defer f.discoveryInFlight.Add(-1)

	if f.discoveryDelay > 0 {
		select {
		case <-time.After(f.discoveryDelay):
		case <-ctx.Done():
			return
		}
	}

	f.mu.Lock()
	f.discoveryQuery = query
	f.discoveryVars = append(f.discoveryVars, vars)
	f.mu.Unlock()

	qVar, _ := vars["query"].(string)
	if err := matchErr(f.searchErrs, qVar); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if f.discoveryFailPrimaryLimit.Load() {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"message":"API rate limit already exceeded for user ID 10038988."}`)
		return
	}

	pages := matchSearch(f.searches, qVar)
	rateLimitJSON := ""
	if strings.Contains(query, "rateLimit") {
		remaining, resetAt := f.rateLimit()
		rateLimitJSON = fmt.Sprintf(
			`,"rateLimit":{"cost":1,"limit":5000,"remaining":%d,"resetAt":%q}`,
			remaining,
			resetAt,
		)
	}
	if pages == nil {
		_, _ = fmt.Fprintf(
			w,
			`{"data":{"search":{"issueCount":0,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}%s}}`,
			rateLimitJSON,
		)
		return
	}
	page := 0
	if cursor, ok := vars["cursor"].(string); ok && cursor != "" {
		if _, err := fmt.Sscanf(cursor, "c%d", &page); err != nil {
			panic(err)
		}
		page++
	}
	if page >= len(pages) {
		_, _ = fmt.Fprintf(
			w,
			`{"data":{"search":{"issueCount":0,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}%s}}`,
			rateLimitJSON,
		)
		return
	}
	p := pages[page]
	cursor := "null"
	if p.hasNextPage {
		cursor = fmt.Sprintf(`"c%d"`, page)
	}
	_, _ = fmt.Fprintf(
		w,
		`{"data":{"search":{"issueCount":%d,"pageInfo":{"hasNextPage":%t,"endCursor":%s},"nodes":[%s]}%s}}`,
		p.issueCount,
		p.hasNextPage,
		cursor,
		p.nodes,
		rateLimitJSON,
	)
}

func (f *fakeGitHub) handleHydrate(ctx context.Context, w http.ResponseWriter, query string, vars map[string]any) {
	cur := f.hydrateInFlight.Add(1)
	for {
		maxVal := f.maxHydrateInFlight.Load()
		if cur <= maxVal || f.maxHydrateInFlight.CompareAndSwap(maxVal, cur) {
			break
		}
	}
	defer f.hydrateInFlight.Add(-1)

	if f.hydrateDelay > 0 {
		select {
		case <-time.After(f.hydrateDelay):
		case <-ctx.Done():
			return
		}
	}

	calls := f.hydrateCalls.Add(1)
	f.mu.Lock()
	f.hydrateQueries = append(f.hydrateQueries, query)
	f.hydrateVars = append(f.hydrateVars, vars)
	f.mu.Unlock()

	var ids []string
	if rawIDs, ok := vars["ids"].([]any); ok {
		for _, id := range rawIDs {
			if s, ok := id.(string); ok {
				ids = append(ids, s)
			}
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"errors":[{"message":"expected ids variable for static hydrate query"}]}`)
		return
	}

	if limit := int(f.hydrateFailAboveAliases.Load()); limit > 0 && len(ids) > limit {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, `{"message":"injected hydrate failure: exceeded batch limit"}`)
		return
	}

	if f.hydrateFailForPR != nil {
		for _, id := range ids {
			if f.hydrateFailForPR[id] {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = fmt.Fprint(w, `{"message":"injected hydrate failure for PR"}`)
				return
			}
		}
	}

	if calls <= f.hydrateFailures.Load() {
		status := int(f.hydrateFailCode.Load())
		if status == 0 {
			status = http.StatusBadGateway
		}
		if f.hydrateFailRetryAft.Load() {
			w.Header().Set("Retry-After", "1")
		}
		msg := "injected hydrate failure"
		if f.hydrateFailSecondary.Load() {
			msg = "You have exceeded a secondary rate limit. Please wait a few minutes before you try again."
		}
		if f.hydrateFailPrimaryLimit.Load() {
			msg = "API rate limit already exceeded for user ID 10038988."
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"message": %q}`, msg)
		return
	}

	if limit := f.hydrateFailSecondaryAfter.Load(); limit > 0 && calls > limit {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(
			w,
			`{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`,
		)
		return
	}

	nodes := make([]any, len(ids))
	for i, id := range ids {
		node, exists := f.prForID(id)
		if !exists {
			nodes[i] = nil
			continue
		}
		nodes[i] = json.RawMessage(node)
	}
	resp := map[string]any{
		"nodes": nodes,
	}
	if strings.Contains(query, "rateLimit") {
		remaining, resetAt := f.rateLimit()
		resp["rateLimit"] = map[string]any{
			"cost":      1,
			"limit":     5000,
			"remaining": remaining,
			"resetAt":   resetAt,
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": resp})
}

// rateLimit returns the remaining/resetAt pair served on rateLimit-bearing
// responses, honoring test overrides.
func (f *fakeGitHub) rateLimit() (int, string) {
	remaining, resetAt := 4999, "2026-09-09T23:00:00Z"
	if f.rateLimitRemaining > 0 {
		remaining = f.rateLimitRemaining
	}
	if f.rateLimitResetAt != "" {
		resetAt = f.rateLimitResetAt
	}
	return remaining, resetAt
}

func (f *fakeGitHub) prForID(id string) (string, bool) {
	parts := strings.Split(id, "#")
	if len(parts) == 2 {
		repo := parts[0]
		var num int
		if _, err := fmt.Sscanf(parts[1], "%d", &num); err == nil {
			if repoMap, ok := f.prs[repo]; ok {
				if node, ok := repoMap[num]; ok {
					return node, true
				}
			}
		}
	}
	return "", false
}

// matchSearch returns the discovery pages configured for a search qualifier.
func matchSearch(searches map[string][]discoveryPage, qVar string) []discoveryPage {
	for key, pages := range searches {
		if strings.Contains(qVar, key) {
			return pages
		}
	}
	return nil
}

// matchErr returns an injected error for a search qualifier, if any.
func matchErr(errs map[string]error, qVar string) error {
	for key, err := range errs {
		if strings.Contains(qVar, key) {
			return err
		}
	}
	return nil
}

type inMemoryTransport struct {
	handler http.Handler
}

func (t *inMemoryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	rec := httptest.NewRecorder()
	go func() {
		t.handler.ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-done:
		res := rec.Result()
		res.Request = req
		return res, nil
	}
}

func newTestGraphQLClient(t *testing.T, handler http.Handler) *api.GraphQLClient {
	t.Helper()
	client, err := api.NewGraphQLClient(api.ClientOptions{
		AuthToken: "mock-token",
		Transport: &inMemoryTransport{handler: handler},
	})
	require.NoError(t, err)
	return client
}

func discardLogger() zerolog.Logger {
	return zerolog.New(io.Discard)
}

// --- fake cache store ------------------------------------------------------

type fakeStore struct {
	id        cache.Identity
	idSavedAt time.Time
	idOK      bool

	prs   map[string]model.PullRequest // key: repo#num
	prsAt map[string]time.Time

	queue   model.Queue
	queueAt time.Time
	queueOK bool

	pruneCalls atomic.Int32
	prReads    atomic.Int32
	touchCalls atomic.Int32
	mu         sync.Mutex
	saves      []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		prs:   map[string]model.PullRequest{},
		prsAt: map[string]time.Time{},
	}
}

func (f *fakeStore) Identity(context.Context) (cache.Identity, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.id, f.idSavedAt, f.idOK
}

func (f *fakeStore) SaveIdentity(_ context.Context, id cache.Identity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.id, f.idSavedAt, f.idOK = id, time.Now(), true
	f.saves = append(f.saves, "identity")
	return nil
}

func (f *fakeStore) PR(_ context.Context, repo string, num int) (model.PullRequest, time.Time, bool) {
	f.prReads.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fmt.Sprintf("%s#%d", repo, num)
	pr, ok := f.prs[key]
	return pr, f.prsAt[key], ok
}

func (f *fakeStore) SavePR(_ context.Context, repo string, num int, pr model.PullRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fmt.Sprintf("%s#%d", repo, num)
	f.prs[key] = pr
	f.prsAt[key] = time.Now()
	f.saves = append(f.saves, fmt.Sprintf("pr:%s#%d", repo, num))
	return nil
}

func (f *fakeStore) TouchPR(_ context.Context, _ string, _ int) error {
	f.touchCalls.Add(1)
	return nil
}

func (f *fakeStore) PrunePRs(_ context.Context, _ time.Duration) (int, error) {
	f.pruneCalls.Add(1)
	return 0, nil
}

func (f *fakeStore) Queue(context.Context) (model.Queue, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queue, f.queueAt, f.queueOK
}

func (f *fakeStore) SaveQueue(_ context.Context, q model.Queue) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue, f.queueAt, f.queueOK = q, time.Now(), true
	f.saves = append(f.saves, "queue")
	return nil
}

func (f *fakeStore) Focus(context.Context) ([]model.PRKey, time.Time, bool) {
	return nil, time.Time{}, false
}

func (f *fakeStore) SaveFocus(context.Context, []model.PRKey) error { return nil }

//nolint:unparam // test helper matches PR signature
func (f *fakeStore) hasPR(repo string, num int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.prs[fmt.Sprintf("%s#%d", repo, num)]
	return ok
}

// --- tests -----------------------------------------------------------------

func discoverPRs(specs []prSpec) []discoveryPage {
	nodes := make([]string, 0, len(specs))
	for _, s := range specs {
		nodes = append(nodes, s.lightJSON())
	}
	return []discoveryPage{{nodes: strings.Join(nodes, ","), issueCount: len(specs)}}
}

func TestFetch_TwoPhaseDiscoveryAndHydration(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login:    "kalverra",
		orgTeams: map[string][]string{"myorg": {"engineers"}},
		searches: map[string][]discoveryPage{
			"author:@me": discoverPRs(
				[]prSpec{
					{
						num:        1,
						title:      "Authored PR 1",
						repo:       "myorg/repo",
						author:     "kalverra",
						oid:        "oid1",
						mergeable:  "MERGEABLE",
						mergeState: "CLEAN",
					},
				},
			),
			"review-requested:@me": discoverPRs(
				[]prSpec{
					{
						num:        2,
						title:      "Inbox PR 2",
						repo:       "myorg/repo",
						author:     "alice",
						oid:        "oid2",
						mergeable:  "MERGEABLE",
						mergeState: "BLOCKED",
					},
					{
						num:        3,
						title:      "Team PR 3",
						repo:       "myorg/repo",
						author:     "bob",
						oid:        "oid3",
						mergeable:  "MERGEABLE",
						mergeState: "CLEAN",
					},
				},
			),
			"assignee:@me": discoverPRs([]prSpec{
				{
					num:        2,
					title:      "Inbox PR 2",
					repo:       "myorg/repo",
					author:     "alice",
					oid:        "oid2",
					mergeable:  "MERGEABLE",
					mergeState: "BLOCKED",
				},
				{
					num:        4,
					title:      "Assigned only PR 4",
					repo:       "myorg/repo",
					author:     "charlie",
					oid:        "oid4",
					mergeable:  "MERGEABLE",
					mergeState: "CLEAN",
				},
			}),
		},
		prs: map[string]map[int]string{},
	}
	for _, spec := range []prSpec{
		{num: 1, additions: 10, deletions: 2, changedFiles: 1, files: []string{"main.go"}},
		{num: 2, additions: 40, deletions: 5, changedFiles: 2, files: []string{"feature.go", "feature_test.go"}},
		{num: 3, additions: 7, deletions: 1, changedFiles: 1, files: []string{"team.go"}},
		{num: 4, additions: 100, deletions: 20, changedFiles: 3, files: []string{"work.go", "util.go", "util_test.go"}},
	} {
		repo := "myorg/repo"
		if fake.prs[repo] == nil {
			fake.prs[repo] = map[int]string{}
		}
		fake.prs[repo][spec.num] = spec.hydrateJSON()
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)

	// Identity is surfaced on the queue for scoring call sites.
	assert.Equal(t, "kalverra", queue.Viewer)
	assert.Equal(t, []string{"myorg/engineers"}, queue.Teams)

	require.Len(t, queue.Authored, 1)
	assert.Equal(t, 1, queue.Authored[0].Number)
	assert.Equal(t, []string{"main.go"}, queue.Authored[0].Files)
	assert.Equal(t, 10, queue.Authored[0].Additions)

	require.Len(t, queue.Inbox, 3)
	var pr2, pr3, pr4 *model.PullRequest
	for i := range queue.Inbox {
		switch queue.Inbox[i].Number {
		case 2:
			pr2 = &queue.Inbox[i]
		case 3:
			pr3 = &queue.Inbox[i]
		case 4:
			pr4 = &queue.Inbox[i]
		}
	}
	require.NotNil(t, pr2, "PR 2 must be present once despite appearing in two searches")
	assert.True(t, pr2.Assigned)
	assert.NotNil(t, pr3)
	assert.NotNil(t, pr4)
	assert.True(t, pr4.Assigned)

	// Discovery queries must stay light: no heavy nested fields.
	assert.NotContains(t, fake.discoveryQuery, "files(first")
	assert.NotContains(t, fake.discoveryQuery, "timelineItems")

	// PR 2 appeared in two searches but must be hydrated exactly once:
	// one hydrate request total, containing 4 aliases.
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())
	assert.Contains(t, fake.hydrateQueries[0], "...StableFields")
	assert.Contains(t, fake.hydrateQueries[0], "...FreshFields")
}

func TestFetch_Pagination(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login:    "kalverra",
		orgTeams: map[string][]string{},
		searches: map[string][]discoveryPage{
			"author:@me": {
				{
					nodes: prSpec{
						num:    101,
						title:  "Authored Page 1",
						repo:   "org/repo",
						author: "kalverra",
						oid:    "oid101",
					}.lightJSON(),
					issueCount:  2,
					hasNextPage: true,
				},
				{
					nodes: prSpec{
						num:    102,
						title:  "Authored Page 2",
						repo:   "org/repo",
						author: "kalverra",
						oid:    "oid102",
					}.lightJSON(),
					issueCount: 2,
				},
			},
		},
		prs: map[string]map[int]string{
			"org/repo": {
				101: prSpec{
					num:          101,
					additions:    1,
					deletions:    1,
					changedFiles: 1,
					files:        []string{"a.go"},
					mergeable:    "MERGEABLE",
					mergeState:   "CLEAN",
				}.hydrateJSON(),
				102: prSpec{
					num:          102,
					additions:    1,
					deletions:    1,
					changedFiles: 1,
					files:        []string{"b.go"},
					mergeable:    "MERGEABLE",
					mergeState:   "CLEAN",
				}.hydrateJSON(),
			},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)

	require.Len(t, queue.Authored, 2)
	assert.Equal(t, 101, queue.Authored[0].Number)
	assert.Equal(t, 102, queue.Authored[1].Number)
	assert.GreaterOrEqual(t, fake.discoveryCalls.Load(), int32(2), "two pages must be fetched")
}

func TestFetch_FilesTruncated(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login:    "kalverra",
		orgTeams: map[string][]string{},
		searches: map[string][]discoveryPage{
			"author:@me": discoverPRs(
				[]prSpec{{num: 500, title: "Huge PR", repo: "org/repo", author: "kalverra", oid: "oid500"}},
			),
		},
		prs: map[string]map[int]string{
			"org/repo": {
				500: prSpec{
					num:          500,
					additions:    5000,
					deletions:    2000,
					changedFiles: 150,
					files:        []string{"file1.go"},
					mergeable:    "MERGEABLE",
					mergeState:   "CLEAN",
				}.hydrateJSON(),
			},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)

	require.Len(t, queue.Authored, 1)
	assert.True(t, queue.Authored[0].FilesTruncated)
	assert.Equal(t, 150, queue.Authored[0].ChangedFiles)
}

func TestFetch_SearchOverflowFailsPrimary(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"author:@me": {{nodes: "", issueCount: 1000}},
		},
		prs: map[string]map[int]string{},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.ErrorIs(t, err, source.ErrSearchOverflow)
}

func TestFetch_AssigneeSearchOverflowDegrades(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"assignee:@me": {{nodes: "", issueCount: 1000}},
		},
		prs: map[string]map[int]string{},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Empty(t, queue.Authored)
	assert.Empty(t, queue.Inbox)
}

func TestFetch_PrimarySearchErrorFailsFetch(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login: "kalverra",
		searchErrs: map[string]error{
			"author:@me": errors.New("boom"),
		},
		prs: map[string]map[int]string{},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
}

func TestFetch_NullHydratedPRSkipped(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"author:@me": discoverPRs([]prSpec{
				{num: 1, title: "Live PR", repo: "org/repo", author: "kalverra", oid: "oid1"},
				{num: 2, title: "Closed between phases", repo: "org/repo", author: "kalverra", oid: "oid2"},
			}),
		},
		prs: map[string]map[int]string{
			"org/repo": {
				1: prSpec{
					num:          1,
					additions:    1,
					deletions:    1,
					changedFiles: 1,
					files:        []string{"a.go"},
					mergeable:    "MERGEABLE",
					mergeState:   "CLEAN",
				}.hydrateJSON(),
			},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Authored, 1)
	assert.Equal(t, 1, queue.Authored[0].Number)
}

func TestFetch_HydrationBatchesRespectMaxAliases(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 30)
	prs := map[string]map[int]string{"org/repo": {}}
	for i := 1; i <= 30; i++ {
		specs = append(
			specs,
			prSpec{
				num:    i,
				title:  fmt.Sprintf("PR %d", i),
				repo:   "org/repo",
				author: "alice",
				oid:    fmt.Sprintf("oid%d", i),
			},
		)
		prs["org/repo"][i] = prSpec{
			num:          i,
			additions:    1,
			deletions:    1,
			changedFiles: 1,
			files:        []string{"a.go"},
			mergeable:    "MERGEABLE",
			mergeState:   "CLEAN",
		}.hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: prs,
	}

	client := newTestGraphQLClient(t, fake.handler())
	maxAliases := 6
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithHydrateBatchSize(maxAliases),
	)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 30)
	// ceil(30 / 6) = 5 batched requests
	expectedBatches := 5
	assert.Equal(
		t,
		int32(expectedBatches),
		fake.hydrateCalls.Load(),
		"30 PRs must hydrate in ceil(30/maxAliases) batched requests",
	)

	fake.mu.Lock()
	for _, vars := range fake.hydrateVars {
		rawIDs, ok := vars["ids"].([]any)
		require.True(t, ok)
		assert.LessOrEqual(t, len(rawIDs), maxAliases, "hydrate queries must cap at maxAliases")
	}
	fake.mu.Unlock()
}

func TestFetch_IdentityCacheTTL(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login:    "kalverra",
		orgTeams: map[string][]string{"myorg": {"engineers"}},
		prs:      map[string]map[int]string{},
	}
	store := newFakeStore()

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()), source.WithCache(store))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.loginCalls.Load())
	assert.Equal(t, int32(1), fake.teamsCalls.Load())

	// Fresh identity cache: no login/teams queries on the second fetch.
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.loginCalls.Load())
	assert.Equal(t, int32(1), fake.teamsCalls.Load())

	// Stale identity (older than TTL) triggers re-resolution.
	store.mu.Lock()
	store.idSavedAt = time.Now().Add(-25 * time.Hour)
	store.mu.Unlock()

	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fake.loginCalls.Load())
	assert.Equal(t, int32(2), fake.teamsCalls.Load())
}

func TestFetch_UnsettledCacheHitStillSkipsStableFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)

	spec := prSpec{
		num: 7, title: "Cached PR", repo: "org/repo", author: "alice", oid: "oid7",
		additions: 10, deletions: 2, changedFiles: 1, files: []string{"main.go"},
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "IN_PROGRESS"}},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(
				[]prSpec{{num: 7, title: "Cached PR", repo: "org/repo", author: "alice", oid: "oid7"}},
			),
		},
		prs: map[string]map[int]string{
			"org/repo": {7: spec.hydrateJSON()},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()), source.WithCache(store))

	// First fetch: full hydration including stable fields, which are cached.
	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 1)
	assert.Equal(t, 10, queue.Inbox[0].Additions)
	assert.Equal(t, []string{"main.go"}, queue.Inbox[0].Files)

	fake.mu.Lock()
	firstQuery := fake.hydrateQueries[0]
	fake.mu.Unlock()
	assert.Contains(t, firstQuery, "...StableFields", "cache miss must request stable fields")

	// Mutate the server's stable data; the cache must win on the second fetch.
	fake.prs["org/repo"][7] = prSpec{
		num: 7, additions: 999, deletions: 999, changedFiles: 99,
		files: []string{"changed.go"}, mergeable: "MERGEABLE", mergeState: "CLEAN",
	}.hydrateJSON()

	queue, err = src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 1)
	assert.Equal(t, 10, queue.Inbox[0].Additions, "stable fields must come from cache")
	assert.Equal(t, []string{"main.go"}, queue.Inbox[0].Files)

	fake.mu.Lock()
	secondQuery := fake.hydrateQueries[len(fake.hydrateQueries)-1]
	fake.mu.Unlock()
	assert.NotContains(t, secondQuery, "...StableFields", "cache hit must skip stable fields")
	assert.NotContains(
		t,
		secondQuery,
		"fragment StableFields",
		"GitHub rejects queries that define unused fragments; a fully-cached batch must omit the definition",
	)
	assert.Contains(t, secondQuery, "...FreshFields", "fresh fields must still be fetched")
}

func TestFetch_PartialFailureWithoutCacheStillWorks(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"author:@me": discoverPRs(
				[]prSpec{{num: 1, title: "Authored", repo: "org/repo", author: "kalverra", oid: "oid1"}},
			),
		},
		prs: map[string]map[int]string{
			"org/repo": {
				1: prSpec{
					num:          1,
					additions:    1,
					deletions:    1,
					changedFiles: 1,
					files:        []string{"a.go"},
					mergeable:    "MERGEABLE",
					mergeState:   "CLEAN",
				}.hydrateJSON(),
			},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Authored, 1)
}

func TestGraphQLSource_JSON_Roundtrip(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            1,
				Title:             "Test PR",
				RepoNameWithOwner: "org/repo",
			},
		},
	}

	data, err := json.Marshal(q)
	require.NoError(t, err)

	var decoded model.Queue
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Len(t, decoded.Authored, 1)
	assert.Equal(t, "Test PR", decoded.Authored[0].Title)
}

func TestGraphQLSource_ReportsFetchProgress(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login: "alice",
		searches: map[string][]discoveryPage{
			"author:@me": discoverPRs([]prSpec{
				{num: 1, repo: "org/repo", oid: "a1"},
				{num: 2, repo: "org/repo", oid: "a2"},
			}),
		},
		prs: map[string]map[int]string{
			"org/repo": {
				1: prSpec{
					num:          1,
					additions:    1,
					deletions:    1,
					changedFiles: 1,
					files:        []string{"a.go"},
					mergeable:    "MERGEABLE",
					mergeState:   "CLEAN",
				}.hydrateJSON(),
				2: prSpec{
					num:          2,
					additions:    2,
					deletions:    2,
					changedFiles: 1,
					files:        []string{"b.go"},
					mergeable:    "MERGEABLE",
					mergeState:   "CLEAN",
				}.hydrateJSON(),
			},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	type progressReport struct {
		loaded int
		total  int
	}
	var reports []progressReport
	var mu sync.Mutex
	ctx := source.WithProgress(context.Background(), func(loaded, total int) {
		mu.Lock()
		defer mu.Unlock()
		reports = append(reports, progressReport{loaded: loaded, total: total})
	})

	queue, err := src.Fetch(ctx)
	require.NoError(t, err)
	assert.Len(t, queue.Authored, 2)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, reports)
	assert.Equal(t, progressReport{loaded: 0, total: 2}, reports[0], "first report must be 0/total after discovery")
	assert.Equal(
		t,
		progressReport{loaded: 2, total: 2},
		reports[len(reports)-1],
		"last report must be total/total after hydration",
	)
}

func TestFetch_RequestsRateLimitCost(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
		additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
		mergeable: "MERGEABLE", mergeState: "CLEAN",
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	assert.Contains(t, fake.discoveryQuery, "rateLimit", "discovery query must request rateLimit block")
	require.NotEmpty(t, fake.hydrateQueries, "hydrate query must be recorded")
	assert.Contains(t, fake.hydrateQueries[0], "rateLimit", "hydrate query must request rateLimit block")
}

func TestFetch_RespectsFetchDeadline(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
		additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
		mergeable: "MERGEABLE", mergeState: "CLEAN",
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
			fake.handler().ServeHTTP(w, r)
		}
	})

	client := newTestGraphQLClient(t, slowHandler)
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := src.Fetch(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "error must wrap context.DeadlineExceeded")
}

// A caller-imposed deadline governs the fetch: short caller deadline beats
// the long default and fetch hits it.
func TestFetch_CallerDeadlineGovernsFetch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	t.Parallel()

	spec := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
		additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
		mergeable: "MERGEABLE", mergeState: "CLEAN",
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}
	fake.hydrateDelay = 50 * time.Millisecond

	client := newTestGraphQLClient(t, fake.handler())

	// Short caller deadline beats the long default: fetch must hit it.
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := src.Fetch(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "caller deadline must cap the fetch below the default")
}

func TestFetch_DiscoveryIssuesExactlyThreeSearches(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login:    "kalverra",
		orgTeams: map[string][]string{"myorg": {"engineers", "platform", "infra"}},
		searches: map[string][]discoveryPage{},
		prs:      map[string]map[int]string{},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	assert.Equal(
		t,
		int32(3),
		fake.discoveryCalls.Load(),
		"must issue exactly 3 discovery searches regardless of team count",
	)
}

func TestFetch_DoesNotIssueTeamReviewRequestedQuery(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login:    "kalverra",
		orgTeams: map[string][]string{"myorg": {"engineers", "platform"}},
		searches: map[string][]discoveryPage{},
		prs:      map[string]map[int]string{},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	for _, vars := range fake.discoveryVars {
		query, _ := vars["query"].(string)
		assert.NotContains(t, query, "team-review-requested:", "must not issue per-team review requested searches")
	}
}

func TestFetch_TeamRequestedPRsStillReachInbox(t *testing.T) {
	t.Parallel()

	reqTime := time.Now().Add(-3 * time.Hour)
	spec := prSpec{
		num:        10,
		title:      "Team PR",
		repo:       "myorg/repo",
		author:     "alice",
		oid:        "oid10",
		mergeable:  "MERGEABLE",
		mergeState: "CLEAN",
		reviewRequests: []testReviewRequest{
			{team: "myorg/engineers", at: reqTime},
		},
	}

	fake := &fakeGitHub{
		login:    "kalverra",
		orgTeams: map[string][]string{"myorg": {"engineers"}},
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"myorg/repo": {10: spec.hydrateJSON()},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)

	require.Len(t, queue.Inbox, 1)
	pr := queue.Inbox[0]
	assert.Equal(t, 10, pr.Number)
	assert.InDelta(t, 3.0, pr.WaitHours(time.Now(), queue.Viewer, queue.Teams), 0.1)
}

func TestFetch_DiscoveryUsesMaxPageSize(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login:    "kalverra",
		orgTeams: map[string][]string{},
		searches: map[string][]discoveryPage{},
		prs:      map[string]map[int]string{},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	require.NotEmpty(t, fake.discoveryVars)
	for _, vars := range fake.discoveryVars {
		assert.InDelta(t, 100.0, vars["first"], 0.001, "discovery search page size must be 100")
	}
}

func TestFetch_DiscoveryRespectsConcurrencyLimit(t *testing.T) {
	t.Parallel()

	fake := &fakeGitHub{
		login:          "kalverra",
		orgTeams:       map[string][]string{},
		discoveryDelay: 50 * time.Millisecond,
		searches:       map[string][]discoveryPage{},
		prs:            map[string]map[int]string{},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithDiscoveryConcurrency(2),
		source.WithLogger(discardLogger()),
	)

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	assert.Equal(
		t,
		int32(2),
		fake.maxDiscoveryInFlight.Load(),
		"concurrency limit must bound in-flight discovery searches",
	)
}

func TestResolveIdentity_WarnsOnTruncatedTeamList(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := zerolog.New(zerolog.SyncWriter(&buf))

	fake := &fakeGitHub{
		login:            "kalverra",
		orgTeams:         map[string][]string{"bigorg": {"t1"}},
		orgsHasNextPage:  true,
		teamsHasNextPage: map[string]bool{"bigorg": true},
		searches:         map[string][]discoveryPage{},
		prs:              map[string]map[int]string{},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(logger))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	logOutput := buf.String()
	assert.Contains(t, logOutput, "viewer organizations truncated")
	assert.Contains(t, logOutput, "viewer teams truncated")
	assert.Contains(t, logOutput, "bigorg")
}

func TestFetch_TimelineQueryRequestsOnlyReviewRequests(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)

	require.NotEmpty(t, fake.hydrateQueries)
	assert.Contains(t, fake.hydrateQueries[0], "timelineItems(itemTypes: [REVIEW_REQUESTED_EVENT], last: 20)")
	assert.NotContains(t, fake.hydrateQueries[0], "PULL_REQUEST_COMMIT")
	assert.Contains(t, fake.hydrateQueries[0], "oid")
	assert.Contains(t, fake.hydrateQueries[0], "committedDate")
}

func TestFetch_LatestCommitDrivesAuthorActive(t *testing.T) {
	t.Parallel()

	now := time.Now()
	reqAt := now.Add(-2 * time.Hour)
	commitAt := now.Add(-1 * time.Hour)

	spec := prSpec{
		num:       1,
		title:     "PR 1",
		repo:      "org/repo",
		author:    "alice",
		oid:       "oid1",
		mergeable: "MERGEABLE",
		reviewRequests: []testReviewRequest{
			{user: "kalverra", at: reqAt},
		},
		latestReviews: []testReview{
			{author: "kalverra", state: "COMMENTED", commitOID: "old-oid", submittedAt: reqAt},
		},
		lastCommit: &testCommit{
			oid: "new-commit-oid",
			at:  commitAt,
		},
		omitCommitFromTimeline: true,
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)

	require.Len(t, queue.Inbox, 1)
	pr := queue.Inbox[0]
	require.NotEmpty(t, pr.Commits, "commits must be populated from commits(last: 1)")
	assert.Equal(t, "new-commit-oid", pr.Commits[0].OID)
	assert.False(
		t,
		pr.AuthorIdle(queue.Viewer, queue.Teams),
		"author pushed commit after review request; must not be idle",
	)
	assert.True(t, pr.HasNewCommitsSinceReview(queue.Viewer), "author pushed commit after viewer review")
}

func TestFetch_LatestCommitDrivesAuthorIdle(t *testing.T) {
	t.Parallel()

	now := time.Now()
	commitAt := now.Add(-3 * time.Hour)
	reqAt := now.Add(-1 * time.Hour)

	spec := prSpec{
		num:       1,
		title:     "PR 1",
		repo:      "org/repo",
		author:    "alice",
		oid:       "oid1",
		mergeable: "MERGEABLE",
		reviewRequests: []testReviewRequest{
			{user: "kalverra", at: reqAt},
		},
		lastCommit: &testCommit{
			oid: "commit-oid",
			at:  commitAt,
		},
		omitCommitFromTimeline: true,
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)

	require.Len(t, queue.Inbox, 1)
	pr := queue.Inbox[0]
	require.NotEmpty(t, pr.Commits, "commits must be populated from commits(last: 1)")
	assert.True(
		t,
		pr.AuthorIdle(queue.Viewer, queue.Teams),
		"no commits pushed after review request; author must be idle",
	)
}

func TestFetch_ReviewRequestSurvivesHighCommitVolume(t *testing.T) {
	t.Parallel()

	now := time.Now()
	reqAt := now.Add(-2 * time.Hour)

	spec := prSpec{
		num:       1,
		title:     "High Commit PR",
		repo:      "org/repo",
		author:    "alice",
		oid:       "oid1",
		mergeable: "MERGEABLE",
		reviewRequests: []testReviewRequest{
			{user: "kalverra", at: reqAt},
		},
		lastCommit: &testCommit{
			oid: "latest-oid",
			at:  now.Add(-10 * time.Minute),
		},
		omitCommitFromTimeline: true,
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)

	require.Len(t, queue.Inbox, 1)
	pr := queue.Inbox[0]
	assert.InDelta(t, 2.0, pr.WaitHours(now, queue.Viewer, queue.Teams), 0.1, "review request must not be evicted")
	require.NotEmpty(t, pr.Commits)
	assert.Equal(t, "latest-oid", pr.Commits[0].OID)
}

func TestFetch_HydrationRunsConcurrently(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 10)
	prs := map[string]map[int]string{"org/repo": {}}
	for i := 1; i <= 10; i++ {
		specs = append(specs, prSpec{
			num: i, title: fmt.Sprintf("PR %d", i), repo: "org/repo", author: "alice", oid: fmt.Sprintf("oid%d", i),
		})
		prs["org/repo"][i] = prSpec{
			num: i, additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
			mergeable: "MERGEABLE", mergeState: "CLEAN",
		}.hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs:          prs,
		hydrateDelay: 50 * time.Millisecond,
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithHydrateBatchSize(2),
		source.WithHydrateConcurrency(4),
	)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 10)
	assert.Greater(t, fake.maxHydrateInFlight.Load(), int32(1), "batches should execute concurrently")
}

func TestFetch_HydrationRespectsConcurrencyLimit(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 10)
	prs := map[string]map[int]string{"org/repo": {}}
	for i := 1; i <= 10; i++ {
		specs = append(specs, prSpec{
			num: i, title: fmt.Sprintf("PR %d", i), repo: "org/repo", author: "alice", oid: fmt.Sprintf("oid%d", i),
		})
		prs["org/repo"][i] = prSpec{
			num: i, additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
			mergeable: "MERGEABLE", mergeState: "CLEAN",
		}.hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs:          prs,
		hydrateDelay: 30 * time.Millisecond,
	}

	client := newTestGraphQLClient(t, fake.handler())
	limit := 2
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithHydrateBatchSize(2),
		source.WithHydrateConcurrency(limit),
	)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 10)
	assert.Equal(t, int32(limit), fake.maxHydrateInFlight.Load(), "hydrate in flight must reach and not exceed limit")
}

func TestFetch_HydrationPreservesDiscoveryOrder(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 20)
	prs := map[string]map[int]string{"org/repo": {}}
	for i := 1; i <= 20; i++ {
		specs = append(specs, prSpec{
			num: i, title: fmt.Sprintf("PR %d", i), repo: "org/repo", author: "alice", oid: fmt.Sprintf("oid%d", i),
		})
		prs["org/repo"][i] = prSpec{
			num: i, additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
			mergeable: "MERGEABLE", mergeState: "CLEAN",
		}.hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: prs,
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithHydrateBatchSize(4),
		source.WithHydrateConcurrency(4),
	)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 20)
	for i, pr := range queue.Inbox {
		assert.Equal(t, i+1, pr.Number, "PR position must match discovery order")
	}
}

func TestFetch_HydrationBisectsOnPersistent502(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 10)
	prs := map[string]map[int]string{"org/repo": {}}
	for i := 1; i <= 10; i++ {
		specs = append(specs, prSpec{
			num: i, title: fmt.Sprintf("PR %d", i), repo: "org/repo", author: "alice", oid: fmt.Sprintf("oid%d", i),
		})
		prs["org/repo"][i] = prSpec{
			num: i, additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
			mergeable: "MERGEABLE", mergeState: "CLEAN",
		}.hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: prs,
	}
	fake.hydrateFailAboveAliases.Store(4)

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithHydrateBatchSize(10),
	)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 10, "all 10 PRs must be returned via bisection")
}

func TestFetch_HydrationReadsCacheOncePerPR(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 10)
	prs := map[string]map[int]string{"org/repo": {}}
	store := newFakeStore()
	for i := 1; i <= 10; i++ {
		oid := fmt.Sprintf("oid%d", i)
		specs = append(specs, prSpec{
			num: i, title: fmt.Sprintf("PR %d", i), repo: "org/repo", author: "alice", oid: oid,
		})
		prs["org/repo"][i] = prSpec{
			num: i, additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
			mergeable: "MERGEABLE", mergeState: "CLEAN",
		}.hydrateJSON()
		_ = store.SavePR(context.Background(), "org/repo", i, model.PullRequest{
			Number:            i,
			RepoNameWithOwner: "org/repo",
			HeadRefOID:        oid,
			Mergeable:         "UNKNOWN",
			MergeStateStatus:  "UNKNOWN",
		})
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: prs,
	}
	fake.hydrateFailAboveAliases.Store(4)

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
		source.WithHydrateBatchSize(10),
	)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 10, "all 10 PRs must be returned via bisection")
	assert.Equal(t, int32(10), store.prReads.Load(), "each PR should be read from cache exactly once during planning")
}

func TestFetch_HydrationBisectsToSingleton(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 4)
	prs := map[string]map[int]string{"org/repo": {}}
	for i := 1; i <= 4; i++ {
		specs = append(specs, prSpec{
			num: i, title: fmt.Sprintf("PR %d", i), repo: "org/repo", author: "alice", oid: fmt.Sprintf("oid%d", i),
		})
		prs["org/repo"][i] = prSpec{
			num: i, additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
			mergeable: "MERGEABLE", mergeState: "CLEAN",
		}.hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: prs,
		hydrateFailForPR: map[string]bool{
			"org/repo#3": true,
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithHydrateBatchSize(4),
	)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 3, "PR 3 should be dropped, remaining 3 PRs returned")
	numbers := make([]int, 0, 3)
	for _, pr := range queue.Inbox {
		numbers = append(numbers, pr.Number)
	}
	assert.Equal(t, []int{1, 2, 4}, numbers)
}

func TestFetch_BisectionDoesNotBurnRetriesOnLargeBatches(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 10)
	prs := map[string]map[int]string{"org/repo": {}}
	for i := 1; i <= 10; i++ {
		specs = append(specs, prSpec{
			num: i, title: fmt.Sprintf("PR %d", i), repo: "org/repo", author: "alice", oid: fmt.Sprintf("oid%d", i),
		})
		prs["org/repo"][i] = prSpec{
			num: i, additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
			mergeable: "MERGEABLE", mergeState: "CLEAN",
		}.hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: prs,
	}
	fake.hydrateFailAboveAliases.Store(4)

	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(
		client,
		source.WithRetryAttempts(3),
		source.WithRetryBaseDelay(500*time.Millisecond),
	)
	src := source.NewGraphQLSource(
		retrying,
		source.WithLogger(discardLogger()),
		source.WithHydrateBatchSize(10),
	)

	start := time.Now()
	queue, err := src.Fetch(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Len(t, queue.Inbox, 10)
	assert.Less(t, elapsed, 400*time.Millisecond, "bisection on multi-alias batches must not sleep for retries")
}

func TestFetch_SinglePRFailureIsDroppedNotFatal(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 3)
	prs := map[string]map[int]string{"org/repo": {}}
	for i := 1; i <= 3; i++ {
		specs = append(specs, prSpec{
			num: i, title: fmt.Sprintf("PR %d", i), repo: "org/repo", author: "alice", oid: fmt.Sprintf("oid%d", i),
		})
		prs["org/repo"][i] = prSpec{
			num: i, additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
			mergeable: "MERGEABLE", mergeState: "CLEAN",
		}.hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: prs,
		hydrateFailForPR: map[string]bool{
			"org/repo#2": true,
		},
	}

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 2)
	assert.Equal(t, 1, queue.Inbox[0].Number)
	assert.Equal(t, 3, queue.Inbox[1].Number)
}

func TestFetch_AllHydrationFailingIsFatal(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 0, 3)
	prs := map[string]map[int]string{"org/repo": {}}
	for i := 1; i <= 3; i++ {
		specs = append(specs, prSpec{
			num: i, title: fmt.Sprintf("PR %d", i), repo: "org/repo", author: "alice", oid: fmt.Sprintf("oid%d", i),
		})
		prs["org/repo"][i] = prSpec{
			num: i, additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
			mergeable: "MERGEABLE", mergeState: "CLEAN",
		}.hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: prs,
	}
	fake.hydrateFailures.Store(100)

	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.Error(t, err, "fetch must fail when all PRs fail hydration")
}

func TestFetch_PrunesCacheOncePerSource(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
		additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
		mergeable: "MERGEABLE", mergeState: "CLEAN",
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
	)

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int32(1), store.pruneCalls.Load(), "cache pruning must occur exactly once per source")
}

func TestFetch_SkipsHydrationForUnchangedSettledPR(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "Settled PR", repo: "org/repo", author: "alice", oid: "oid1",
		additions: 5, deletions: 1, changedFiles: 1, files: []string{"a.go"},
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{
			{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"},
		},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
	)

	q1, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, q1.Inbox, 1)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	// Second fetch: identical discovery and settled checks; must skip hydration completely.
	q2, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, q2.Inbox, 1)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "second fetch must issue zero hydrate requests")
	assert.Equal(t, "Settled PR", q2.Inbox[0].Title)
	assert.Equal(t, 5, q2.Inbox[0].Additions)
}

func TestFetch_RehydratesWhenUpdatedAtChanges(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	spec1 := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
		updatedAt: now,
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec1}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec1.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()), source.WithCache(store))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	// Update discovery updatedAt
	spec2 := spec1
	spec2.updatedAt = now.Add(1 * time.Hour)
	fake.searches["review-requested:@me"] = discoverPRs([]prSpec{spec2})

	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "updatedAt change must trigger rehydration")
}

func TestFetch_RehydratesWhenChecksStillRunning(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "IN_PROGRESS"}},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()), source.WithCache(store))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	// Second fetch: running checks must trigger rehydration
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "running checks must trigger rehydration")
}

func TestFetch_RehydratesWhenRequiredChecksHaveNotAppeared(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		requiredContexts: []string{"ci/build", "ci/lint"},
		checkContexts: []checkSpec{
			{name: "ci/build", status: "COMPLETED", conclusion: "SUCCESS"},
		},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()), source.WithCache(store))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	// Second fetch: required check not in rollup must trigger rehydration
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(
		t,
		int32(2),
		fake.hydrateCalls.Load(),
		"missing required check must trigger rehydration even when running==0",
	)
}

func TestFetch_RehydratesWhenMergeableUnknown(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
		mergeable: "UNKNOWN", mergeState: "UNKNOWN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()), source.WithCache(store))

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	// Second fetch: UNKNOWN mergeable must trigger rehydration
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "UNKNOWN mergeable must trigger rehydration")
}

func TestFetch_RehydratesAfterMaxReuseAge(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "PR 1", repo: "org/repo", author: "alice", oid: "oid1",
		updatedAt: time.Now(),
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
		source.WithMaxReuseAge(10*time.Minute),
	)

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	// Backdate savedAt in cache past max reuse age
	store.mu.Lock()
	store.prsAt["org/repo#1"] = time.Now().Add(-30 * time.Minute)
	store.mu.Unlock()

	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "expired max reuse age must trigger rehydration")
}

func TestFetch_ReusedPRAdoptsCurrentDiscoveryFlags(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	authoredSpec := prSpec{
		num: 1, title: "Old Title", repo: "org/repo", author: "kalverra", oid: "oid1",
		updatedAt: now,
		isDraft:   false,
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
		additions:     42,
	}
	inboxSpec := prSpec{
		num: 2, title: "Inbox PR", repo: "org/repo", author: "alice", oid: "oid2",
		updatedAt: now,
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
		additions:     10,
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"author:@me":           discoverPRs([]prSpec{authoredSpec}),
			"review-requested:@me": discoverPRs([]prSpec{inboxSpec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {
				1: authoredSpec.hydrateJSON(),
				2: inboxSpec.hydrateJSON(),
			},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()), source.WithCache(store))

	q1, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, q1.Authored, 1)
	require.Len(t, q1.Inbox, 1)
	assert.False(t, q1.Authored[0].IsDraft)
	assert.Equal(t, "Old Title", q1.Authored[0].Title)
	assert.False(t, q1.Inbox[0].Assigned)
	assert.Nil(t, q1.Authored[0].Stack)

	// Second fetch:
	// authoredSpec updates title, isDraft=true, and gains stack in discovery.
	// inboxSpec is found by assignee:@me in addition to review-requested.
	updatedAuthored := authoredSpec
	updatedAuthored.title = "New Discovery Title"
	updatedAuthored.isDraft = true
	updatedAuthored.stackID = "PRS_123"
	updatedAuthored.stackNum = 5
	updatedAuthored.stackSize = 3
	updatedAuthored.stackPos = 2
	updatedAuthored.stackBase = "main"

	fake.searches = map[string][]discoveryPage{
		"author:@me":           discoverPRs([]prSpec{updatedAuthored}),
		"review-requested:@me": discoverPRs([]prSpec{inboxSpec}),
		"assignee:@me":         discoverPRs([]prSpec{inboxSpec}),
	}

	q2, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, q2.Authored, 1)
	require.Len(t, q2.Inbox, 1)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "PRs must be reused without hydration")
	assert.Equal(t, "New Discovery Title", q2.Authored[0].Title, "Title must be overlaid from discovery")
	assert.True(t, q2.Authored[0].IsDraft, "IsDraft must be overlaid from discovery")
	assert.Equal(t, 42, q2.Authored[0].Additions, "hydrated fields like Additions must come from cache")
	require.NotNil(t, q2.Authored[0].Stack, "Stack must be overlaid from discovery")
	assert.Equal(t, "PRS_123", q2.Authored[0].Stack.ID)
	assert.Equal(t, 2, q2.Authored[0].Stack.Position)
	assert.Equal(t, 3, q2.Authored[0].Stack.Size)
	assert.True(t, q2.Inbox[0].Assigned, "Assigned must be overlaid from discovery")
	assert.Equal(t, 10, q2.Inbox[0].Additions)
}

func TestFetch_MaxReuseAgeIsStaggered(t *testing.T) {
	t.Parallel()

	const nPRs = 40
	now := time.Now()
	baseMaxAge := 10 * time.Minute

	specs := make([]prSpec, nPRs)
	prMap := make(map[int]string, nPRs)
	for i := range nPRs {
		specs[i] = prSpec{
			num: i + 1, title: fmt.Sprintf("PR %d", i+1), repo: "org/repo",
			author: "alice", oid: fmt.Sprintf("oid%d", i+1),
			updatedAt: now,
			mergeable: "MERGEABLE", mergeState: "CLEAN",
			checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
		}
		prMap[i+1] = specs[i].hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: map[string]map[int]string{
			"org/repo": prMap,
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
		source.WithMaxReuseAge(baseMaxAge),
	)

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	initialHydrates := fake.hydrateCalls.Load()
	require.Positive(t, initialHydrates)

	// Probe at 0.85x base max age
	probeAge := time.Duration(float64(baseMaxAge) * 0.85)
	store.mu.Lock()
	for k := range store.prsAt {
		store.prsAt[k] = time.Now().Add(-probeAge)
	}
	store.mu.Unlock()

	fake.hydrateCalls.Store(0)
	fake.mu.Lock()
	fake.hydrateQueries = nil
	fake.hydrateVars = nil
	fake.mu.Unlock()

	_, err = src.Fetch(context.Background())
	require.NoError(t, err)

	rehydratedCount := 0
	fake.mu.Lock()
	for _, vars := range fake.hydrateVars {
		if rawIDs, ok := vars["ids"].([]any); ok {
			rehydratedCount += len(rawIDs)
		}
	}
	fake.mu.Unlock()

	assert.Positive(t, rehydratedCount, "at 0.85x max age, some PRs with lower jittered TTL must expire and rehydrate")
	assert.Less(t, rehydratedCount, nPRs, "at 0.85x max age, some PRs with higher jittered TTL must still be reused")
}

// A secondary rate limit 403 is account-global, so bisecting a failed batch
// only multiplies requests made while limited (GitHub's docs warn that
// continuing to make requests while secondary-limited risks a ban). The fetch
// must abort immediately instead.
func TestFetch_SecondaryRateLimitDoesNotBisect(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 4)
	prMap := make(map[int]string, 4)
	for i := range specs {
		specs[i] = prSpec{
			num: i + 1, title: fmt.Sprintf("PR %d", i+1), repo: "org/repo",
			author: "alice", oid: fmt.Sprintf("oid%d", i+1),
			mergeable: "MERGEABLE", mergeState: "CLEAN",
			checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
		}
		prMap[i+1] = specs[i].hydrateJSON()
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: map[string]map[int]string{
			"org/repo": prMap,
		},
	}
	fake.hydrateFailures.Store(100)
	fake.hydrateFailCode.Store(http.StatusForbidden)
	fake.hydrateFailSecondary.Store(true)

	client := newTestGraphQLClient(t, fake.handler())
	retrying := source.NewRetryingGraphQLClient(client, source.WithRetryBaseDelay(time.Millisecond))
	src := source.NewGraphQLSource(retrying, source.WithLogger(discardLogger()))

	_, err := src.Fetch(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, source.ErrSecondaryRateLimit)
	// One batch of 4 aliases; without the guard the bisect cascade would issue
	// 1 + 2 + 4 = 7 requests. The guard must stop at the first.
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "secondary rate limit must abort, not bisect")
}

// A PR with no activity for over a month is cheap to keep serving from cache:
// reuse must ignore the max reuse age entirely (the PR cannot have changed),
// while touching the cache file so the retention prune does not force a rehydrate cycle.
func TestFetch_StalePRReusedBeyondMaxReuseAge(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "Stale PR", repo: "org/repo", author: "alice", oid: "oid1",
		updatedAt: time.Now().Add(-40 * 24 * time.Hour),
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
		source.WithMaxReuseAge(10*time.Minute),
	)

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	// Backdate savedAt past max reuse age; the stale activity cutoff must win.
	store.mu.Lock()
	store.prsAt["org/repo#1"] = time.Now().Add(-30 * time.Minute)
	store.mu.Unlock()

	q2, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, q2.Inbox, 1)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "stale PR must be reused beyond max reuse age")

	store.mu.Lock()
	resaves := 0
	for _, s := range store.saves {
		if s == "pr:org/repo#1" {
			resaves++
		}
	}
	store.mu.Unlock()
	assert.Equal(t, 1, resaves, "reused stale PR must not be re-saved")
	assert.Equal(t, int32(1), store.touchCalls.Load(), "reused stale PR must be touched so prune does not evict it")
}

// The stale activity cutoff is configurable: with a cutoff longer than the
// PR's inactivity, the max reuse age applies again.
func TestFetch_StaleActivityCutoffConfigurable(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "Quiet PR", repo: "org/repo", author: "alice", oid: "oid1",
		updatedAt: time.Now().Add(-40 * 24 * time.Hour),
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
		source.WithMaxReuseAge(10*time.Minute),
		source.WithStaleActivityAfter(365*24*time.Hour),
	)

	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	store.mu.Lock()
	store.prsAt["org/repo#1"] = time.Now().Add(-30 * time.Minute)
	store.mu.Unlock()

	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "beyond the configured cutoff the max reuse age must apply")
}

func TestFetch_ReusedPRIsTouchedNotResaved(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "Active PR", repo: "org/repo", author: "alice", oid: "oid1",
		updatedAt: time.Now().Add(-1 * time.Hour),
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
	)

	// First fetch: cold miss -> SavePR called.
	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())
	assert.Equal(t, int32(0), store.touchCalls.Load())

	store.mu.Lock()
	originalSavedAt := store.prsAt["org/repo#1"]
	saveCount := 0
	for _, s := range store.saves {
		if s == "pr:org/repo#1" {
			saveCount++
		}
	}
	store.mu.Unlock()
	require.Equal(t, 1, saveCount)

	// Second fetch: PR is reused -> TouchPR called, SavePR NOT called, prsAt NOT changed.
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load(), "should be reused without re-hydrating")
	assert.Equal(t, int32(1), store.touchCalls.Load(), "reused PR must be touched")

	store.mu.Lock()
	newSaveCount := 0
	for _, s := range store.saves {
		if s == "pr:org/repo#1" {
			newSaveCount++
		}
	}
	currentSavedAt := store.prsAt["org/repo#1"]
	store.mu.Unlock()

	assert.Equal(t, 1, newSaveCount, "SavePR must not be called on reuse")
	assert.Equal(t, originalSavedAt, currentSavedAt, "prsAt must not advance on reuse")
}

func TestFetch_RehydratesAfterMaxReuseAgeAcrossReusePolls(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "Active PR", repo: "org/repo", author: "alice", oid: "oid1",
		updatedAt: time.Now().Add(-1 * time.Hour), // not stale
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
		source.WithMaxReuseAge(10*time.Minute),
	)

	// Poll 1: cold fetch -> hydrates (call count = 1).
	_, err := src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	// Poll 2: second fetch within maxReuseAge -> reused (call count = 1).
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fake.hydrateCalls.Load())

	// Simulate passage of time past max reuse age from Poll 1's hydration time.
	// Since Poll 2 touched instead of re-saving, backdating savedAt simulates
	// maxReuseAge expiry across reuse polls.
	store.mu.Lock()
	store.prsAt["org/repo#1"] = time.Now().Add(-15 * time.Minute)
	store.mu.Unlock()

	// Poll 3: SavedAt is now older than maxReuseAge -> Poll 3 MUST rehydrate (call count = 2).
	_, err = src.Fetch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "must rehydrate once maxReuseAge expires across reuse polls")
}

// Identity resolution and search discovery must run concurrently in one errgroup,
// so identity resolution does not block discovery searches.
func TestFetch_IdentityAndDiscoveryRunConcurrently(t *testing.T) {
	t.Parallel()

	spec := prSpec{
		num: 1, title: "Solo PR", repo: "org/repo", author: "alice", oid: "oid1",
		additions: 1, deletions: 1, changedFiles: 1, files: []string{"a.go"},
		mergeable: "MERGEABLE", mergeState: "CLEAN",
	}
	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{spec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {1: spec.hydrateJSON()},
		},
	}

	inner := newTestGraphQLClient(t, fake.handler())

	identStarted := make(chan struct{}, 1)
	discStarted := make(chan struct{}, 1)

	client := &concurrentHookClient{
		inner:        inner,
		identStarted: identStarted,
		discStarted:  discStarted,
	}

	src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))
	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, queue.Inbox, 1)
	assert.Equal(t, "kalverra", queue.Viewer)
}

type concurrentHookClient struct {
	inner        source.GraphQLClient
	identStarted chan struct{}
	discStarted  chan struct{}
}

func (c *concurrentHookClient) DoWithContext(ctx context.Context, query string, vars map[string]any, resp any) error {
	if strings.Contains(query, "ViewerLogin") {
		select {
		case c.identStarted <- struct{}{}:
		default:
		}
		// Wait for discovery to start concurrently within 100ms.
		select {
		case <-c.discStarted:
		case <-time.After(100 * time.Millisecond):
			return errors.New("timed out waiting for concurrent discovery to start")
		case <-ctx.Done():
			return ctx.Err()
		}
	} else if strings.Contains(query, "search(") {
		select {
		case c.discStarted <- struct{}{}:
		default:
		}
	}
	return c.inner.DoWithContext(ctx, query, vars, resp)
}

// When a secondary rate limit interrupts hydration after some PRs were already
// hydrated, Fetch must not discard the successful PRs. It halts further
// network requests immediately (no bisecting), returns a degraded queue
// combining the hydrated PRs with discovery-level metadata for unhydrated PRs,
// and does not poison the cache with unhydrated PR data.
func TestFetch_SecondaryRateLimitPartialDegradation(t *testing.T) {
	t.Parallel()

	specs := make([]prSpec, 4)
	prMap := make(map[int]string, 4)
	for i := range specs {
		specs[i] = prSpec{
			num: i + 1, title: fmt.Sprintf("PR %d", i+1), repo: "org/repo",
			author: "alice", oid: fmt.Sprintf("oid%d", i+1),
			mergeable: "MERGEABLE", mergeState: "CLEAN",
			checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
		}
		prMap[i+1] = specs[i].hydrateJSON()
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs(specs),
		},
		prs: map[string]map[int]string{
			"org/repo": prMap,
		},
	}
	// Fail on second hydrate call with secondary rate limit.
	fake.hydrateFailSecondaryAfter.Store(1)

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
		source.WithHydrateBatchSize(2),
		source.WithHydrateConcurrency(1), // serial execution for deterministic call ordering
	)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err, "fetch should not fail when partial hydration succeeds")
	require.Len(t, queue.Inbox, 4, "queue must contain all discovered PRs")

	// First batch (PR 1 & 2) was fully hydrated
	assert.Equal(t, model.MergeStateClean, queue.Inbox[0].MergeStatus.State())
	assert.Equal(t, model.MergeStateClean, queue.Inbox[1].MergeStatus.State())
	assert.Positive(t, queue.Inbox[0].Checks.Total)

	// Second batch (PR 3 & 4) degraded to discovery metadata
	assert.Equal(t, 3, queue.Inbox[2].Number)
	assert.Equal(t, "PR 3", queue.Inbox[2].Title)
	assert.Equal(t, model.MergeStateUnknown, queue.Inbox[2].MergeStatus.State())
	assert.Equal(t, 0, queue.Inbox[2].Checks.Total)

	assert.Equal(t, 4, queue.Inbox[3].Number)
	assert.Equal(t, "PR 4", queue.Inbox[3].Title)
	assert.Equal(t, model.MergeStateUnknown, queue.Inbox[3].MergeStatus.State())

	// Cache must only contain PR 1 and PR 2, not the degraded PR 3 and PR 4
	assert.True(t, store.hasPR("org/repo", 1))
	assert.True(t, store.hasPR("org/repo", 2))
	assert.False(t, store.hasPR("org/repo", 3))
	assert.False(t, store.hasPR("org/repo", 4))

	// Exactly 2 hydrate calls: batch 1 succeeded, batch 2 failed with secondary limit,
	// halted immediately without bisecting.
	assert.Equal(t, int32(2), fake.hydrateCalls.Load(), "must stop immediately on secondary rate limit")
}

// When cached PRs exist and new PR hydration hits a secondary rate limit on the
// very first attempt, Fetch must return the cached PRs alongside discovery data
// for the unhydrated PRs without returning an error.
func TestFetch_SecondaryRateLimitWithCachedPRsDegrades(t *testing.T) {
	t.Parallel()

	cachedSpec := prSpec{
		num: 1, title: "Cached PR", repo: "org/repo", author: "alice", oid: "oid1",
		updatedAt: time.Now(),
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}
	newSpec := prSpec{
		num: 2, title: "New PR", repo: "org/repo", author: "bob", oid: "oid2",
		updatedAt: time.Now(),
		mergeable: "MERGEABLE", mergeState: "CLEAN",
		checkContexts: []checkSpec{{name: "ci", status: "COMPLETED", conclusion: "SUCCESS"}},
	}

	fake := &fakeGitHub{
		login: "kalverra",
		searches: map[string][]discoveryPage{
			"review-requested:@me": discoverPRs([]prSpec{cachedSpec}),
		},
		prs: map[string]map[int]string{
			"org/repo": {
				1: cachedSpec.hydrateJSON(),
				2: newSpec.hydrateJSON(),
			},
		},
	}

	store := newFakeStore()
	client := newTestGraphQLClient(t, fake.handler())
	src := source.NewGraphQLSource(
		client,
		source.WithLogger(discardLogger()),
		source.WithCache(store),
		source.WithMaxReuseAge(10*time.Minute),
	)

	// First fetch populates cache for PR 1.
	q1, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, q1.Inbox, 1)
	require.True(t, store.hasPR("org/repo", 1))

	// Now introduce PR 2 and inject secondary rate limit on all subsequent hydrate calls.
	fake.mu.Lock()
	fake.searches["review-requested:@me"] = discoverPRs([]prSpec{cachedSpec, newSpec})
	fake.mu.Unlock()
	fake.hydrateFailures.Store(100)
	fake.hydrateFailCode.Store(http.StatusForbidden)
	fake.hydrateFailSecondary.Store(true)

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err, "fetch should return partial queue when cached PRs exist")
	require.Len(t, queue.Inbox, 2)

	// PR 1 reused from cache
	assert.Equal(t, 1, queue.Inbox[0].Number)
	assert.Equal(t, model.MergeStateClean, queue.Inbox[0].MergeStatus.State())

	// PR 2 degraded to discovery data
	assert.Equal(t, 2, queue.Inbox[1].Number)
	assert.Equal(t, "New PR", queue.Inbox[1].Title)
	assert.Equal(t, model.MergeStateUnknown, queue.Inbox[1].MergeStatus.State())

	// PR 2 must not be cached
	assert.False(t, store.hasPR("org/repo", 2))
}

func TestFetch_PopulatesCheckTimestamps(t *testing.T) {
	t.Parallel()

	t.Run("check runs with startedAt and completedAt", func(t *testing.T) {
		t.Parallel()

		start1 := time.Date(2026, 9, 10, 9, 30, 0, 0, time.UTC)
		end1 := time.Date(2026, 9, 10, 9, 45, 0, 0, time.UTC)
		start2 := time.Date(2026, 9, 10, 9, 35, 0, 0, time.UTC)
		end2 := time.Date(2026, 9, 10, 9, 50, 0, 0, time.UTC)

		spec := prSpec{
			num: 1, title: "PR with CheckRun Timestamps", repo: "org/repo", author: "alice", oid: "oid1",
			mergeable: "MERGEABLE", mergeState: "CLEAN",
			checkContexts: []checkSpec{
				{
					name:        "lint",
					status:      "COMPLETED",
					conclusion:  "SUCCESS",
					startedAt:   &start1,
					completedAt: &end1,
				},
				{
					name:        "test",
					status:      "COMPLETED",
					conclusion:  "SUCCESS",
					startedAt:   &start2,
					completedAt: &end2,
				},
			},
		}
		fake := &fakeGitHub{
			login: "kalverra",
			searches: map[string][]discoveryPage{
				"review-requested:@me": discoverPRs([]prSpec{spec}),
			},
			prs: map[string]map[int]string{
				"org/repo": {1: spec.hydrateJSON()},
			},
		}

		client := newTestGraphQLClient(t, fake.handler())
		src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

		queue, err := src.Fetch(context.Background())
		require.NoError(t, err)
		require.Len(t, queue.Inbox, 1)

		pr := queue.Inbox[0]
		require.NotNil(t, pr.Checks.StartedAt)
		require.NotNil(t, pr.Checks.CompletedAt)
		assert.Equal(t, start1, *pr.Checks.StartedAt)
		assert.Equal(t, end2, *pr.Checks.CompletedAt)

		dur, ok := pr.Checks.Duration(time.Time{})
		assert.True(t, ok)
		assert.Equal(t, 20*time.Minute, dur)
	})

	t.Run("status context with createdAt falls back to startedAt", func(t *testing.T) {
		t.Parallel()

		created := time.Date(2026, 9, 10, 8, 15, 0, 0, time.UTC)

		spec := prSpec{
			num: 2, title: "PR with StatusContext CreatedAt", repo: "org/repo", author: "bob", oid: "oid2",
			mergeable: "MERGEABLE", mergeState: "CLEAN",
			checkContexts: []checkSpec{
				{
					context:   "continuous-integration/jenkins",
					state:     "SUCCESS",
					createdAt: &created,
				},
			},
		}
		fake := &fakeGitHub{
			login: "kalverra",
			searches: map[string][]discoveryPage{
				"review-requested:@me": discoverPRs([]prSpec{spec}),
			},
			prs: map[string]map[int]string{
				"org/repo": {2: spec.hydrateJSON()},
			},
		}

		client := newTestGraphQLClient(t, fake.handler())
		src := source.NewGraphQLSource(client, source.WithLogger(discardLogger()))

		queue, err := src.Fetch(context.Background())
		require.NoError(t, err)
		require.Len(t, queue.Inbox, 1)

		pr := queue.Inbox[0]
		require.NotNil(t, pr.Checks.StartedAt)
		assert.Equal(t, created, *pr.Checks.StartedAt)
		assert.Nil(t, pr.Checks.CompletedAt)
	})
}
