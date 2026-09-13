package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
)

func TestMergeStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		mergeable        string
		mergeStateStatus string
		isDraft          bool
		inMergeQueue     bool
		wantState        model.MergeState
		wantClean        bool
		wantConflict     bool
		wantBlocked      bool
		wantBehind       bool
		wantQueued       bool
		wantBadge        string
	}{
		{
			name:             "clean mergeable PR",
			mergeable:        "MERGEABLE",
			mergeStateStatus: "CLEAN",
			isDraft:          false,
			wantState:        model.MergeStateClean,
			wantClean:        true,
			wantConflict:     false,
			wantBlocked:      false,
			wantBehind:       false,
			wantQueued:       false,
			wantBadge:        "[clean]",
		},
		{
			name:             "conflicting mergeable field",
			mergeable:        "CONFLICTING",
			mergeStateStatus: "BLOCKED",
			isDraft:          false,
			wantState:        model.MergeStateConflict,
			wantClean:        false,
			wantConflict:     true,
			wantBlocked:      false,
			wantBehind:       false,
			wantQueued:       false,
			wantBadge:        "[conflict]",
		},
		{
			name:             "dirty mergeStateStatus",
			mergeable:        "UNKNOWN",
			mergeStateStatus: "DIRTY",
			isDraft:          false,
			wantState:        model.MergeStateConflict,
			wantClean:        false,
			wantConflict:     true,
			wantBlocked:      false,
			wantBehind:       false,
			wantQueued:       false,
			wantBadge:        "[conflict]",
		},
		{
			name:             "blocked by checks or reviews",
			mergeable:        "MERGEABLE",
			mergeStateStatus: "BLOCKED",
			isDraft:          false,
			wantState:        model.MergeStateBlocked,
			wantClean:        false,
			wantConflict:     false,
			wantBlocked:      true,
			wantBehind:       false,
			wantQueued:       false,
			wantBadge:        "[blocked]",
		},
		{
			name:             "behind base branch",
			mergeable:        "MERGEABLE",
			mergeStateStatus: "BEHIND",
			isDraft:          false,
			wantState:        model.MergeStateBehind,
			wantClean:        false,
			wantConflict:     false,
			wantBlocked:      false,
			wantBehind:       true,
			wantQueued:       false,
			wantBadge:        "[behind]",
		},
		{
			name:             "draft PR",
			mergeable:        "MERGEABLE",
			mergeStateStatus: "DRAFT",
			isDraft:          true,
			wantState:        model.MergeStateDraft,
			wantClean:        false,
			wantConflict:     false,
			wantBlocked:      false,
			wantBehind:       false,
			wantQueued:       false,
			wantBadge:        "[draft]",
		},
		{
			name:             "queued PR by mergeStateStatus",
			mergeable:        "MERGEABLE",
			mergeStateStatus: "QUEUED",
			isDraft:          false,
			wantState:        model.MergeStateQueued,
			wantClean:        false,
			wantConflict:     false,
			wantBlocked:      false,
			wantBehind:       false,
			wantQueued:       true,
			wantBadge:        "[queued]",
		},
		{
			name:             "queued PR by inMergeQueue flag",
			mergeable:        "MERGEABLE",
			mergeStateStatus: "BLOCKED",
			isDraft:          false,
			inMergeQueue:     true,
			wantState:        model.MergeStateQueued,
			wantClean:        false,
			wantConflict:     false,
			wantBlocked:      false,
			wantBehind:       false,
			wantQueued:       true,
			wantBadge:        "[queued]",
		},
		{
			name:             "unknown state",
			mergeable:        "UNKNOWN",
			mergeStateStatus: "UNKNOWN",
			isDraft:          false,
			wantState:        model.MergeStateUnknown,
			wantClean:        false,
			wantConflict:     false,
			wantBlocked:      false,
			wantBehind:       false,
			wantQueued:       false,
			wantBadge:        "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status := model.ComputeMergeStatus(tc.mergeable, tc.mergeStateStatus, tc.isDraft)
			status.IsInMergeQueue = tc.inMergeQueue
			assert.Equal(t, tc.wantState, status.State())
			assert.Equal(t, tc.wantClean, status.IsClean())
			assert.Equal(t, tc.wantConflict, status.HasConflict())
			assert.Equal(t, tc.wantBlocked, status.IsBlocked())
			assert.Equal(t, tc.wantBehind, status.IsBehind())
			assert.Equal(t, tc.wantQueued, status.IsQueued())
			assert.Equal(t, tc.wantBadge, status.Badge())
		})
	}
}
