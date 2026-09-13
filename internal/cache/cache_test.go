package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/model"
)

func TestDiskStore_IdentityRoundtrip(t *testing.T) {
	t.Parallel()

	store := cache.NewDiskStore(t.TempDir())
	ctx := context.Background()

	_, _, ok := store.Identity(ctx)
	require.False(t, ok, "empty store must miss")

	id := cache.Identity{
		Login: "kalverra",
		Teams: []string{"smartcontractlink/engineers", "myorg/platform"},
	}
	require.NoError(t, store.SaveIdentity(ctx, id))

	got, savedAt, ok := store.Identity(ctx)
	require.True(t, ok)
	assert.Equal(t, id, got)
	assert.WithinDuration(t, time.Now(), savedAt, time.Minute)
}

func TestDiskStore_IdentityCorruptFileMisses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)
	ctx := context.Background()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "identity.json"), []byte("{not json"), 0o600))

	_, _, ok := store.Identity(ctx)
	assert.False(t, ok, "corrupt file must miss, not error")
}

func fullyPopulatedPR() model.PullRequest {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	return model.PullRequest{
		Number:            42,
		Title:             "Feature PR",
		URL:               "https://github.com/org/repo/pull/42",
		IsDraft:           true,
		CreatedAt:         now.Add(-24 * time.Hour),
		UpdatedAt:         now.Add(-1 * time.Hour),
		HeadRefOID:        "head12345",
		Additions:         120,
		Deletions:         30,
		ChangedFiles:      4,
		FilesTruncated:    false,
		Files:             []string{"a.go", "b.go", "c.go", "d.go"},
		Author:            "alice",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		ReviewDecision:    "APPROVED",
		Assigned:          true,
		Starred:           true,
		MergeStatus: model.MergeStatus{
			Mergeable:        "MERGEABLE",
			MergeStateStatus: "CLEAN",
			IsDraft:          true,
		},
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          2,
			ReqRunning:        0,
			ReqDone:           2,
			ReqFailed:         0,
			Total:             5,
			Running:           0,
			Done:              5,
			Failed:            0,
		},
		LatestReviews: []model.Review{
			{
				Author:      "bob",
				State:       "APPROVED",
				CommitOID:   "head12345",
				SubmittedAt: now.Add(-2 * time.Hour),
			},
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-3 * time.Hour),
				ReviewerUser: "kalverra",
				ReviewerTeam: "org/team",
			},
		},
		Commits: []model.Commit{
			{
				OID:           "head12345",
				CommittedDate: now.Add(-4 * time.Hour),
			},
		},
	}
}

func TestPullRequest_AllExportedFieldsHaveJSONTags(t *testing.T) {
	t.Parallel()

	types := []reflect.Type{
		reflect.TypeFor[model.PullRequest](),
		reflect.TypeFor[model.Review](),
		reflect.TypeFor[model.TimelineItem](),
		reflect.TypeFor[model.Commit](),
		reflect.TypeFor[model.ChecksSummary](),
		reflect.TypeFor[model.ContextCheck](),
		reflect.TypeFor[model.MergeStatus](),
	}

	for _, typ := range types {
		for field := range typ.Fields() {
			if !field.IsExported() {
				continue
			}
			tag := field.Tag.Get("json")
			assert.NotEmptyf(t, tag, "field %s.%s must have json tag", typ.Name(), field.Name)
		}
	}
}

func TestDiskStore_PR_RoundtripPreservesEveryField(t *testing.T) {
	t.Parallel()

	store := cache.NewDiskStore(t.TempDir())
	ctx := context.Background()

	pr := fullyPopulatedPR()
	require.NoError(t, store.SavePR(ctx, pr.RepoNameWithOwner, pr.Number, pr))

	got, savedAt, ok := store.PR(ctx, pr.RepoNameWithOwner, pr.Number)
	require.True(t, ok)
	assert.WithinDuration(t, time.Now(), savedAt, time.Minute)
	assert.Equal(t, pr, got)
}

func TestDiskStore_PROverwritesPreviousHeadOID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)
	ctx := context.Background()

	pr1 := fullyPopulatedPR()
	pr1.HeadRefOID = "oid1"
	require.NoError(t, store.SavePR(ctx, pr1.RepoNameWithOwner, pr1.Number, pr1))

	pr2 := pr1
	pr2.HeadRefOID = "oid2"
	require.NoError(t, store.SavePR(ctx, pr2.RepoNameWithOwner, pr2.Number, pr2))

	matches, err := filepath.Glob(filepath.Join(dir, "prs", "*.json"))
	require.NoError(t, err)
	assert.Len(t, matches, 1, "two saves with different OIDs must overwrite and leave exactly one file")

	got, _, ok := store.PR(ctx, pr2.RepoNameWithOwner, pr2.Number)
	require.True(t, ok)
	assert.Equal(t, "oid2", got.HeadRefOID)
}

func TestDiskStore_UpdatedAtSurvivesRoundtrip(t *testing.T) {
	t.Parallel()

	store := cache.NewDiskStore(t.TempDir())
	ctx := context.Background()

	pr := fullyPopulatedPR()
	require.NoError(t, store.SavePR(ctx, pr.RepoNameWithOwner, pr.Number, pr))

	got, _, ok := store.PR(ctx, pr.RepoNameWithOwner, pr.Number)
	require.True(t, ok)
	assert.True(t, pr.UpdatedAt.Equal(got.UpdatedAt), "UpdatedAt must survive roundtrip with .Equal()")
}

func TestDiskStore_SchemaMismatchMisses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)
	ctx := context.Background()

	// 1. Identity schema mismatch
	idFile := filepath.Join(dir, "identity.json")
	require.NoError(
		t,
		os.WriteFile(
			idFile,
			[]byte(`{"schema": 999, "saved_at": "2026-09-10T10:00:00Z", "data": {"login": "kalverra"}}`),
			0o600,
		),
	)
	_, _, ok := store.Identity(ctx)
	assert.False(t, ok, "schema mismatch must miss on Identity")

	// 2. Queue schema mismatch
	queueFile := filepath.Join(dir, "queue.json")
	require.NoError(
		t,
		os.WriteFile(queueFile, []byte(`{"schema": 999, "saved_at": "2026-09-10T10:00:00Z", "data": {}}`), 0o600),
	)
	_, _, ok = store.Queue(ctx)
	assert.False(t, ok, "schema mismatch must miss on Queue")

	// 3. PR schema mismatch
	prDir := filepath.Join(dir, "prs")
	require.NoError(t, os.MkdirAll(prDir, 0o700))
	prFile := filepath.Join(prDir, "org_repo_42.json")
	require.NoError(
		t,
		os.WriteFile(
			prFile,
			[]byte(`{"schema": 999, "key": "org/repo#42", "saved_at": "2026-09-10T10:00:00Z", "data": {}}`),
			0o600,
		),
	)
	_, _, ok = store.PR(ctx, "org/repo", 42)
	assert.False(t, ok, "schema mismatch must miss on PR")
}

func TestDiskStore_ColludingRepoNamesDoNotCollide(t *testing.T) {
	t.Parallel()

	store := cache.NewDiskStore(t.TempDir())
	ctx := context.Background()

	pr := fullyPopulatedPR()
	pr.RepoNameWithOwner = "a/b_c"
	pr.Number = 1
	require.NoError(t, store.SavePR(ctx, "a/b_c", 1, pr))

	// Reading colluding repo name must miss
	_, _, ok := store.PR(ctx, "a_b/c", 1)
	assert.False(t, ok, "colluding repo name must miss due to canonical key validation")
}

func TestDiskStore_TouchPRMissingEntryIsNoop(t *testing.T) {
	t.Parallel()

	store := cache.NewDiskStore(t.TempDir())
	err := store.TouchPR(context.Background(), "org/repo", 999)
	assert.NoError(t, err, "TouchPR on nonexistent file must swallow os.ErrNotExist")
}

func TestDiskStore_PrunePRsUsesFileModTimeNotSavedAt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)
	ctx := context.Background()

	pr := fullyPopulatedPR()
	pr.Number = 1
	require.NoError(t, store.SavePR(ctx, pr.RepoNameWithOwner, 1, pr))

	// Backdate file mtime to 20 days ago, while SavedAt in JSON remains fresh.
	prPath := filepath.Join(dir, "prs", "org_repo_1.json")
	oldTime := time.Now().Add(-20 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(prPath, oldTime, oldTime))

	removed, err := store.PrunePRs(ctx, 14*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, removed, "file with stale ModTime must be pruned even if SavedAt in JSON is fresh")

	_, _, ok := store.PR(ctx, pr.RepoNameWithOwner, 1)
	assert.False(t, ok, "pruned PR must miss")
}

func TestDiskStore_TouchPRKeepsEntryFromPruneWithoutAdvancingSavedAt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)
	ctx := context.Background()

	pr := fullyPopulatedPR()
	pr.Number = 1
	require.NoError(t, store.SavePR(ctx, pr.RepoNameWithOwner, 1, pr))

	_, originalSavedAt, ok := store.PR(ctx, pr.RepoNameWithOwner, 1)
	require.True(t, ok)

	// Backdate file mtime to 20 days ago (older than 14-day retention).
	prPath := filepath.Join(dir, "prs", "org_repo_1.json")
	oldTime := time.Now().Add(-20 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(prPath, oldTime, oldTime))

	// Touch the PR to mark it as recently seen.
	require.NoError(t, store.TouchPR(ctx, pr.RepoNameWithOwner, 1))

	// Prune with 14-day threshold. Touched PR should survive.
	removed, err := store.PrunePRs(ctx, 14*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, removed, "recently touched PR must not be pruned")

	// Verify SavedAt was NOT advanced by TouchPR.
	_, savedAtAfterTouch, ok := store.PR(ctx, pr.RepoNameWithOwner, 1)
	require.True(t, ok)
	assert.Equal(t, originalSavedAt, savedAtAfterTouch, "TouchPR must not advance SavedAt")
}

func TestDiskStore_PrunePRsRemovesStaleEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)
	ctx := context.Background()

	pr1 := fullyPopulatedPR()
	pr1.Number = 1
	require.NoError(t, store.SavePR(ctx, pr1.RepoNameWithOwner, 1, pr1))

	pr2 := fullyPopulatedPR()
	pr2.Number = 2
	require.NoError(t, store.SavePR(ctx, pr2.RepoNameWithOwner, 2, pr2))

	// Make PR 1 stale by backdating its file modification time.
	staleTime := time.Now().Add(-30 * 24 * time.Hour)
	pr1Path := filepath.Join(dir, "prs", "org_repo_1.json")
	require.NoError(t, os.Chtimes(pr1Path, staleTime, staleTime))

	removed, err := store.PrunePRs(ctx, 14*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, removed, "one stale PR file must be removed")

	_, _, ok1 := store.PR(ctx, pr1.RepoNameWithOwner, 1)
	assert.False(t, ok1, "stale PR must be gone")

	_, _, ok2 := store.PR(ctx, pr2.RepoNameWithOwner, 2)
	assert.True(t, ok2, "fresh PR must remain")
}

func TestDiskStore_PruneLeavesNonPREntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)
	ctx := context.Background()

	require.NoError(t, store.SaveIdentity(ctx, cache.Identity{Login: "kalverra"}))
	require.NoError(t, store.SaveQueue(ctx, model.Queue{}))

	removed, err := store.PrunePRs(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, removed)

	_, _, ok := store.Identity(ctx)
	assert.True(t, ok, "Identity must not be pruned")

	_, _, ok = store.Queue(ctx)
	assert.True(t, ok, "Queue must not be pruned")
}

func TestDiskStore_PruneSweepsAbandonedTempFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)
	ctx := context.Background()

	prsDir := filepath.Join(dir, "prs")
	require.NoError(t, os.MkdirAll(prsDir, 0o700))
	tmpFile := filepath.Join(prsDir, ".tmp-abandoned-12345")
	require.NoError(t, os.WriteFile(tmpFile, []byte("debris"), 0o600))
	oldTime := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(tmpFile, oldTime, oldTime))

	freshTmpFile := filepath.Join(prsDir, ".tmp-active-67890")
	require.NoError(t, os.WriteFile(freshTmpFile, []byte("in-flight"), 0o600))

	removed, err := store.PrunePRs(ctx, 14*24*time.Hour)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, removed, 0)

	_, err = os.Stat(tmpFile)
	assert.True(t, os.IsNotExist(err), "abandoned temp file must be removed")

	_, err = os.Stat(freshTmpFile)
	assert.NoError(t, err, "fresh temp file must be preserved")
}

func TestDiskStore_PruneEmptyStoreIsNoop(t *testing.T) {
	t.Parallel()

	store := cache.NewDiskStore(t.TempDir())
	removed, err := store.PrunePRs(context.Background(), 14*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 0, removed)
}

func TestDiskStore_QueueRoundtrip(t *testing.T) {
	t.Parallel()

	store := cache.NewDiskStore(t.TempDir())
	ctx := context.Background()

	_, _, ok := store.Queue(ctx)
	require.False(t, ok, "empty store must miss")

	q := model.Queue{
		Authored: []model.PullRequest{{Number: 1, Title: "mine"}},
		Inbox:    []model.PullRequest{{Number: 2, Title: "theirs"}},
	}
	require.NoError(t, store.SaveQueue(ctx, q))

	got, savedAt, ok := store.Queue(ctx)
	require.True(t, ok)
	require.Len(t, got.Authored, 1)
	require.Len(t, got.Inbox, 1)
	assert.Equal(t, "mine", got.Authored[0].Title)
	assert.Equal(t, "theirs", got.Inbox[0].Title)
	assert.WithinDuration(t, time.Now(), savedAt, time.Minute)
}

func TestDiskStore_QueueCorruptFileMisses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := cache.NewDiskStore(dir)
	ctx := context.Background()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "queue.json"), []byte("nope"), 0o600))

	_, _, ok := store.Queue(ctx)
	assert.False(t, ok, "corrupt queue file must miss, not error")
}

func TestOpen_RespectsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CACHE_DIR", dir)

	store, err := cache.Open()
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.SaveIdentity(ctx, cache.Identity{Login: "kalverra"}))

	_, err = os.Stat(filepath.Join(dir, "identity.json"))
	require.NoError(t, err, "cache files must land in PRONTO_CACHE_DIR")
}

func TestOpen_DefaultsToUserCacheDir(t *testing.T) {
	t.Setenv("PRONTO_CACHE_DIR", "")

	store, err := cache.Open()
	require.NoError(t, err)
	require.NotNil(t, store)

	base, err := os.UserCacheDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "pronto"), store.Dir())
}
