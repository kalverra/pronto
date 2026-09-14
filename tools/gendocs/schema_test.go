package gendocs_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/tools/gendocs"
)

func TestRenderSchema_Deterministic(t *testing.T) {
	t.Parallel()

	first, err := gendocs.RenderSchema()
	require.NoError(t, err)
	second, err := gendocs.RenderSchema()
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second),
		"RenderSchema must be byte-identical across runs")
}

func TestRenderSchema_EnumsMatchCode(t *testing.T) {
	t.Parallel()

	raw, err := gendocs.RenderSchema()
	require.NoError(t, err)

	var doc struct {
		Defs struct {
			Request struct {
				Properties struct {
					Method struct {
						Enum []string `json:"enum"`
					} `json:"method"`
				} `json:"properties"`
			} `json:"request"`
			Error struct {
				Properties struct {
					Code struct {
						Enum []string `json:"enum"`
					} `json:"code"`
				} `json:"properties"`
			} `json:"error"`
			Event struct {
				Properties struct {
					Type struct {
						Enum []string `json:"enum"`
					} `json:"type"`
				} `json:"properties"`
			} `json:"event"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	assert.ElementsMatch(t, server.Methods, doc.Defs.Request.Properties.Method.Enum,
		"schema method enum must come from server.Methods")

	assert.ElementsMatch(t, events.ValidCodes, doc.Defs.Error.Properties.Code.Enum,
		"schema error code enum must come from events.ValidCodes")

	wantTypes := make([]string, 0, len(events.ValidTypes))
	for typ := range events.ValidTypes {
		wantTypes = append(wantTypes, string(typ))
	}
	assert.ElementsMatch(t, wantTypes, doc.Defs.Event.Properties.Type.Enum,
		"schema event type enum must come from events.ValidTypes")
}

func TestCommittedSchema_NotStale(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	generated, err := gendocs.RenderSchema()
	require.NoError(t, err)

	committed, err := os.ReadFile(
		filepath.Join(root, "internal", "events", "schema.json"),
	) // #nosec G304 — committed schema path under the repo.
	if os.IsNotExist(err) {
		t.Fatalf("internal/events/schema.json is missing; run `mise run generate` to generate it")
	}
	require.NoError(t, err)
	assert.Equal(t, string(generated), string(committed),
		"internal/events/schema.json is stale; run `mise run generate` and commit the result")
}
