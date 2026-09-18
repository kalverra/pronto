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

func TestFreshFields_IncludesCheckTimestamps(t *testing.T) {
	t.Parallel()
	assert.Contains(t, freshFragment, "... on CheckRun { name status conclusion startedAt completedAt }")
	assert.Contains(t, freshFragment, "... on StatusContext { context state createdAt }")
}

func TestConvertPR_CheckTimestamps(t *testing.T) {
	t.Parallel()

	t.Run("check runs with startedAt and completedAt", func(t *testing.T) {
		t.Parallel()
		start1 := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
		end2 := time.Date(2026, 9, 17, 12, 10, 0, 0, time.UTC)

		freshJSON := `{
			"commits": {
				"nodes": [
					{
						"commit": {
							"statusCheckRollup": {
								"state": "SUCCESS",
								"contexts": {
									"totalCount": 2,
									"nodes": [
										{
											"__typename": "CheckRun",
											"name": "lint",
											"status": "COMPLETED",
											"conclusion": "SUCCESS",
											"startedAt": "2026-09-17T12:00:00Z",
											"completedAt": "2026-09-17T12:05:00Z"
										},
										{
											"__typename": "CheckRun",
											"name": "test",
											"status": "COMPLETED",
											"conclusion": "SUCCESS",
											"startedAt": "2026-09-17T12:02:00Z",
											"completedAt": "2026-09-17T12:10:00Z"
										}
									]
								}
							}
						}
					}
				]
			}
		}`
		var fresh rawFresh
		require.NoError(t, json.Unmarshal([]byte(freshJSON), &fresh))

		pr := convertPR(rawIdentity{Number: 1}, stableFields{}, fresh, false, convertOpts{})
		require.NotNil(t, pr.Checks.StartedAt)
		require.NotNil(t, pr.Checks.CompletedAt)
		assert.Equal(t, start1, *pr.Checks.StartedAt)
		assert.Equal(t, end2, *pr.Checks.CompletedAt)

		dur, ok := pr.Checks.Duration(time.Time{})
		assert.True(t, ok)
		assert.Equal(t, 10*time.Minute, dur)
	})

	t.Run("status context falls back to createdAt as startedAt", func(t *testing.T) {
		t.Parallel()
		created := time.Date(2026, 9, 17, 14, 30, 0, 0, time.UTC)

		freshJSON := `{
			"commits": {
				"nodes": [
					{
						"commit": {
							"statusCheckRollup": {
								"state": "SUCCESS",
								"contexts": {
									"totalCount": 1,
									"nodes": [
										{
											"__typename": "StatusContext",
											"context": "coverage",
											"state": "SUCCESS",
											"createdAt": "2026-09-17T14:30:00Z"
										}
									]
								}
							}
						}
					}
				]
			}
		}`
		var fresh rawFresh
		require.NoError(t, json.Unmarshal([]byte(freshJSON), &fresh))

		pr := convertPR(rawIdentity{Number: 2}, stableFields{}, fresh, false, convertOpts{})
		require.NotNil(t, pr.Checks.StartedAt)
		assert.Equal(t, created, *pr.Checks.StartedAt)
		assert.Nil(t, pr.Checks.CompletedAt)
	})

	t.Run("check run without timestamps leaves StartedAt and CompletedAt nil", func(t *testing.T) {
		t.Parallel()
		freshJSON := `{
			"commits": {
				"nodes": [
					{
						"commit": {
							"statusCheckRollup": {
								"state": "SUCCESS",
								"contexts": {
									"totalCount": 1,
									"nodes": [
										{
											"__typename": "CheckRun",
											"name": "build",
											"status": "COMPLETED",
											"conclusion": "SUCCESS"
										}
									]
								}
							}
						}
					}
				]
			}
		}`
		var fresh rawFresh
		require.NoError(t, json.Unmarshal([]byte(freshJSON), &fresh))

		pr := convertPR(rawIdentity{Number: 3}, stableFields{}, fresh, false, convertOpts{})
		assert.Nil(t, pr.Checks.StartedAt)
		assert.Nil(t, pr.Checks.CompletedAt)
	})
}
