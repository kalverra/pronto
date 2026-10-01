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

	assert.Contains(t, view, "▌ ACTION REQUIRED")
	assert.Contains(t, view, "▌ MERGE QUEUE")
	assert.Contains(t, view, "▌ READY TO MERGE")
	assert.Contains(t, view, "▌ IN REVIEW")
	assert.Contains(t, view, "▌ DRAFTS")
	assert.Contains(t, view, "▌ STALE")
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

	assert.Contains(t, view, "▌ MERGE QUEUE")
	assert.Contains(t, view, "▌ ACTION REQUIRED")
	assert.Contains(t, view, "QUEUED")
}

func TestModel_MinePRInMergeQueueWithPreQueueCheckFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	enqueuedAt := now.Add(-30 * time.Minute)

	pr := model.PullRequest{
		Number:            23849,
		Title:             "fix(dockerfile): better version pins",
		Author:            "kalverra",
		RepoName:          "chainlink",
		RepoNameWithOwner: "smartcontractkit/chainlink",
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "UNSTABLE",
		IsInMergeQueue:    true,
		UpdatedAt:         now.Add(-10 * time.Minute),
		Checks: model.ChecksSummary{
			Total:  235,
			Done:   235,
			Failed: 1,
		},
		MergeQueue: &model.MergeQueueInfo{
			Position:   1,
			State:      "AWAITING_CHECKS",
			EnqueuedAt: &enqueuedAt,
		},
		MergeQueueChecks: model.ChecksSummary{
			State:   "PENDING",
			Total:   15,
			Running: 10,
			Done:    5,
			Failed:  0,
		},
		Stack: &model.PRStack{
			ID:       "stack-23852",
			Number:   23852,
			Size:     2,
			Position: 1,
		},
	}

	q := model.Queue{
		Authored: []model.PullRequest{pr},
	}

	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithDimensions(140, 30))

	// 1. In Mine Tab:
	mMine, _ := sendKey(m, tea.KeyTab)
	mineView := mMine.View()
	assert.Contains(t, mineView, "▌ MERGE QUEUE")
	assert.NotContains(t, mineView, "▌ ACTION REQUIRED")
	assert.Contains(t, mineView, "QUEUED")
	assert.NotContains(t, mineView, "FAILING CI")
	assert.Contains(t, mineView, "5/15")

	// 2. In Focus Tab (when focused):
	mFocused, _ := sendRune(mMine, 'f')
	mFocus, _ := sendRune(mFocused, '1')
	focusView := mFocus.View()
	assert.Contains(t, focusView, "▌ MERGE QUEUE")
	assert.NotContains(t, focusView, "▌ ACTION REQUIRED")

	// 3. Details Modal:
	mDetails, _ := sendKey(mMine, tea.KeyEnter)
	detailsView := mDetails.View()
	assert.Contains(t, detailsView, "QUEUED")
	assert.Contains(t, detailsView, "IN MERGE QUEUE (#1)")
	assert.Contains(t, detailsView, "CI Checks (Merge Queue)")
	assert.NotContains(t, detailsView, "FAILING CI")

	// 4. Explain Modal:
	mExplain, _ := sendRune(mMine, '?')
	explainView := mExplain.View()
	assert.Contains(t, explainView, "In merge queue")
	assert.Contains(t, explainView, "PR is in merge queue")
	assert.NotContains(t, explainView, "Failing CI checks")
}
