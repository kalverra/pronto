package tui_test

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

func TestModel_MineCategoryDividers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:         101,
				Title:          "Intervention PR (failing ci)",
				UpdatedAt:      now.Add(-2 * time.Hour),
				ReviewDecision: "REVIEW_REQUIRED",
				Checks: model.ChecksSummary{
					HasRequiredChecks: true,
					ReqTotal:          2,
					ReqFailed:         1,
				},
			},
			{
				Number:           102,
				Title:            "Ready to merge PR (clean)",
				UpdatedAt:        now.Add(-3 * time.Hour),
				Mergeable:        "MERGEABLE",
				MergeStateStatus: "CLEAN",
				ReviewDecision:   "APPROVED",
			},
			{
				Number:         103,
				Title:          "In review PR",
				UpdatedAt:      now.Add(-4 * time.Hour),
				ReviewDecision: "REVIEW_REQUIRED",
			},
			{
				Number:    104,
				Title:     "Draft PR",
				IsDraft:   true,
				UpdatedAt: now.Add(-5 * time.Hour),
			},
			{
				Number:    105,
				Title:     "Old stale PR",
				UpdatedAt: now.Add(-40 * 24 * time.Hour),
			},
			{
				Number:         106,
				Title:          "Queued PR in merge queue",
				UpdatedAt:      now.Add(-1 * time.Hour),
				IsInMergeQueue: true,
			},
		},
	}

	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithDimensions(140, 30))
	// Switch to Mine tab
	mMine, _ := sendKey(m, tea.KeyTab)
	view := mMine.(tui.Model).View()

	assert.Contains(t, view, "── ACTION REQUIRED (1) ──")
	assert.Contains(t, view, "── MERGE QUEUE (1) ──")
	assert.Contains(t, view, "── READY TO MERGE (1) ──")
	assert.Contains(t, view, "── IN REVIEW (1) ──")
	assert.Contains(t, view, "── DRAFTS (1) ──")
	assert.Contains(t, view, "── STALE (1) ──")
}

func TestModel_MineStackedPRInMergeQueueNotDegraded(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	stackBase := &model.PRStack{
		ID:       "stack-chainlink",
		Number:   23507,
		Size:     14,
		Position: 6,
	}
	stackChild := &model.PRStack{
		ID:       "stack-chainlink",
		Number:   23507,
		Size:     14,
		Position: 13,
	}

	q := model.Queue{
		Authored: []model.PullRequest{
			{
				Number:           23498,
				Title:            "Lint Roller 6: core/services/pipeline",
				IsInMergeQueue:   true,
				MergeStateStatus: "CLEAN",
				UpdatedAt:        now.Add(-1 * time.Hour),
				Stack:            stackBase,
			},
			{
				Number:    23505,
				Title:     "Lint Roller 13: integration-tests",
				UpdatedAt: now.Add(-2 * time.Hour),
				Stack:     stackChild,
				Checks: model.ChecksSummary{
					HasRequiredChecks: true,
					ReqTotal:          1,
					ReqFailed:         1,
				},
			},
		},
	}

	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithDimensions(140, 30))
	mMine, _ := sendKey(m, tea.KeyTab)
	view := mMine.View()

	assert.Contains(t, view, "── MERGE QUEUE (1) ──")
	assert.Contains(t, view, "── ACTION REQUIRED (1) ──")
	assert.Contains(t, view, "QUEUED")
}
