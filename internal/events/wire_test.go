package events_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/events"
)

func TestWireFrames(t *testing.T) {
	t.Parallel()

	we := &events.WireError{
		Code:    events.CodeNotFound,
		Message: "not found",
	}
	assert.Equal(t, "not_found: not found", we.Error())

	req := events.Request{
		ID:     "123",
		Method: "ping",
		Params: json.RawMessage(`{}`),
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"123","method":"ping","params":{}}`, string(data))

	resp := events.Response{
		ID:     "123",
		Result: json.RawMessage(`{"ok":true}`),
	}
	respData, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"123","result":{"ok":true}}`, string(respData))
}

func TestTriggerStrings(t *testing.T) {
	t.Parallel()

	strs := events.TriggerStrings()
	assert.Equal(t, []string{
		"ci_passed",
		"ci_failed",
		"conflict",
		"review_received",
		"pr_merged",
	}, strs)
}
