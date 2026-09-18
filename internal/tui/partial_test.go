package tui_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/score"
	"github.com/kalverra/pronto/internal/tui"
)

// makePartialQueue returns a queue holding one discovery-only PR (no merge
// status, checks, or diff data — Partial) and one fully hydrated PR.
func makePartialQueue() model.Queue {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	partial := model.PullRequest{
		Number:            301,
		Title:             "Half-loaded PR",
		URL:               "https://github.com/org/repo/pull/301",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		UpdatedAt:         now,
		Partial:           true,
	}
	hydrated := model.PullRequest{
		Number:            302,
		Title:             "Loaded PR",
		URL:               "https://github.com/org/repo/pull/302",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "bob",
		UpdatedAt:         now,
		Additions:         10,
		Deletions:         5,
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
	}
	return model.Queue{
		Viewer: "kalverra",
		Inbox:  []model.PullRequest{partial, hydrated},
	}
}

// A partial PR must render a loading indicator instead of misleading
// zero-value badges: a spinner + LOADING in the status column and no bogus
// diff size, while hydrated PRs render normally.
func TestView_PartialPRShowsLoadingIndicator(t *testing.T) {
	t.Parallel()

	m := tui.New(makePartialQueue(),
		tui.WithViewer("kalverra"),
		tui.WithNow(time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)),
		tui.WithWeights(score.DefaultWeights()),
		tui.WithActiveTab(tui.TabInbox),
	)

	view := m.View()
	assert.Contains(t, view, "Half-loaded PR", "partial PR must appear in the list")
	assert.Contains(t, view, "LOADING", "partial PR must carry a loading badge")
	assert.Contains(t, view, "⠋", "partial PR must show a spinner frame")
	assert.NotContains(t, view, "+0", "partial PR must not render a bogus diff size")
	assert.Contains(t, view, "Loaded PR", "hydrated PR must render normally")
}

// The spinner must keep animating while partial PRs are visible, not only
// while CI is running, so loading indicators don't freeze.
func TestSpinnerTick_AdvancesWithPartialPRs(t *testing.T) {
	t.Parallel()

	m := tui.New(makePartialQueue(), tui.WithWeights(score.DefaultWeights()))
	require.Zero(t, m.SpinnerFrame(), "initial spinner frame must be 0")

	updated, _ := m.Update(tui.SpinnerTickMsg{})
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	assert.Equal(t, 1, next.SpinnerFrame(), "partial PRs must keep the spinner animating")
}

// Without partial PRs or running CI, spinner ticks must not advance frames.
func TestSpinnerTick_StopsWithoutRunningCIOrPartials(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	settled := make([]model.PullRequest, len(q.Authored))
	copy(settled, q.Authored)
	for i := range settled {
		settled[i].Checks.ReqRunning = 0
		settled[i].Checks.ReqDone = settled[i].Checks.ReqTotal
	}
	q.Authored = settled

	m := tui.New(q, tui.WithWeights(score.DefaultWeights()))

	updated, _ := m.Update(tui.SpinnerTickMsg{})
	next, ok := updated.(tui.Model)
	require.True(t, ok)
	assert.Zero(t, next.SpinnerFrame(), "settled queue must not animate the spinner")
}
