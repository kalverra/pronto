package tui_test

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

func makeDetailTestPR(now time.Time) model.PullRequest {
	return model.PullRequest{
		Number:            101,
		Title:             "Add auth middleware",
		URL:               "https://github.com/org/repo/pull/101",
		RepoNameWithOwner: "org/repo",
		RepoOwner:         "org",
		RepoName:          "repo",
		HeadRefName:       "feat/auth",
		BaseRefName:       "main",
		Author:            "octocat",
		CreatedAt:         now.Add(-24 * time.Hour),
		UpdatedAt:         now.Add(-1 * time.Hour),
		Additions:         142,
		Deletions:         28,
		ChangedFiles:      3,
		Files:             []string{"internal/auth/auth.go", "internal/auth/auth_test.go", "cmd/server/main.go"},
		Mergeable:         "MERGEABLE",
		ReviewDecision:    "CHANGES_REQUESTED",
		LatestReviews: []model.Review{
			{Author: "alice", State: "CHANGES_REQUESTED", SubmittedAt: now.Add(-2 * time.Hour)},
			{Author: "bob", State: "APPROVED", SubmittedAt: now.Add(-4 * time.Hour)},
		},
		Checks: model.ChecksSummary{
			Total:     4,
			Done:      3,
			Failed:    1,
			Running:   1,
			ReqTotal:  2,
			ReqDone:   1,
			ReqFailed: 0,
		},
	}
}

func TestModel_KeyD_TriggersDiffViewer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := makeDetailTestPR(now)
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	var diffedPR *model.PullRequest
	differ := func(p model.PullRequest) tea.Cmd {
		diffedPR = &p
		return func() tea.Msg {
			return tui.DiffReadyMsg{PR: p}
		}
	}

	m := tui.New(q, tui.WithNow(now), tui.WithPRDiffer(differ))

	// Press 'd'
	m2, cmd := sendRune(m, 'd')
	require.NotNil(t, cmd, "pressing 'd' must return a command")
	require.NotNil(t, diffedPR, "pressing 'd' must call the differ")
	assert.Equal(t, 101, diffedPR.Number)
	assert.Equal(t, "https://github.com/org/repo/pull/101", diffedPR.URL)

	_ = m2
}

func TestModel_KeyEnter_TogglesDetailsView(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := makeDetailTestPR(now)
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(q, tui.WithNow(now))
	assert.False(t, m.IsDetailsOpen(), "details view should initially be closed")

	// Press Enter to open details view
	m2, cmd := sendKey(m, tea.KeyEnter)
	assert.Nil(t, cmd, "opening details view should not launch external process")
	mDetails := m2.(tui.Model)
	assert.True(t, mDetails.IsDetailsOpen(), "details view must be open after pressing Enter")

	view := mDetails.View()
	assert.Contains(t, view, "Add auth middleware")
	assert.Contains(t, view, "101")
	assert.Contains(t, view, "feat/auth")
	assert.Contains(t, view, "main")
	assert.Contains(t, view, "octocat")
	assert.Contains(t, view, "alice")
	assert.Contains(t, view, "CHANGES_REQUESTED")
	assert.Contains(t, view, "internal/auth/auth.go")

	// Press Enter again to close details view
	m3, _ := sendKey(mDetails, tea.KeyEnter)
	mClosed := m3.(tui.Model)
	assert.False(t, mClosed.IsDetailsOpen(), "details view must close after pressing Enter again")
}

func TestModel_DetailsView_KeyD_LaunchesDiff(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := makeDetailTestPR(now)
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	var diffedPR *model.PullRequest
	differ := func(p model.PullRequest) tea.Cmd {
		diffedPR = &p
		return nil
	}

	m := tui.New(q, tui.WithNow(now), tui.WithPRDiffer(differ))

	// Open details
	m2, _ := sendKey(m, tea.KeyEnter)
	mDetails := m2.(tui.Model)
	require.True(t, mDetails.IsDetailsOpen())

	// Press 'd' inside details
	_, cmd := sendRune(mDetails, 'd')
	_ = cmd
	require.NotNil(t, diffedPR, "pressing 'd' in details view must trigger diff")
	assert.Equal(t, 101, diffedPR.Number)
}

func TestModel_DetailsView_KeyV_LaunchesViewer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := makeDetailTestPR(now)
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	var viewedPR *model.PullRequest
	viewer := func(p model.PullRequest) tea.Cmd {
		viewedPR = &p
		return nil
	}

	m := tui.New(q, tui.WithNow(now), tui.WithPRViewer(viewer))

	// Open details
	m2, _ := sendKey(m, tea.KeyEnter)
	mDetails := m2.(tui.Model)
	require.True(t, mDetails.IsDetailsOpen())

	// Press 'v' inside details
	_, cmd := sendRune(mDetails, 'v')
	_ = cmd
	require.NotNil(t, viewedPR, "pressing 'v' in details view must trigger viewer")
	assert.Equal(t, 101, viewedPR.Number)
}

func TestModel_DetailsView_DismissWithEscOrQ(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := makeDetailTestPR(now)
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(q, tui.WithNow(now))

	// Open details
	m2, _ := sendKey(m, tea.KeyEnter)
	mDetails := m2.(tui.Model)
	require.True(t, mDetails.IsDetailsOpen())

	// Dismiss with 'esc'
	mEsc, _ := sendKey(mDetails, tea.KeyEsc)
	assert.False(t, mEsc.(tui.Model).IsDetailsOpen(), "'esc' must close details view")

	// Open again
	m3, _ := sendKey(mEsc, tea.KeyEnter)
	mDetails2 := m3.(tui.Model)
	require.True(t, mDetails2.IsDetailsOpen())

	// Dismiss with 'q'
	mQ, _ := sendRune(mDetails2, 'q')
	assert.False(t, mQ.(tui.Model).IsDetailsOpen(), "'q' must close details view")
}

func TestModel_DetailsView_KeyO_OpensBrowser(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := makeDetailTestPR(now)
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	var openedURL string
	opener := func(u string) error {
		openedURL = u
		return nil
	}

	m := tui.New(q, tui.WithNow(now), tui.WithOpener(opener))

	// Open details
	m2, _ := sendKey(m, tea.KeyEnter)
	mDetails := m2.(tui.Model)
	require.True(t, mDetails.IsDetailsOpen())

	// Press 'o'
	_, cmd := sendRune(mDetails, 'o')
	require.NotNil(t, cmd)
	_ = cmd()
	assert.Equal(t, "https://github.com/org/repo/pull/101", openedURL)
}

func TestModel_HelpBar_IncludesDetailsAndDiff(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := makeDetailTestPR(now)
	q := model.Queue{
		Inbox: []model.PullRequest{pr},
	}

	m := tui.New(q, tui.WithNow(now))
	view := m.View()
	assert.Contains(t, view, "enter: details")
	assert.Contains(t, view, "d: diff")
}
