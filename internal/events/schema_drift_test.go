package events_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/events"
)

func TestSchema_EventEnumMatchesValidTypes(t *testing.T) {
	t.Parallel()

	var doc struct {
		Defs struct {
			Event struct {
				Properties struct {
					Type struct {
						Enum []string `json:"enum"`
					} `json:"type"`
				} `json:"properties"`
			} `json:"event"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal([]byte(events.SchemaJSON()), &doc))

	want := make([]string, 0, len(events.ValidTypes))
	for typ := range events.ValidTypes {
		want = append(want, string(typ))
	}
	assert.ElementsMatch(t, want, doc.Defs.Event.Properties.Type.Enum,
		"schema.json event enum must match events.ValidTypes")
}

func TestSchema_ErrorCodeEnumMatchesValidCodes(t *testing.T) {
	t.Parallel()

	var doc struct {
		Defs struct {
			Error struct {
				Properties struct {
					Code struct {
						Enum []string `json:"enum"`
					} `json:"code"`
				} `json:"properties"`
			} `json:"error"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal([]byte(events.SchemaJSON()), &doc))

	want := []string{
		events.CodeInvalidRequest,
		events.CodeInvalidParams,
		events.CodeUnsupportedMethod,
		events.CodeNotFound,
	}
	assert.ElementsMatch(t, want, doc.Defs.Error.Properties.Code.Enum,
		"schema.json error enum must match events error code constants")
}

func TestSchema_SubscriptionRef(t *testing.T) {
	t.Parallel()

	var doc struct {
		Defs struct {
			Request struct {
				Properties struct {
					Params struct {
						Properties struct {
							Subscriptions struct {
								Items struct {
									Ref string `json:"$ref"`
								} `json:"items"`
							} `json:"subscriptions"`
						} `json:"properties"`
					} `json:"params"`
				} `json:"properties"`
			} `json:"request"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal([]byte(events.SchemaJSON()), &doc))

	assert.Equal(t, "#/$defs/subscription", doc.Defs.Request.Properties.Params.Properties.Subscriptions.Items.Ref)
}

func TestSchema_SnapshotDroppedEvents(t *testing.T) {
	t.Parallel()

	var doc struct {
		Defs struct {
			Snapshot struct {
				Properties struct {
					DroppedEvents struct {
						Type string `json:"type"`
					} `json:"dropped_events"`
				} `json:"properties"`
			} `json:"snapshot"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal([]byte(events.SchemaJSON()), &doc))

	assert.Equal(t, "integer", doc.Defs.Snapshot.Properties.DroppedEvents.Type,
		"schema.json snapshot must declare dropped_events property")
}
