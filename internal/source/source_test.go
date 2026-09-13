package source_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/source"
)

func TestSourceInterface_FixtureReplay(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "real_account_prs.json")
	var src source.Source = source.NewFixtureSource(fixturePath)

	ctx := context.Background()
	queue, err := src.Fetch(ctx)
	require.NoError(t, err)

	t.Run("authored PR parsed from real fixture", func(t *testing.T) {
		t.Parallel()

		require.NotEmpty(t, queue.Authored)
		pr := queue.Authored[0]

		assert.Equal(t, 23499, pr.Number)
		assert.Equal(t, "kalverra", pr.Author)
		assert.Equal(t, "smartcontractkit/chainlink", pr.RepoNameWithOwner)
		assert.Equal(t, "BLOCKED", pr.MergeStateStatus)
		assert.Equal(t, model.MergeStateBlocked, pr.MergeStatus.State())
		assert.True(t, pr.MergeStatus.IsBlocked())
		assert.False(t, pr.MergeStatus.IsClean())

		assert.False(t, pr.Checks.HasRequiredChecks)
		assert.Equal(t, 0, pr.Checks.ReqTotal)
		assert.Equal(t, 0, pr.Checks.ReqFailed)
		assert.True(t, pr.Checks.IsFailing())
		assert.Equal(t, "CI: ✓ 156  ✗ 61", pr.Checks.Badge())
	})

	t.Run("inbox PR parsed from real fixture", func(t *testing.T) {
		t.Parallel()

		require.NotEmpty(t, queue.Inbox)
		pr := queue.Inbox[0]

		assert.Equal(t, 23695, pr.Number)
		assert.Equal(t, "denis-chernov-smartcontract", pr.Author)
		assert.Equal(t, "smartcontractkit/chainlink", pr.RepoNameWithOwner)
		assert.False(t, pr.Checks.HasRequiredChecks)
		assert.True(t, pr.Checks.IsFailing()) // 1 failure in overall rollup
	})
}

func TestSource_ProgressContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	assert.Nil(t, source.ProgressFromContext(ctx), "nil context should return nil ProgressFunc")

	var calledWith []int
	fn := func(loaded, total int) {
		calledWith = append(calledWith, loaded, total)
	}

	pCtx := source.WithProgress(ctx, fn)
	extracted := source.ProgressFromContext(pCtx)
	require.NotNil(t, extracted)

	extracted(5, 20)
	assert.Equal(t, []int{5, 20}, calledWith)
}

func TestSourceInterface_FixtureCustomStaleThreshold(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "real_account_prs.json")
	src := source.NewFixtureSource(fixturePath, source.WithFixtureStaleActivityAfter(1*time.Minute))

	queue, err := src.Fetch(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, queue.Authored)
	assert.True(t, queue.Authored[0].Stale, "fixture PR with 1-minute threshold must be marked Stale")
}
