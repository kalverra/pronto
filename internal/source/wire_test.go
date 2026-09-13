package source

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertPR_Stack(t *testing.T) {
	t.Parallel()

	t.Run("without stack", func(t *testing.T) {
		t.Parallel()
		pr := convertPR(rawIdentity{Number: 10}, stableFields{}, rawFresh{}, false, convertOpts{})
		assert.Nil(t, pr.Stack)
		assert.False(t, pr.IsPartOfStack())
	})

	t.Run("with stack and stackEntry", func(t *testing.T) {
		t.Parallel()
		ident := rawIdentity{
			Number:     101,
			Repository: rawRepo{NameWithOwner: "acme/repo"},
			Stack: &rawStack{
				ID:          "PRS_kwDO123",
				Number:      7,
				Size:        4,
				BaseRefName: "main",
			},
			StackEntry: &rawStackEntry{
				ID:       "PRSE_456",
				Position: 2,
			},
		}

		pr := convertPR(ident, stableFields{}, rawFresh{}, false, convertOpts{})

		require.NotNil(t, pr.Stack)
		assert.Equal(t, "PRS_kwDO123", pr.Stack.ID)
		assert.Equal(t, 7, pr.Stack.Number)
		assert.Equal(t, 4, pr.Stack.Size)
		assert.Equal(t, 2, pr.Stack.Position)
		assert.Equal(t, "main", pr.Stack.BaseRefName)
		assert.True(t, pr.IsPartOfStack())
		assert.Equal(t, "acme/repo#PRS_kwDO123", pr.StackKey())
	})
}

func TestDiscoveryFields_IncludesStack(t *testing.T) {
	t.Parallel()
	assert.Contains(t, discoveryFragment, "stack {")
	assert.Contains(t, discoveryFragment, "stackEntry {")
}

func TestDiscoveryFields_IncludesRefNames(t *testing.T) {
	t.Parallel()
	assert.Contains(t, discoveryFragment, "headRefName")
	assert.Contains(t, discoveryFragment, "baseRefName")
}

func TestConvertPR_RefNames(t *testing.T) {
	t.Parallel()
	ident := rawIdentity{
		Number:      101,
		HeadRefName: "feature-xyz",
		BaseRefName: "main",
	}
	pr := convertPR(ident, stableFields{}, rawFresh{}, false, convertOpts{})
	assert.Equal(t, "feature-xyz", pr.HeadRefName)
	assert.Equal(t, "main", pr.BaseRefName)
}

func TestConvertPR_StatusCheckRollup_LargePRTruncated(t *testing.T) {
	t.Parallel()

	ident := rawIdentity{
		Number: 23498,
		Repository: rawRepo{
			NameWithOwner: "smartcontractkit/chainlink",
		},
	}

	// 100 successful nodes (simulating GraphQL first: 100 truncation)
	var nodeJSONs []string
	for range 100 {
		nodeJSONs = append(
			nodeJSONs,
			`{"__typename":"CheckRun","name":"test-run","status":"COMPLETED","conclusion":"SUCCESS"}`,
		)
	}

	freshJSON := `{
		"commits": {
			"nodes": [
				{
					"commit": {
						"statusCheckRollup": {
							"state": "FAILURE",
							"contexts": {
								"totalCount": 216,
								"checkRunCountsByState": [
									{"state": "FAILURE", "count": 1},
									{"state": "SUCCESS", "count": 200},
									{"state": "SKIPPED", "count": 14}
								],
								"statusContextCountsByState": [
									{"state": "SUCCESS", "count": 1}
								],
								"nodes": [` + strings.Join(nodeJSONs, ",") + `]
							}
						}
					}
				}
			]
		}
	}`

	var fresh rawFresh
	err := json.Unmarshal([]byte(freshJSON), &fresh)
	require.NoError(t, err)

	pr := convertPR(ident, stableFields{}, fresh, false, convertOpts{})

	assert.True(t, pr.Checks.IsFailing(), "PR with rollup state FAILURE must be failing")
	assert.False(t, pr.Checks.IsPassing())
	assert.Equal(t, 216, pr.Checks.Total)
	assert.Equal(t, 1, pr.Checks.Failed)
	assert.Equal(t, "failing_ci", string(pr.ActionStatus()))
}

func TestConvertPR_StaleThreshold(t *testing.T) {
	t.Parallel()

	ident := rawIdentity{
		Number:    1,
		UpdatedAt: time.Now().Add(-2 * time.Hour),
	}

	prDefault := convertPR(ident, stableFields{}, rawFresh{}, false, convertOpts{})
	assert.False(t, prDefault.Stale, "2 hours is not stale under default 30-day threshold")

	prCustomStale := convertPR(ident, stableFields{}, rawFresh{}, false, convertOpts{
		staleActivityAfter: 1 * time.Hour,
	})
	assert.True(t, prCustomStale.Stale, "2 hours is stale under 1-hour threshold")
}

func TestDiscoveryFields_IncludesID(t *testing.T) {
	t.Parallel()
	assert.Contains(t, discoveryFragment, "\n  id\n")
}

func TestRawIdentity_UnmarshalID(t *testing.T) {
	t.Parallel()
	const data = `{"id":"PR_kwDO123","number":42}`
	var ident rawIdentity
	require.NoError(t, json.Unmarshal([]byte(data), &ident))
	assert.Equal(t, "PR_kwDO123", ident.ID)
}

func TestHydrateQueries_StaticStructure(t *testing.T) {
	t.Parallel()
	assert.Contains(t, hydrateFreshQuery, "query HydrateFresh($ids: [ID!]!)")
	assert.Contains(t, hydrateFreshQuery, "nodes(ids: $ids)")
	assert.Contains(t, hydrateFreshQuery, "... on PullRequest")
	assert.Contains(t, hydrateFreshQuery, "...FreshFields")
	assert.NotContains(t, hydrateFreshQuery, "...StableFields")
	assert.NotContains(t, hydrateFreshQuery, "fragment StableFields")
	assert.Contains(t, hydrateFreshQuery, "rateLimit")

	assert.Contains(t, hydrateFullQuery, "query HydrateFull($ids: [ID!]!)")
	assert.Contains(t, hydrateFullQuery, "nodes(ids: $ids)")
	assert.Contains(t, hydrateFullQuery, "... on PullRequest")
	assert.Contains(t, hydrateFullQuery, "...StableFields")
	assert.Contains(t, hydrateFullQuery, "...FreshFields")
	assert.Contains(t, hydrateFullQuery, "fragment StableFields")
	assert.Contains(t, hydrateFullQuery, "fragment FreshFields")
	assert.Contains(t, hydrateFullQuery, "rateLimit")
}
