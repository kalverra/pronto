package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
)

func TestMergeQueueInfo_Badge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		info      model.MergeQueueInfo
		wantBadge string
	}{
		{
			name:      "positive position",
			info:      model.MergeQueueInfo{Position: 3, State: "QUEUED"},
			wantBadge: "[queued #3]",
		},
		{
			name:      "position 1",
			info:      model.MergeQueueInfo{Position: 1, State: "QUEUED"},
			wantBadge: "[queued #1]",
		},
		{
			name:      "zero position",
			info:      model.MergeQueueInfo{Position: 0, State: "QUEUED"},
			wantBadge: "[queued]",
		},
		{
			name:      "negative position",
			info:      model.MergeQueueInfo{Position: -1, State: "QUEUED"},
			wantBadge: "[queued]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.wantBadge, tc.info.Badge())
		})
	}
}

func TestMergeQueueInfo_JSON(t *testing.T) {
	t.Parallel()

	t.Run("omits enqueued_at when nil", func(t *testing.T) {
		t.Parallel()
		info := model.MergeQueueInfo{
			Position: 2,
			State:    "MERGEABLE",
		}
		data, err := json.Marshal(info)
		require.NoError(t, err)
		assert.JSONEq(t, `{"position":2,"state":"MERGEABLE"}`, string(data))

		var decoded model.MergeQueueInfo
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)
		assert.Equal(t, info, decoded)
	})

	t.Run("includes enqueued_at when present", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
		info := model.MergeQueueInfo{
			Position:   5,
			State:      "QUEUED",
			EnqueuedAt: &now,
		}
		data, err := json.Marshal(info)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"enqueued_at":"2026-09-17T12:00:00Z"`)

		var decoded model.MergeQueueInfo
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)
		assert.Equal(t, info.Position, decoded.Position)
		assert.Equal(t, info.State, decoded.State)
		require.NotNil(t, decoded.EnqueuedAt)
		assert.True(t, now.Equal(*decoded.EnqueuedAt))
	})
}

func TestPullRequest_MergeQueue_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("nil MergeQueue roundtrip", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			Number: 42,
			Title:  "Regular PR",
		}

		data, err := json.Marshal(pr)
		require.NoError(t, err)
		assert.NotContains(t, string(data), `"merge_queue"`)

		var decoded model.PullRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)
		assert.Nil(t, decoded.MergeQueue)
	})

	t.Run("non-nil MergeQueue and MergeQueueChecks roundtrip", func(t *testing.T) {
		t.Parallel()
		enqueuedAt := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
		startedAt := time.Date(2026, 9, 17, 10, 31, 0, 0, time.UTC)
		pr := model.PullRequest{
			Number: 42,
			Title:  "Queued PR",
			MergeQueue: &model.MergeQueueInfo{
				Position:   3,
				State:      "QUEUED",
				EnqueuedAt: &enqueuedAt,
			},
			MergeQueueChecks: model.ChecksSummary{
				State:     "SUCCESS",
				Total:     4,
				Done:      4,
				Running:   0,
				Failed:    0,
				StartedAt: &startedAt,
			},
		}

		data, err := json.Marshal(pr)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"merge_queue":`)
		assert.Contains(t, string(data), `"merge_queue_checks":`)

		var decoded model.PullRequest
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)
		require.NotNil(t, decoded.MergeQueue)
		assert.Equal(t, 3, decoded.MergeQueue.Position)
		assert.Equal(t, "QUEUED", decoded.MergeQueue.State)
		require.NotNil(t, decoded.MergeQueue.EnqueuedAt)
		assert.True(t, enqueuedAt.Equal(*decoded.MergeQueue.EnqueuedAt))

		assert.Equal(t, "SUCCESS", decoded.MergeQueueChecks.State)
		assert.Equal(t, 4, decoded.MergeQueueChecks.Total)
		assert.Equal(t, 4, decoded.MergeQueueChecks.Done)
		assert.Equal(t, 0, decoded.MergeQueueChecks.Failed)
		require.NotNil(t, decoded.MergeQueueChecks.StartedAt)
		assert.True(t, startedAt.Equal(*decoded.MergeQueueChecks.StartedAt))
	})
}

func TestPullRequest_InMergeQueue_WithMergeQueueInfo(t *testing.T) {
	t.Parallel()

	pr := model.PullRequest{
		Number: 42,
		MergeQueue: &model.MergeQueueInfo{
			Position: 1,
			State:    "QUEUED",
		},
	}
	assert.True(t, pr.InMergeQueue())
}
