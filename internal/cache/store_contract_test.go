package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/cache/cachetest"
	"github.com/kalverra/pronto/internal/model"
)

// storeHarness builds a fresh Store and backdates a saved PR's retention clock
// (the thing TouchPR refreshes and PrunePRs reads) by age.
type storeHarness struct {
	newStore func(t *testing.T) cache.Store
	backdate func(t *testing.T, s cache.Store, repo string, num int, age time.Duration)
}

// TestStoreContract runs the cache.Store contract against DiskStore and the
// cachetest fake so the fake stays faithful.
func TestStoreContract(t *testing.T) {
	t.Parallel()

	harnesses := map[string]storeHarness{
		"disk": {
			newStore: func(t *testing.T) cache.Store {
				t.Helper()
				return cache.NewDiskStore(t.TempDir())
			},
			backdate: func(t *testing.T, s cache.Store, repo string, num int, age time.Duration) {
				t.Helper()
				d, ok := s.(*cache.DiskStore)
				require.True(t, ok)
				matches, err := filepath.Glob(filepath.Join(d.Dir(), "prs", "*.json"))
				require.NoError(t, err)
				require.Len(t, matches, 1, "backdate expects exactly one PR file for %s#%d", repo, num)
				old := time.Now().Add(-age)
				require.NoError(t, os.Chtimes(matches[0], old, old))
			},
		},
		"cachetest": {
			newStore: func(*testing.T) cache.Store { return cachetest.New() },
			backdate: func(t *testing.T, s cache.Store, repo string, num int, age time.Duration) {
				t.Helper()
				fake, ok := s.(*cachetest.Store)
				require.True(t, ok)
				pr, _, ok := fake.PR(context.Background(), repo, num)
				require.True(t, ok)
				old := time.Now().Add(-age)
				fake.SetClock(func() time.Time { return old })
				require.NoError(t, fake.SavePR(context.Background(), repo, num, pr))
				fake.SetClock(time.Now)
			},
		},
	}

	for name, h := range harnesses {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runStoreContract(t, h)
		})
	}
}

func runStoreContract(t *testing.T, h storeHarness) {
	t.Helper()
	ctx := context.Background()
	const repo = "org/repo"
	pr := model.PullRequest{Number: 7, Title: "seven", RepoNameWithOwner: repo}

	t.Run("empty store misses with zero values", func(t *testing.T) {
		t.Parallel()
		s := h.newStore(t)
		id, at, ok := s.Identity(ctx)
		assert.False(t, ok)
		assert.Zero(t, id)
		assert.Zero(t, at)
		gotPR, _, ok := s.PR(ctx, repo, 7)
		assert.False(t, ok)
		assert.Zero(t, gotPR)
		q, _, ok := s.Queue(ctx)
		assert.False(t, ok)
		assert.Zero(t, q)
		keys, _, ok := s.Focus(ctx)
		assert.False(t, ok)
		assert.Nil(t, keys)
	})

	t.Run("round-trips every document", func(t *testing.T) {
		t.Parallel()
		s := h.newStore(t)
		id := cache.Identity{Login: "alice", Teams: []string{"org/team"}}
		q := model.Queue{Viewer: "alice"}
		keys := []model.PRKey{{Repo: repo, Number: 7}}
		require.NoError(t, s.SaveIdentity(ctx, id))
		require.NoError(t, s.SavePR(ctx, repo, 7, pr))
		require.NoError(t, s.SaveQueue(ctx, q))
		require.NoError(t, s.SaveFocus(ctx, keys))

		gotID, at, ok := s.Identity(ctx)
		require.True(t, ok)
		assert.Equal(t, id, gotID)
		assert.WithinDuration(t, time.Now(), at, time.Minute)
		gotPR, _, ok := s.PR(ctx, repo, 7)
		require.True(t, ok)
		assert.Equal(t, pr.Title, gotPR.Title)
		gotQ, _, ok := s.Queue(ctx)
		require.True(t, ok)
		assert.Equal(t, "alice", gotQ.Viewer)
		gotKeys, _, ok := s.Focus(ctx)
		require.True(t, ok)
		assert.Equal(t, keys, gotKeys)
	})

	t.Run("identity without login misses", func(t *testing.T) {
		t.Parallel()
		s := h.newStore(t)
		require.NoError(t, s.SaveIdentity(ctx, cache.Identity{Teams: []string{"org/team"}}))
		id, _, ok := s.Identity(ctx)
		assert.False(t, ok)
		assert.Zero(t, id)
	})

	t.Run("done context misses on read and fails on write", func(t *testing.T) {
		t.Parallel()
		s := h.newStore(t)
		require.NoError(t, s.SavePR(ctx, repo, 7, pr))
		done, cancel := context.WithCancel(ctx)
		cancel()
		gotPR, _, ok := s.PR(done, repo, 7)
		assert.False(t, ok)
		assert.Zero(t, gotPR)
		require.ErrorIs(t, s.SaveQueue(done, model.Queue{}), context.Canceled)
		require.ErrorIs(t, s.TouchPR(done, repo, 7), context.Canceled)
		_, err := s.PrunePRs(done, time.Hour)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("touch missing entry is a no-op", func(t *testing.T) {
		t.Parallel()
		s := h.newStore(t)
		require.NoError(t, s.TouchPR(ctx, repo, 404))
		_, _, ok := s.PR(ctx, repo, 404)
		assert.False(t, ok)
	})

	t.Run("prune removes entries unused for longer than ttl", func(t *testing.T) {
		t.Parallel()
		s := h.newStore(t)
		require.NoError(t, s.SavePR(ctx, repo, 7, pr))
		removed, err := s.PrunePRs(ctx, time.Hour)
		require.NoError(t, err)
		assert.Zero(t, removed, "fresh entry kept")

		h.backdate(t, s, repo, 7, 2*time.Hour)
		removed, err = s.PrunePRs(ctx, time.Hour)
		require.NoError(t, err)
		assert.Equal(t, 1, removed)
		_, _, ok := s.PR(ctx, repo, 7)
		assert.False(t, ok)
	})

	t.Run("prune leaves non-PR documents", func(t *testing.T) {
		t.Parallel()
		s := h.newStore(t)
		require.NoError(t, s.SaveQueue(ctx, model.Queue{Viewer: "alice"}))
		require.NoError(t, s.SaveFocus(ctx, nil))
		_, err := s.PrunePRs(ctx, -time.Hour)
		require.NoError(t, err)
		_, _, ok := s.Queue(ctx)
		assert.True(t, ok)
		_, _, ok = s.Focus(ctx)
		assert.True(t, ok)
	})

	t.Run("touch extends retention without advancing savedAt", func(t *testing.T) {
		t.Parallel()
		s := h.newStore(t)
		require.NoError(t, s.SavePR(ctx, repo, 7, pr))
		h.backdate(t, s, repo, 7, 2*time.Hour)
		_, savedAt, ok := s.PR(ctx, repo, 7)
		require.True(t, ok)

		require.NoError(t, s.TouchPR(ctx, repo, 7))
		removed, err := s.PrunePRs(ctx, time.Hour)
		require.NoError(t, err)
		assert.Zero(t, removed, "touched entry kept")

		_, after, ok := s.PR(ctx, repo, 7)
		require.True(t, ok)
		assert.True(t, savedAt.Equal(after), "touch must not advance savedAt")
	})
}
