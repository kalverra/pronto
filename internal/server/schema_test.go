package server_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/server"
)

func TestSchema_MethodEnumMatchesServerMethods(t *testing.T) {
	t.Parallel()

	var doc struct {
		Defs struct {
			Request struct {
				Properties struct {
					Method struct {
						Enum []string `json:"enum"`
					} `json:"method"`
				} `json:"properties"`
			} `json:"request"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal([]byte(events.SchemaJSON()), &doc))

	assert.ElementsMatch(t, server.Methods, doc.Defs.Request.Properties.Method.Enum,
		"schema.json method enum must match server.Methods")
}
