package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
)

func TestQueue_Find(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:            497,
				RepoNameWithOwner: "smartcontractkit/quick-agent",
				Title:             "merge queue monitor",
			},
			{
				Number:            100,
				RepoNameWithOwner: "smartcontractkit/repo-a",
				Title:             "duplicate number in repo A",
			},
		},
		Inbox: []model.PullRequest{
			{
				Number:            23695,
				RepoNameWithOwner: "smartcontractkit/chainlink",
				Title:             "bridge cache fallback",
			},
			{
				Number:            100,
				RepoNameWithOwner: "smartcontractkit/repo-b",
				Title:             "duplicate number in repo B",
			},
		},
	}

	t.Run("find by exact repo and number", func(t *testing.T) {
		t.Parallel()

		pr, err := q.Find("smartcontractkit/quick-agent#497")
		require.NoError(t, err)
		assert.Equal(t, 497, pr.Number)
		assert.Equal(t, "smartcontractkit/quick-agent", pr.RepoNameWithOwner)
	})

	t.Run("find by bare unique number with hash", func(t *testing.T) {
		t.Parallel()

		pr, err := q.Find("#23695")
		require.NoError(t, err)
		assert.Equal(t, 23695, pr.Number)
	})

	t.Run("find by bare unique number without hash", func(t *testing.T) {
		t.Parallel()

		pr, err := q.Find("23695")
		require.NoError(t, err)
		assert.Equal(t, 23695, pr.Number)
	})

	t.Run("ambiguous number returns ErrAmbiguousRef listing candidates", func(t *testing.T) {
		t.Parallel()

		_, err := q.Find("#100")
		require.ErrorIs(t, err, model.ErrAmbiguousRef)
		assert.Contains(t, err.Error(), "smartcontractkit/repo-a#100")
		assert.Contains(t, err.Error(), "smartcontractkit/repo-b#100")
	})

	t.Run("not found returns ErrNotFound", func(t *testing.T) {
		t.Parallel()

		_, err := q.Find("#99999")
		require.ErrorIs(t, err, model.ErrNotFound)
	})
}

func TestMergeQueue(t *testing.T) {
	t.Parallel()

	authored := []model.PullRequest{
		{
			Number:            1,
			RepoNameWithOwner: "org/repo",
			Title:             "authored ready",
			IsDraft:           false,
		},
		{
			Number:            2,
			RepoNameWithOwner: "org/repo",
			Title:             "authored draft",
			IsDraft:           true,
		},
		{
			Number:            3,
			RepoNameWithOwner: "org/repo",
			Title:             "authored ready 2",
			IsDraft:           false,
		},
	}

	inbox := []model.PullRequest{
		{
			// Overlaps with authored #1
			Number:            1,
			RepoNameWithOwner: "org/repo",
			Title:             "authored ready overlapping inbox",
			Assigned:          true,
		},
		{
			Number:            10,
			RepoNameWithOwner: "org/repo",
			Title:             "inbox draft (must be excluded)",
			IsDraft:           true,
		},
		{
			Number:            11,
			RepoNameWithOwner: "org/repo",
			Title:             "assigned only",
			Assigned:          true,
		},
		{
			Number:            12,
			RepoNameWithOwner: "org/repo",
			Title:             "assigned duplicate",
			Assigned:          true,
		},
	}

	merged := model.MergeQueue(authored, inbox)

	t.Run("dedupes overlapping authored and inbox with authored priority", func(t *testing.T) {
		t.Parallel()

		// #1 should appear in Authored, not in Inbox
		assert.Len(t, merged.Authored, 3)
		assert.Equal(t, 1, merged.Authored[0].Number)

		for _, p := range merged.Inbox {
			assert.NotEqual(t, 1, p.Number)
		}
	})

	t.Run("pins authored drafts to bottom of authored list", func(t *testing.T) {
		t.Parallel()

		// Authored has [ready, ready 2, draft]
		require.Len(t, merged.Authored, 3)
		assert.False(t, merged.Authored[0].IsDraft)
		assert.False(t, merged.Authored[1].IsDraft)
		assert.True(t, merged.Authored[2].IsDraft)
		assert.Equal(t, 2, merged.Authored[2].Number)
	})

	t.Run("inbox hard filters drafts", func(t *testing.T) {
		t.Parallel()

		// Inbox draft #10 must not be in Inbox
		for _, p := range merged.Inbox {
			assert.False(t, p.IsDraft)
			assert.NotEqual(t, 10, p.Number)
		}
	})

	t.Run("preserves assigned flag in inbox", func(t *testing.T) {
		t.Parallel()

		var found11, found12 bool
		for _, p := range merged.Inbox {
			if p.Number == 11 {
				found11 = true
				assert.True(t, p.Assigned)
			}
			if p.Number == 12 {
				found12 = true
				assert.True(t, p.Assigned)
			}
		}
		assert.True(t, found11)
		assert.True(t, found12)
	})
}

func TestQueue_IsAuthored(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Viewer: "kalverra",
		Authored: []model.PullRequest{
			{Number: 10, RepoNameWithOwner: "org/repo", Author: "other"},
		},
		Inbox: []model.PullRequest{
			{Number: 20, RepoNameWithOwner: "org/repo", Author: "kalverra"},
			{Number: 30, RepoNameWithOwner: "org/repo", Author: "someone_else"},
		},
	}

	assert.True(t, q.IsAuthored(model.PullRequest{Number: 10, RepoNameWithOwner: "org/repo"}))
	assert.True(t, q.IsAuthored(model.PullRequest{Number: 20, RepoNameWithOwner: "org/repo", Author: "kalverra"}))
	assert.False(t, q.IsAuthored(model.PullRequest{Number: 30, RepoNameWithOwner: "org/repo", Author: "someone_else"}))
	assert.False(t, q.IsAuthored(model.PullRequest{Number: 99, RepoNameWithOwner: "org/repo"}))
}
