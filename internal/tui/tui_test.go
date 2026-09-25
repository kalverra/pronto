package tui_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/score"
	"github.com/kalverra/pronto/internal/source"
	"github.com/kalverra/pronto/internal/tui"
)

func makeTestQueue() model.Queue {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	inboxPR1 := model.PullRequest{
		Number:            101,
		Title:             "Add auth middleware",
		URL:               "https://github.com/org/repo/pull/101",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Additions:         10,
		Deletions:         5,
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          2,
			ReqDone:           2,
			ReqFailed:         0,
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-4 * time.Hour),
				ReviewerUser: "kalverra",
			},
		},
	}

	inboxPR2 := model.PullRequest{
		Number:            102,
		Title:             "Fix broken pipeline",
		URL:               "https://github.com/org/repo/pull/102",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "bob",
		Additions:         150,
		Deletions:         30,
		Mergeable:         "CONFLICTING",
		MergeStateStatus:  "DIRTY",
		MergeStatus:       model.ComputeMergeStatus("CONFLICTING", "DIRTY", false),
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          3,
			ReqDone:           2,
			ReqFailed:         1,
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-1 * time.Hour),
				ReviewerUser: "kalverra",
			},
		},
	}

	authoredPR := model.PullRequest{
		Number:            201,
		Title:             "Implement TUI dashboard",
		URL:               "https://github.com/org/repo/pull/201",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "kalverra",
		Additions:         50,
		Deletions:         10,
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "BLOCKED",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "BLOCKED", false),
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          2,
			ReqDone:           1,
			ReqRunning:        1,
		},
	}

	return model.Queue{
		Inbox:    []model.PullRequest{inboxPR1, inboxPR2},
		Authored: []model.PullRequest{authoredPR},
	}
}

func sendKey(m tea.Model, keyType tea.KeyType, runes ...rune) (tea.Model, tea.Cmd) {
	msg := tea.KeyMsg{
		Type:  keyType,
		Runes: runes,
	}
	return m.Update(msg)
}

func sendRune(m tea.Model, r rune) (tea.Model, tea.Cmd) {
	return sendKey(m, tea.KeyRunes, r)
}

func TestModel_InitialState(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithWeights(score.DefaultWeights()))

	assert.Equal(t, tui.TabFocus, m.ActiveTab())
	assert.Equal(t, 0, m.Cursor())
	assert.False(t, m.IsModalOpen())

	require.Len(t, m.InboxItems(), 2)
	require.Len(t, m.MineItems(), 1)

	// Items should be ranked by score descending
	assert.GreaterOrEqual(t, m.InboxItems()[0].Score, m.InboxItems()[1].Score)

	// Focus tab starts empty, so no PR is selected
	assert.Nil(t, m.SelectedPR())

	// Switching to Inbox tab allows selecting inbox PR
	mInbox, _ := sendRune(m, '4')
	selected := mInbox.(tui.Model).SelectedPR()
	require.NotNil(t, selected)
	assert.Equal(t, m.InboxItems()[0].PR.Number, selected.Number)
}

func TestModel_TabSwitching(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	m := tui.New(q, tui.WithViewer("kalverra"))

	// Default active tab is Focus
	assert.Equal(t, tui.TabFocus, m.ActiveTab())

	// Tab advances to Mine
	m2, _ := sendKey(m, tea.KeyTab)
	model2 := m2.(tui.Model)
	assert.Equal(t, tui.TabMine, model2.ActiveTab())
	assert.Equal(t, 0, model2.Cursor())
	require.NotNil(t, model2.SelectedPR())
	assert.Equal(t, 201, model2.SelectedPR().Number)

	// Tab advances to Priority, then Inbox
	mP, _ := sendKey(model2, tea.KeyTab)
	assert.Equal(t, tui.TabPriority, mP.(tui.Model).ActiveTab())
	m3, _ := sendKey(mP, tea.KeyTab)
	model3 := m3.(tui.Model)
	assert.Equal(t, tui.TabInbox, model3.ActiveTab())

	// Tab cycles back to Focus
	mFocus, _ := sendKey(model3, tea.KeyTab)
	modelFocus := mFocus.(tui.Model)
	assert.Equal(t, tui.TabFocus, modelFocus.ActiveTab())

	// Shift+Tab reverses: Focus -> Inbox
	m4, _ := sendKey(modelFocus, tea.KeyShiftTab)
	model4 := m4.(tui.Model)
	assert.Equal(t, tui.TabInbox, model4.ActiveTab())

	// Number keys switch explicitly
	m5, _ := sendRune(model4, '1')
	model5 := m5.(tui.Model)
	assert.Equal(t, tui.TabFocus, model5.ActiveTab())

	m6, _ := sendRune(model5, '2')
	model6 := m6.(tui.Model)
	assert.Equal(t, tui.TabMine, model6.ActiveTab())

	m7, _ := sendRune(model6, '3')
	assert.Equal(t, tui.TabPriority, m7.(tui.Model).ActiveTab())

	m8, _ := sendRune(m7, '4')
	assert.Equal(t, tui.TabInbox, m8.(tui.Model).ActiveTab())
}

func TestModel_TabSwitching_FourTabs(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	m := tui.New(q, tui.WithViewer("kalverra"))

	// 1. Defaults to TabFocus
	assert.Equal(t, tui.TabFocus, m.ActiveTab())

	// 2. Tab cycling: Focus -> Mine -> Priority -> Inbox -> Focus
	m1, _ := sendKey(m, tea.KeyTab)
	assert.Equal(t, tui.TabMine, m1.(tui.Model).ActiveTab())

	mp, _ := sendKey(m1, tea.KeyTab)
	assert.Equal(t, tui.TabPriority, mp.(tui.Model).ActiveTab())

	m2, _ := sendKey(mp, tea.KeyTab)
	assert.Equal(t, tui.TabInbox, m2.(tui.Model).ActiveTab())

	m3, _ := sendKey(m2, tea.KeyTab)
	assert.Equal(t, tui.TabFocus, m3.(tui.Model).ActiveTab())

	// 3. Shift+Tab cycling: Focus -> Inbox -> Priority -> Mine -> Focus
	mr1, _ := sendKey(m3, tea.KeyShiftTab)
	assert.Equal(t, tui.TabInbox, mr1.(tui.Model).ActiveTab())

	mrp, _ := sendKey(mr1, tea.KeyShiftTab)
	assert.Equal(t, tui.TabPriority, mrp.(tui.Model).ActiveTab())

	mr2, _ := sendKey(mrp, tea.KeyShiftTab)
	assert.Equal(t, tui.TabMine, mr2.(tui.Model).ActiveTab())

	mr3, _ := sendKey(mr2, tea.KeyShiftTab)
	assert.Equal(t, tui.TabFocus, mr3.(tui.Model).ActiveTab())

	// 4. Number keys direct navigation: 1 Focus, 2 Mine, 3 Priority, 4 Inbox
	mk4, _ := sendRune(m, '4')
	assert.Equal(t, tui.TabInbox, mk4.(tui.Model).ActiveTab())

	mk3, _ := sendRune(mk4, '3')
	assert.Equal(t, tui.TabPriority, mk3.(tui.Model).ActiveTab())

	mk2, _ := sendRune(mk3, '2')
	assert.Equal(t, tui.TabMine, mk2.(tui.Model).ActiveTab())

	mk1, _ := sendRune(mk2, '1')
	assert.Equal(t, tui.TabFocus, mk1.(tui.Model).ActiveTab())

	// 5. Tab headers render all 4 tabs with counts
	view := m.View()
	assert.Contains(t, view, "1: Focus (0)")
	assert.Contains(t, view, "2: Mine (1)")
	assert.Contains(t, view, "3: Priority (0)")
	assert.Contains(t, view, "4: Inbox (2)")
}

func TestModel_FocusTab_HeadersIncludeAuthor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		Number:    101,
		Title:     "Focused PR",
		Author:    "alice",
		UpdatedAt: now.Add(-1 * time.Hour),
	}
	m := tui.New(
		model.Queue{},
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithFocusItems([]score.Scored{{PR: pr}}),
	)

	assert.Equal(t, tui.TabFocus, m.ActiveTab())
	view := m.View()
	assert.Contains(t, view, "AUTHOR")
}

func TestModel_NavigationKeybindings(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithActiveTab(tui.TabInbox))

	// Move down with 'j'
	m2, _ := sendRune(m, 'j')
	model2 := m2.(tui.Model)
	assert.Equal(t, 1, model2.Cursor())
	assert.Equal(t, model2.InboxItems()[1].PR.Number, model2.SelectedPR().Number)

	// Move down past end (clamped)
	m3, _ := sendRune(model2, 'j')
	model3 := m3.(tui.Model)
	assert.Equal(t, 1, model3.Cursor())

	// Move up with 'k'
	m4, _ := sendRune(model3, 'k')
	model4 := m4.(tui.Model)
	assert.Equal(t, 0, model4.Cursor())

	// Move up past start (clamped)
	m5, _ := sendRune(model4, 'k')
	model5 := m5.(tui.Model)
	assert.Equal(t, 0, model5.Cursor())

	// Down arrow key
	m6, _ := sendKey(model5, tea.KeyDown)
	model6 := m6.(tui.Model)
	assert.Equal(t, 1, model6.Cursor())

	// Up arrow key
	m7, _ := sendKey(model6, tea.KeyUp)
	model7 := m7.(tui.Model)
	assert.Equal(t, 0, model7.Cursor())

	// Jump to bottom 'G'
	m8, _ := sendRune(model7, 'G')
	model8 := m8.(tui.Model)
	assert.Equal(t, 1, model8.Cursor())

	// Jump to top 'g'
	m9, _ := sendRune(model8, 'g')
	model9 := m9.(tui.Model)
	assert.Equal(t, 0, model9.Cursor())
}

func TestModel_CursorPreservedPerTab(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithActiveTab(tui.TabInbox))

	// Move down in Inbox to index 1
	m2, _ := sendRune(m, 'j')
	model2 := m2.(tui.Model)
	assert.Equal(t, 1, model2.Cursor())

	// Switch to Focus (cursor should be 0)
	m3, _ := sendKey(model2, tea.KeyTab)
	model3 := m3.(tui.Model)
	assert.Equal(t, tui.TabFocus, model3.ActiveTab())
	assert.Equal(t, 0, model3.Cursor())

	// Switch to Mine (cursor should be 0)
	m4, _ := sendKey(model3, tea.KeyTab)
	model4 := m4.(tui.Model)
	assert.Equal(t, tui.TabMine, model4.ActiveTab())
	assert.Equal(t, 0, model4.Cursor())

	// Switch back to Inbox (cursor should still be 1)
	m5, _ := sendRune(model4, '4')
	model5 := m5.(tui.Model)
	assert.Equal(t, tui.TabInbox, model5.ActiveTab())
	assert.Equal(t, 1, model5.Cursor())
}

func TestModel_ScoreBreakdownModal(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithActiveTab(tui.TabInbox))

	assert.False(t, m.IsModalOpen())

	// Toggle modal with '?'
	m2, _ := sendRune(m, '?')
	model2 := m2.(tui.Model)
	assert.True(t, model2.IsModalOpen())

	// Modal view should contain breakdown terms and score
	view := model2.View()
	assert.Contains(t, view, "Score Breakdown")
	assert.Contains(t, view, "WaitHours")

	// Close modal with '?'
	m3, _ := sendRune(model2, '?')
	model3 := m3.(tui.Model)
	assert.False(t, model3.IsModalOpen())

	// Open again and close with Esc
	m4, _ := sendRune(model3, '?')
	model4 := m4.(tui.Model)
	assert.True(t, model4.IsModalOpen())

	m5, _ := sendKey(model4, tea.KeyEsc)
	model5 := m5.(tui.Model)
	assert.False(t, model5.IsModalOpen())
}

func TestModel_BadgesAndDisplayInView(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(120, 40),
		tui.WithActiveTab(tui.TabInbox),
	)

	view := m.View()

	// Contains PR information
	assert.Contains(t, view, "Add auth middleware")
	assert.Contains(t, view, "#101")
	assert.Contains(t, view, "alice")

	// Contains CI badge
	assert.Contains(t, view, "✓ 2/2")

	// Contains Merge status badge
	assert.Contains(t, view, "CLEAN")

	// Switch to Mine tab
	mMine, _ := sendRune(m, '2')
	mineView := mMine.View()
	assert.Contains(t, mineView, "Implement TUI dashboard")
	assert.Contains(t, mineView, "1/2 req (1 ⠋)")
}

func TestModel_SpinnerTick(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	// q has a PR with running CI in mine tab (#201)
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithDimensions(120, 40))

	m1, cmd := m.Update(tui.SpinnerTickMsg{})
	model1 := m1.(tui.Model)
	assert.Equal(t, 1, model1.SpinnerFrame())
	assert.Nil(t, cmd, "background ticker handles timing; update must not return recursive tick")

	// Switch to mine tab and check rendered frame 1
	mMine, _ := sendKey(model1, tea.KeyTab)
	mineView := mMine.View()
	assert.Contains(t, mineView, "1/2 req (1 ⠙)")
}

func TestModel_SpinnerTick_NoRunningCI(t *testing.T) {
	t.Parallel()

	m := tui.New(model.Queue{}, tui.WithViewer("kalverra"), tui.WithDimensions(120, 40))
	m1, cmd := m.Update(tui.SpinnerTickMsg{})
	model1 := m1.(tui.Model)
	assert.Equal(t, 0, model1.SpinnerFrame(), "spinner frame must not advance when no CI is running")
	assert.Nil(t, cmd)
}

func TestModel_NewDefaultColumnsAndBotBadge(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:            101,
				Title:             "Add auth middleware",
				UpdatedAt:         now.Add(-2 * time.Hour),
				RepoName:          "repo",
				RepoNameWithOwner: "org/repo",
				Author:            "alice",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				Checks: model.ChecksSummary{
					HasRequiredChecks: true,
					ReqTotal:          2,
					ReqDone:           2,
					ReqFailed:         0,
				},
			},
			{
				Number:            102,
				Title:             "Automated bump dependencies",
				UpdatedAt:         now.Add(-24 * time.Hour),
				RepoName:          "repo",
				RepoNameWithOwner: "org/repo",
				Author:            "dependabot",
				MergeStatus:       model.ComputeMergeStatus("CONFLICTING", "DIRTY", false),
				Checks: model.ChecksSummary{
					HasRequiredChecks: true,
					ReqTotal:          3,
					ReqDone:           2,
					ReqFailed:         1,
				},
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 40),
		tui.WithActiveTab(tui.TabInbox),
	)
	view := m.View()

	// Column headers: TITLE | STATUS | SIZE | CI | UPDATED | REPO | AUTHOR (and no old PR header)
	assert.Contains(t, view, "TITLE")
	assert.Contains(t, view, "STATUS")
	assert.Contains(t, view, "SIZE")
	assert.Contains(t, view, "CI")
	assert.Contains(t, view, "UPDATED")
	assert.Contains(t, view, "REPO")
	assert.Contains(t, view, "AUTHOR")
	assert.NotContains(t, view, " PR ")

	// Title formatted with #number prefix consistently with stacks
	assert.Contains(t, view, "#101  Add auth middleware")
	assert.Contains(t, view, "#102  Automated bump dependencies")
	assert.NotContains(t, view, "Add auth middleware (#101)")
	assert.NotContains(t, view, "Automated bump dependencies (#102)")

	// Solid pill badges for status
	assert.Contains(t, view, "CLEAN")
	assert.Contains(t, view, "CONFLICT")

	// Last updated relative age
	assert.Contains(t, view, "2h")
	assert.Contains(t, view, "1d")

	// Repo name
	assert.Contains(t, view, "repo")

	// Bot badge for dependabot, not for alice
	assert.Contains(t, view, "@dependabot")
	assert.Contains(t, view, "BOT")
	assert.Contains(t, view, "@alice")
}

func TestModel_QueuedPRStatusBadgeAndDivider(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:           101,
				Title:            "Inbox PR in merge queue",
				MergeStateStatus: "QUEUED",
				UpdatedAt:        now.Add(-10 * time.Minute),
			},
		},
		Authored: []model.PullRequest{
			{
				Number:           201,
				Title:            "My PR in merge queue",
				MergeStateStatus: "QUEUED",
				UpdatedAt:        now.Add(-15 * time.Minute),
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 40),
		tui.WithActiveTab(tui.TabInbox),
	)
	inboxView := m.View()
	assert.Contains(t, inboxView, "QUEUED")

	mMine, _ := sendRune(m, '2')
	mineView := mMine.View()
	assert.Contains(t, mineView, "QUEUED")
	assert.Contains(t, mineView, "▌ MERGE QUEUE")
}

func TestModel_EmptyQueue(t *testing.T) {
	t.Parallel()

	q := model.Queue{}
	m := tui.New(q, tui.WithViewer("kalverra"))

	assert.Nil(t, m.SelectedPR())
	assert.Nil(t, m.SelectedScored())

	// Navigation does not panic
	m2, _ := sendRune(m, 'j')
	model2 := m2.(tui.Model)
	assert.Equal(t, 0, model2.Cursor())

	m3, _ := sendRune(model2, 'k')
	model3 := m3.(tui.Model)
	assert.Equal(t, 0, model3.Cursor())

	// Modal toggle does not panic
	m4, _ := sendRune(model3, '?')
	model4 := m4.(tui.Model)
	assert.False(t, model4.IsModalOpen())

	// View displays empty state
	view := model4.View()
	assert.NotEmpty(t, view)
}

func TestModel_QuitKeybindings(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	m := tui.New(q, tui.WithViewer("kalverra"))

	// 'q' emits Quit
	_, cmd := sendRune(m, 'q')
	require.NotNil(t, cmd)
	msg := cmd()
	assert.IsType(t, tea.QuitMsg{}, msg)

	// 'ctrl+c' emits Quit
	_, cmd2 := sendKey(m, tea.KeyCtrlC)
	require.NotNil(t, cmd2)
	msg2 := cmd2()
	assert.IsType(t, tea.QuitMsg{}, msg2)
}

func TestModel_WithFixtureData(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "real_account_prs.json")
	src := source.NewFixtureSource(fixturePath)
	q, err := src.Fetch(t.Context())
	require.NoError(t, err)

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithActiveTab(tui.TabInbox))

	assert.NotEmpty(t, m.InboxItems())
	assert.NotEmpty(t, m.MineItems())

	sel := m.SelectedPR()
	require.NotNil(t, sel)
	assert.NotEmpty(t, sel.Title)
	assert.NotEmpty(t, sel.RepoNameWithOwner)

	view := m.View()
	assert.True(t, strings.Contains(view, "Inbox") || strings.Contains(view, "INBOX"))
}

func makeLargeTestQueue(inboxCount, authoredCount int) model.Queue {
	var inbox []model.PullRequest
	for i := 1; i <= inboxCount; i++ {
		inbox = append(inbox, model.PullRequest{
			Number:            i,
			Title:             fmt.Sprintf("Inbox pull request %02d", i),
			URL:               fmt.Sprintf("https://github.com/org/repo/pull/%d", i),
			RepoOwner:         "org",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			Author:            fmt.Sprintf("user%d", i),
		})
	}
	var authored []model.PullRequest
	for i := 101; i <= 100+authoredCount; i++ {
		authored = append(authored, model.PullRequest{
			Number:            i,
			Title:             fmt.Sprintf("Authored pull request %02d", i),
			URL:               fmt.Sprintf("https://github.com/org/repo/pull/%d", i),
			RepoOwner:         "org",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			Author:            "kalverra",
		})
	}
	return model.Queue{
		Inbox:    inbox,
		Authored: authored,
	}
}

func TestModel_TableHeadersAndScoresHidden(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(120, 30),
		tui.WithActiveTab(tui.TabInbox),
	)

	view := m.View()

	// Table headers must be present
	assert.Contains(t, view, "TITLE")
	assert.Contains(t, view, "STATUS")
	assert.Contains(t, view, "SIZE")
	assert.Contains(t, view, "CI")
	assert.Contains(t, view, "UPDATED")
	assert.Contains(t, view, "REPO")
	assert.Contains(t, view, "AUTHOR")

	// Scores must NOT be displayed in the table list view
	for _, item := range m.InboxItems() {
		scoreStr := fmt.Sprintf("[%4.1f]", item.Score)
		assert.NotContains(t, view, scoreStr)
	}

	// But '?' modal still exposes score breakdown
	mModal, _ := sendRune(m, '?')
	modalView := mModal.View()
	assert.Contains(t, modalView, "Score Breakdown")
	assert.Contains(t, modalView, "Score:")
}

func TestModel_ScrollWithArrowKeys(t *testing.T) {
	t.Parallel()

	// 25 PRs in inbox, terminal height 17 (leaving around 4-5 visible rows after notification chrome)
	q := makeLargeTestQueue(25, 0)
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithDimensions(100, 17), tui.WithActiveTab(tui.TabInbox))

	assert.Equal(t, 0, m.Cursor())
	assert.Equal(t, 0, m.ScrollOffset())

	initialView := m.View()
	assert.Contains(t, initialView, "Inbox pull request 01")
	assert.NotContains(t, initialView, "Inbox pull request 25")

	// Scroll down past the visible window using down arrow key
	current := m
	for range 10 {
		updated, _ := sendKey(current, tea.KeyDown)
		current = updated.(tui.Model)
	}

	assert.Equal(t, 10, current.Cursor())
	assert.Positive(t, current.ScrollOffset(), "scroll offset should increase when navigating past visible window")

	scrolledView := current.View()
	assert.NotContains(t, scrolledView, "Inbox pull request 01", "top items should have scrolled out of viewport")
	assert.Contains(t, scrolledView, "Inbox pull request 10")

	// Scroll back up using up arrow key
	for range 10 {
		updated, _ := sendKey(current, tea.KeyUp)
		current = updated.(tui.Model)
	}

	assert.Equal(t, 0, current.Cursor())
	assert.Equal(t, 0, current.ScrollOffset())
	assert.Contains(t, current.View(), "Inbox pull request 01")
}

func TestModel_ScrollPreservedPerTab(t *testing.T) {
	t.Parallel()

	q := makeLargeTestQueue(25, 25)
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithDimensions(100, 14), tui.WithActiveTab(tui.TabInbox))

	// Move down in Inbox
	current := m
	for range 10 {
		updated, _ := sendKey(current, tea.KeyDown)
		current = updated.(tui.Model)
	}
	inboxOffset := current.ScrollOffset()
	require.Positive(t, inboxOffset)

	// Switch to Focus tab
	mFocus, _ := sendKey(current, tea.KeyTab)
	focusModel := mFocus.(tui.Model)
	assert.Equal(t, tui.TabFocus, focusModel.ActiveTab())

	// Switch to Mine tab
	mMine, _ := sendKey(focusModel, tea.KeyTab)
	mineModel := mMine.(tui.Model)
	assert.Equal(t, tui.TabMine, mineModel.ActiveTab())
	assert.Equal(t, 0, mineModel.ScrollOffset(), "Mine tab should start at scroll offset 0")

	// Switch back to Inbox
	mInbox, _ := sendRune(mineModel, '4')
	inboxModel := mInbox.(tui.Model)
	assert.Equal(t, tui.TabInbox, inboxModel.ActiveTab())
	assert.Equal(t, inboxOffset, inboxModel.ScrollOffset(), "Inbox tab scroll offset should be preserved")
}

func TestModel_PageUpDown(t *testing.T) {
	t.Parallel()

	q := makeLargeTestQueue(30, 0)
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithDimensions(100, 14), tui.WithActiveTab(tui.TabInbox))

	// Page down advances cursor and scroll
	mPageDown, _ := sendKey(m, tea.KeyPgDown)
	pagedModel := mPageDown.(tui.Model)
	assert.Positive(t, pagedModel.Cursor())
	assert.Positive(t, pagedModel.ScrollOffset())

	// Page up returns toward top
	mPageUp, _ := sendKey(pagedModel, tea.KeyPgUp)
	upModel := mPageUp.(tui.Model)
	assert.Less(t, upModel.Cursor(), pagedModel.Cursor())
	assert.LessOrEqual(t, upModel.ScrollOffset(), pagedModel.ScrollOffset())
}

func TestModel_SizeColumnValues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:    1,
				Title:     "Small bugfix",
				Additions: 5,
				Deletions: 2, // Total 7 lines -> XS
				UpdatedAt: now.Add(-1 * time.Hour),
			},
			{
				Number:    2,
				Title:     "Medium feature",
				Additions: 120,
				Deletions: 30, // Total 150 lines -> M
				UpdatedAt: now.Add(-2 * time.Hour),
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(120, 30),
		tui.WithActiveTab(tui.TabInbox),
	)
	view := m.View()

	assert.Contains(t, view, "SIZE")
	assert.Contains(t, view, "+5")
	assert.Contains(t, view, "-2")
	assert.Contains(t, view, "+120")
	assert.Contains(t, view, "-30")
}

func TestModel_StaleBreakAndNavigation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	viewer := "kalverra"

	// 2 Active PRs (< 30 days) and 2 Stale PRs (>= 30 days)
	// Stale PRs have high wait hours, but active PRs must be partitioned first.
	activePR1 := model.PullRequest{
		Number:    101,
		Title:     "Active PR One",
		UpdatedAt: now.Add(-2 * 24 * time.Hour),
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-4 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}
	activePR2 := model.PullRequest{
		Number:    102,
		Title:     "Active PR Two",
		UpdatedAt: now.Add(-5 * 24 * time.Hour),
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-2 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}
	stalePR1 := model.PullRequest{
		Number:    201,
		Title:     "Stale PR One",
		UpdatedAt: now.Add(-60 * 24 * time.Hour),
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-60 * 24 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}
	stalePR2 := model.PullRequest{
		Number:    202,
		Title:     "Stale PR Two",
		UpdatedAt: now.Add(-45 * 24 * time.Hour),
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-45 * 24 * time.Hour),
				ReviewerUser: viewer,
			},
		},
	}

	q := model.Queue{
		Inbox: []model.PullRequest{stalePR1, activePR1, stalePR2, activePR2},
	}

	m := tui.New(
		q,
		tui.WithViewer(viewer),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)

	// Active items must come before stale items in InboxItems()
	inbox := m.InboxItems()
	require.Len(t, inbox, 4)
	assert.Equal(t, 101, inbox[0].PR.Number)
	assert.Equal(t, 102, inbox[1].PR.Number)
	assert.Equal(t, 201, inbox[2].PR.Number)
	assert.Equal(t, 202, inbox[3].PR.Number)

	view := m.View()

	// View must contain the divider break with stale count
	assert.Contains(t, view, "▌ STALE")

	// Verify order in rendered output: active PRs appear before stale divider,
	// and stale PRs appear after stale divider.
	activeIdx := strings.Index(view, "Active PR Two")
	dividerIdx := strings.Index(view, "▌ STALE")
	staleIdx := strings.Index(view, "Stale PR One")

	require.NotEqual(t, -1, activeIdx, "active PR should be in view")
	require.NotEqual(t, -1, dividerIdx, "divider should be in view")
	require.NotEqual(t, -1, staleIdx, "stale PR should be in view")
	assert.Less(t, activeIdx, dividerIdx, "active PRs must appear before STALE divider")
	assert.Less(t, dividerIdx, staleIdx, "stale PRs must appear after STALE divider")

	// Cursor navigation:
	// Start at 0 (activePR1)
	assert.Equal(t, 0, m.Cursor())
	assert.Equal(t, 101, m.SelectedPR().Number)

	// Move down to index 1 (activePR2)
	m1, _ := sendRune(m, 'j')
	model1 := m1.(tui.Model)
	assert.Equal(t, 1, model1.Cursor())
	assert.Equal(t, 102, model1.SelectedPR().Number)

	// Move down past the divider to index 2 (stalePR1). Cursor skips divider!
	m2, _ := sendRune(model1, 'j')
	model2 := m2.(tui.Model)
	assert.Equal(t, 2, model2.Cursor())
	assert.Equal(t, 201, model2.SelectedPR().Number)

	// Move up back over the divider to index 1 (activePR2). Cursor skips divider!
	m3, _ := sendRune(model2, 'k')
	model3 := m3.(tui.Model)
	assert.Equal(t, 1, model3.Cursor())
	assert.Equal(t, 102, model3.SelectedPR().Number)
}

func TestModel_StaleBreak_NoStalePRs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 1, Title: "Active PR 1", UpdatedAt: now.Add(-1 * time.Hour)},
			{Number: 2, Title: "Active PR 2", UpdatedAt: now.Add(-24 * time.Hour)},
		},
	}

	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithDimensions(120, 30))
	view := m.View()

	assert.NotContains(t, view, "STALE")
}

func TestModel_StaleBreak_AllStalePRs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{Number: 1, Title: "Stale PR 1", UpdatedAt: now.Add(-40 * 24 * time.Hour)},
			{Number: 2, Title: "Stale PR 2", UpdatedAt: now.Add(-60 * 24 * time.Hour)},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(120, 30),
		tui.WithActiveTab(tui.TabInbox),
	)
	view := m.View()

	assert.Contains(t, view, "▌ STALE")
	assert.Equal(t, 0, m.Cursor())
	assert.Equal(t, 1, m.SelectedPR().Number)
}

func TestModel_MineTab_OmitsAuthorColumn(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:    1,
				Title:     "Inbox PR",
				Author:    "alice",
				UpdatedAt: now.Add(-1 * time.Hour),
			},
		},
		Authored: []model.PullRequest{
			{
				Number:    2,
				Title:     "My PR",
				Author:    "kalverra",
				UpdatedAt: now.Add(-1 * time.Hour),
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)

	// In Inbox tab, AUTHOR column is present
	inboxView := m.View()
	assert.Contains(t, inboxView, "AUTHOR")
	assert.Contains(t, inboxView, "@alice")

	// Switch to Mine tab
	mMine, _ := sendRune(m, '2')
	mineView := mMine.View()

	// Mine tab has headers except AUTHOR
	assert.Contains(t, mineView, "TITLE")
	assert.Contains(t, mineView, "STATUS")
	assert.Contains(t, mineView, "SIZE")
	assert.Contains(t, mineView, "CI")
	assert.Contains(t, mineView, "UPDATED")
	assert.Contains(t, mineView, "REPO")
	assert.NotContains(t, mineView, "AUTHOR")
	assert.NotContains(t, mineView, "@kalverra")
}

func TestModel_OpenBrowser_Inbox(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	var openedURL string
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithActiveTab(tui.TabInbox),
		tui.WithOpener(func(rawURL string) error {
			openedURL = rawURL
			return nil
		}),
	)

	// Press 'o' on the first inbox item (auth middleware, #101)
	_, cmd := sendRune(m, 'o')
	require.NotNil(t, cmd)

	msg := cmd()
	require.IsType(t, tui.OpenURLMsg{}, msg)
	openMsg := msg.(tui.OpenURLMsg)
	require.NoError(t, openMsg.Err)
	assert.Equal(t, "https://github.com/org/repo/pull/101", openMsg.URL)
	assert.Equal(t, "https://github.com/org/repo/pull/101", openedURL)
}

func TestModel_OpenBrowser_Mine(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	var openedURL string
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithOpener(func(rawURL string) error {
			openedURL = rawURL
			return nil
		}),
	)

	// Switch to Mine tab
	m2, _ := sendKey(m, tea.KeyTab)

	// Press 'o' on the authored PR (#201)
	_, cmd := sendRune(m2, 'o')
	require.NotNil(t, cmd)

	msg := cmd()
	require.IsType(t, tui.OpenURLMsg{}, msg)
	openMsg := msg.(tui.OpenURLMsg)
	require.NoError(t, openMsg.Err)
	assert.Equal(t, "https://github.com/org/repo/pull/201", openMsg.URL)
	assert.Equal(t, "https://github.com/org/repo/pull/201", openedURL)
}

func TestModel_OpenBrowser_EmptyQueue(t *testing.T) {
	t.Parallel()

	q := model.Queue{}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithOpener(func(string) error {
			t.Fatal("opener should not be invoked when queue is empty")
			return nil
		}),
	)

	_, cmd := sendRune(m, 'o')
	assert.Nil(t, cmd)
}

func TestModel_OpenBrowser_OpenerError(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithActiveTab(tui.TabInbox),
		tui.WithOpener(func(string) error {
			return errors.New("failed to launch browser")
		}),
	)

	m2, cmd := sendRune(m, 'o')
	require.NotNil(t, cmd)

	msg := cmd()
	require.IsType(t, tui.OpenURLMsg{}, msg)
	openMsg := msg.(tui.OpenURLMsg)
	require.Error(t, openMsg.Err)
	assert.Contains(t, openMsg.Err.Error(), "failed to launch browser")

	// Model update with OpenURLMsg does not crash
	m3, followUpCmd := m2.Update(openMsg)
	assert.NotNil(t, m3)
	assert.Nil(t, followUpCmd)
}

func TestModel_OpenBrowser_ModalOpen(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithActiveTab(tui.TabInbox),
		tui.WithOpener(func(string) error {
			t.Fatal("opener should not be called when modal is open")
			return nil
		}),
	)

	// Open score breakdown modal
	mModal, _ := sendRune(m, '?')
	require.True(t, mModal.(tui.Model).IsModalOpen())

	// Pressing 'o' while modal is open should not trigger browser open
	_, cmd := sendRune(mModal, 'o')
	assert.Nil(t, cmd)
}

func TestModel_HelpBar_ContainsOpen(t *testing.T) {
	t.Parallel()

	q := makeTestQueue()
	m := tui.New(q, tui.WithViewer("kalverra"))
	view := m.View()
	assert.Contains(t, view, "o: open")
}

func TestModel_ActionStatusBadges(t *testing.T) {
	t.Parallel()

	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number: 101,
				Title:  "Failing CI PR",
				Checks: model.ChecksSummary{Failed: 1},
			},
			{
				Number:         102,
				Title:          "Changes Requested PR",
				ReviewDecision: "CHANGES_REQUESTED",
			},
			{
				Number:         103,
				Title:          "Needs Review PR",
				ReviewDecision: "REVIEW_REQUIRED",
			},
		},
	}
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithActiveTab(tui.TabInbox))
	view := m.View()

	assert.Contains(t, view, "FAILING CI")
	assert.Contains(t, view, "CHANGES REQ")
	assert.Contains(t, view, "NEEDS REVIEW")
}

func TestModel_InboxCategoryDividers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:         101,
				Title:          "Needs review PR",
				UpdatedAt:      now.Add(-2 * time.Hour),
				ReviewDecision: "REVIEW_REQUIRED",
				Checks: model.ChecksSummary{
					Total: 2,
					Done:  2,
				},
			},
			{
				Number:         102,
				Title:          "Blocked CI failing PR",
				UpdatedAt:      now.Add(-3 * time.Hour),
				ReviewDecision: "REVIEW_REQUIRED",
				Checks: model.ChecksSummary{
					Total:  2,
					Failed: 1,
				},
			},
			{
				Number:    103,
				Title:     "Old stale PR",
				UpdatedAt: now.Add(-40 * 24 * time.Hour),
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)
	view := m.View()

	assert.Contains(t, view, "▌ NEEDS YOUR ATTENTION")
	assert.Contains(t, view, "▌ BLOCKED")
	assert.Contains(t, view, "▌ STALE")
}

func TestModel_StackGrouping(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	// PR 101: Stack Alpha, Position 1 of 3, base PR (wait 2h)
	pr101 := model.PullRequest{
		Number:            101,
		Title:             "Alpha base migration",
		URL:               "https://github.com/org/repo/pull/101",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
		Stack: &model.PRStack{
			ID:          "PRS_ALPHA",
			Number:      1,
			Size:        3,
			Position:    1,
			BaseRefName: "main",
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-2 * time.Hour),
				ReviewerUser: "kalverra",
			},
		},
	}

	// PR 102: Stack Alpha, Position 2 of 3 (would normally be Blocked due to conflict,
	// but because PR 101 is in Attention, the entire stack is pulled into Attention!)
	pr102 := model.PullRequest{
		Number:            102,
		Title:             "Alpha middle endpoint",
		URL:               "https://github.com/org/repo/pull/102",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Mergeable:         "CONFLICTING",
		MergeStateStatus:  "DIRTY",
		MergeStatus:       model.ComputeMergeStatus("CONFLICTING", "DIRTY", false),
		Stack: &model.PRStack{
			ID:          "PRS_ALPHA",
			Number:      1,
			Size:        3,
			Position:    2,
			BaseRefName: "main",
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-5 * time.Hour),
				ReviewerUser: "kalverra",
			},
		},
	}

	// PR 103: Stack Alpha, Position 3 of 3
	pr103 := model.PullRequest{
		Number:            103,
		Title:             "Alpha top UI",
		URL:               "https://github.com/org/repo/pull/103",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
		Stack: &model.PRStack{
			ID:          "PRS_ALPHA",
			Number:      1,
			Size:        3,
			Position:    3,
			BaseRefName: "main",
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-1 * time.Hour),
				ReviewerUser: "kalverra",
			},
		},
	}

	// PR 200: Independent non-stack PR with higher wait time (10h) than PR 101
	pr200 := model.PullRequest{
		Number:            200,
		Title:             "High priority standalone PR",
		URL:               "https://github.com/org/repo/pull/200",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "bob",
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-10 * time.Hour),
				ReviewerUser: "kalverra",
			},
		},
	}

	// PR 300: Solo stack PR (only PR in this stack present in the queue)
	pr300 := model.PullRequest{
		Number:            300,
		Title:             "Solo stack PR",
		URL:               "https://github.com/org/repo/pull/300",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "carol",
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
		Stack: &model.PRStack{
			ID:          "PRS_BETA",
			Number:      2,
			Size:        2,
			Position:    2,
			BaseRefName: "main",
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-1 * time.Minute),
				ReviewerUser: "kalverra",
			},
		},
	}

	q := model.Queue{
		Inbox: []model.PullRequest{pr103, pr102, pr200, pr101, pr300},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(160, 40),
		tui.WithCollapsedStacks(false),
		tui.WithActiveTab(tui.TabInbox),
	)
	view := m.View()

	// 1. All 3 Alpha PRs must be in Attention (since PR 101 pulled the stack in)
	assert.Contains(t, view, "▌ NEEDS YOUR ATTENTION")

	// 2. Folder banner + tree connectors rendered in Title column
	assert.Contains(t, view, "(3 PRs · #101..#103)")
	assert.NotContains(t, view, "[STACK]")
	assert.Contains(t, view, "├─ #101 [1/3] Alpha base migration")
	assert.Contains(t, view, "├─ #102 [2/3] Alpha middle endpoint")
	assert.Contains(t, view, "╰─ #103 [3/3] Alpha top UI")

	// 3. Solo stack member rendered with #number and stack pill [2/2]
	assert.Contains(t, view, "#300  [2/2]  Solo stack PR")
	assert.NotContains(t, view, "╶ [2/2]")

	// 4. Standalone PR does not have tree connector
	assert.Contains(t, view, "High priority standalone PR")
	assert.NotContains(t, view, "┌ High priority")

	// 5. Contiguous ordering: PR 200 before Stack Alpha; 101 before 102 before 103; Stack Alpha before PR 300
	idx200 := strings.Index(view, "High priority standalone PR")
	idx101 := strings.Index(view, "Alpha base migration")
	idx102 := strings.Index(view, "Alpha middle endpoint")
	idx103 := strings.Index(view, "Alpha top UI")
	idx300 := strings.Index(view, "Solo stack PR")

	assert.True(t, idx200 >= 0 && idx101 >= 0 && idx200 < idx101, "PR 200 should rank before Stack Alpha")
	assert.Less(t, idx101, idx102, "PR 101 should appear before PR 102")
	assert.Less(t, idx102, idx103, "PR 102 should appear before PR 103")
	assert.Less(t, idx103, idx300, "Stack Alpha should appear before PR 300")

	// 6. Navigation and score breakdown modal shows stack details
	mDown, _ := sendKey(m, tea.KeyDown)
	mModal, _ := sendRune(mDown, '?')
	modalView := mModal.(tui.Model).View()
	assert.Contains(t, modalView, "Score Breakdown: org/repo#101")
	assert.Contains(t, modalView, "Stack: #1 • Entry 1 of 3 (base: main)")
}

func TestModel_StackGrouping_Mine(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	pr501 := model.PullRequest{
		Number:            501,
		Title:             "Mine stack bottom",
		URL:               "https://github.com/org/repo/pull/501",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "kalverra",
		UpdatedAt:         now.Add(-1 * time.Hour),
		Stack: &model.PRStack{
			ID:          "PRS_GAMMA",
			Number:      10,
			Size:        2,
			Position:    1,
			BaseRefName: "main",
		},
	}

	pr502 := model.PullRequest{
		Number:            502,
		Title:             "Mine stack top",
		URL:               "https://github.com/org/repo/pull/502",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "kalverra",
		UpdatedAt:         now.Add(-40 * 24 * time.Hour),
		Stack: &model.PRStack{
			ID:          "PRS_GAMMA",
			Number:      10,
			Size:        2,
			Position:    2,
			BaseRefName: "main",
		},
	}

	q := model.Queue{
		Authored: []model.PullRequest{pr502, pr501},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithCollapsedStacks(false),
	)
	mTab, _ := sendKey(m, tea.KeyTab)
	view := mTab.(tui.Model).View()

	assert.Contains(t, view, "├─ #501 [1/2] Mine stack bottom")
	assert.Contains(t, view, "╰─ #502 [2/2] Mine stack top")
	assert.NotContains(t, view, "── STALE (")
}

func TestModel_ScrollIndicatorCountsVisiblePRs(t *testing.T) {
	t.Parallel()

	// The scroll indicator must count item rows, not display rows: display
	// row indices include section dividers and can exceed the PR count.
	q := makeLargeTestQueue(25, 0)
	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithDimensions(100, 17), tui.WithActiveTab(tui.TabInbox))

	mEnd, _ := sendRune(m, 'G')
	atEnd := mEnd.(tui.Model)

	assert.Contains(t, atEnd.View(), "showing 7 of 25 PRs")
}

func TestModel_DividersSpanTableWidthAndUncolored(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:         101,
				Title:          "Needs review PR",
				UpdatedAt:      now.Add(-2 * time.Hour),
				ReviewDecision: "REVIEW_REQUIRED",
			},
		},
	}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)
	view := m.View()

	// 1. Must contain the divider prefix
	assert.Contains(t, view, "▌ NEEDS YOUR ATTENTION")

	// 2. The divider line must span more of the table width (width is 140, divider line should be >= 120 chars)
	lines := strings.Split(view, "\n")
	var dividerLine string
	for _, l := range lines {
		if strings.Contains(l, "NEEDS YOUR ATTENTION") {
			dividerLine = l
			break
		}
	}
	require.NotEmpty(t, dividerLine, "divider line should exist in view")
	assert.GreaterOrEqual(t, lipgloss.Width(dividerLine), 120, "divider line should span most of the table width")
	// The divider line should be filled with horizontal rule characters '─', not empty trailing whitespace
	assert.GreaterOrEqual(
		t,
		strings.Count(dividerLine, "─"),
		100,
		"divider line should span with horizontal rule characters",
	)
}

func TestModel_PRStacks_CollapsedByDefault(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr1 := model.PullRequest{
		Number:            101,
		Title:             "Alpha base",
		URL:               "https://github.com/org/repo/pull/101",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Stack: &model.PRStack{
			ID:          "PRS_ALPHA",
			Number:      1,
			Size:        3,
			Position:    1,
			BaseRefName: "main",
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-2 * time.Hour),
				ReviewerUser: "kalverra",
			},
		},
	}
	pr2 := model.PullRequest{
		Number:            102,
		Title:             "Alpha middle",
		URL:               "https://github.com/org/repo/pull/102",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Stack: &model.PRStack{
			ID:          "PRS_ALPHA",
			Number:      1,
			Size:        3,
			Position:    2,
			BaseRefName: "main",
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-2 * time.Hour),
				ReviewerUser: "kalverra",
			},
		},
	}
	pr3 := model.PullRequest{
		Number:            103,
		Title:             "Alpha top",
		URL:               "https://github.com/org/repo/pull/103",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Stack: &model.PRStack{
			ID:          "PRS_ALPHA",
			Number:      1,
			Size:        3,
			Position:    3,
			BaseRefName: "main",
		},
		TimelineItems: []model.TimelineItem{
			{
				Type:         model.TimelineItemReviewRequested,
				CreatedAt:    now.Add(-2 * time.Hour),
				ReviewerUser: "kalverra",
			},
		},
	}

	q := model.Queue{Inbox: []model.PullRequest{pr1, pr2, pr3}}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)
	view := m.View()

	// Bottom PR visible via collapsed stack pill with root title
	assert.Contains(t, view, "[1/3]")
	assert.NotContains(t, view, "[STACK]")
	assert.NotContains(t, view, "[1..3]")
	assert.Contains(t, view, "Alpha base")
	// Child PRs hidden
	assert.NotContains(t, view, "Alpha middle")
	assert.NotContains(t, view, "Alpha top")
}

func TestModel_PRStacks_ToggleExpandAndCollapse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr1 := model.PullRequest{
		Number:            101,
		Title:             "Alpha base",
		URL:               "https://github.com/org/repo/pull/101",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Stack:             &model.PRStack{ID: "PRS_ALPHA", Number: 1, Size: 3, Position: 1, BaseRefName: "main"},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	pr2 := model.PullRequest{
		Number:            102,
		Title:             "Alpha middle",
		URL:               "https://github.com/org/repo/pull/102",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Stack:             &model.PRStack{ID: "PRS_ALPHA", Number: 1, Size: 3, Position: 2, BaseRefName: "main"},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	pr3 := model.PullRequest{
		Number:            103,
		Title:             "Alpha top",
		URL:               "https://github.com/org/repo/pull/103",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Stack:             &model.PRStack{ID: "PRS_ALPHA", Number: 1, Size: 3, Position: 3, BaseRefName: "main"},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
		},
	}

	q := model.Queue{Inbox: []model.PullRequest{pr1, pr2, pr3}}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)

	// Initial: collapsed
	assert.Contains(t, m.View(), "[1/3]")
	assert.NotContains(t, m.View(), "[STACK]")
	assert.NotContains(t, m.View(), "[1..3]")

	// Press space: expands stack
	mExpanded, _ := sendKey(m, tea.KeySpace)
	viewExp := mExpanded.(tui.Model).View()
	assert.Contains(t, viewExp, "(3 PRs · #101..#103)")
	assert.NotContains(t, viewExp, "[STACK]")
	assert.Contains(t, viewExp, "├─ #101 [1/3] Alpha base")
	assert.Contains(t, viewExp, "├─ #102 [2/3] Alpha middle")
	assert.Contains(t, viewExp, "╰─ #103 [3/3] Alpha top")

	// Press space again: collapses stack
	mCollapsed, _ := sendKey(mExpanded, tea.KeySpace)
	viewCol := mCollapsed.(tui.Model).View()
	assert.Contains(t, viewCol, "[1/3]")
	assert.NotContains(t, viewCol, "[STACK]")
	assert.NotContains(t, viewCol, "[1..3]")
	assert.NotContains(t, viewCol, "Alpha middle")

	// Press 'e': also expands
	mE, _ := sendRune(mCollapsed, 'e')
	assert.Contains(t, mE.(tui.Model).View(), "├─ #101 [1/3] Alpha base")
}

func TestModel_PRStacks_ToggleAllKey(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	prA1 := model.PullRequest{
		Number:            101,
		Title:             "Alpha 1",
		RepoNameWithOwner: "org/repo",
		Stack:             &model.PRStack{ID: "STACK_A", Number: 1, Size: 2, Position: 1},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	prA2 := model.PullRequest{
		Number:            102,
		Title:             "Alpha 2",
		RepoNameWithOwner: "org/repo",
		Stack:             &model.PRStack{ID: "STACK_A", Number: 1, Size: 2, Position: 2},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-2 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	prB1 := model.PullRequest{
		Number:            201,
		Title:             "Beta 1",
		RepoNameWithOwner: "org/repo",
		Stack:             &model.PRStack{ID: "STACK_B", Number: 2, Size: 2, Position: 1},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	prB2 := model.PullRequest{
		Number:            202,
		Title:             "Beta 2",
		RepoNameWithOwner: "org/repo",
		Stack:             &model.PRStack{ID: "STACK_B", Number: 2, Size: 2, Position: 2},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
		},
	}

	q := model.Queue{Inbox: []model.PullRequest{prA1, prA2, prB1, prB2}}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)

	// Initial: both collapsed
	view := m.View()
	assert.Contains(t, view, "[1/2]")
	assert.NotContains(t, view, "[STACK]")
	assert.NotContains(t, view, "[1..2]")
	assert.Contains(t, view, "Alpha 1")
	assert.Contains(t, view, "Beta 1")
	assert.NotContains(t, view, "Alpha 2")
	assert.NotContains(t, view, "Beta 2")

	// Press 'E': expands all stacks
	mAllExp, _ := sendRune(m, 'E')
	viewAllExp := mAllExp.(tui.Model).View()
	assert.Contains(t, viewAllExp, "(2 PRs · #101..#102)")
	assert.Contains(t, viewAllExp, "(2 PRs · #201..#202)")
	assert.NotContains(t, viewAllExp, "[STACK]")
	assert.Contains(t, viewAllExp, "├─ #101 [1/2] Alpha 1")
	assert.Contains(t, viewAllExp, "╰─ #102 [2/2] Alpha 2")
	assert.Contains(t, viewAllExp, "├─ #201 [1/2] Beta 1")
	assert.Contains(t, viewAllExp, "╰─ #202 [2/2] Beta 2")

	// Press 'E' again: collapses all stacks
	mAllCol, _ := sendRune(mAllExp, 'E')
	viewAllCol := mAllCol.(tui.Model).View()
	assert.Contains(t, viewAllCol, "[1/2]")
	assert.NotContains(t, viewAllCol, "[STACK]")
	assert.NotContains(t, viewAllCol, "[1..2]")
	assert.Contains(t, viewAllCol, "Alpha 1")
	assert.Contains(t, viewAllCol, "Beta 1")
	assert.NotContains(t, viewAllCol, "Alpha 2")
	assert.NotContains(t, viewAllCol, "Beta 2")
}

func TestModel_PRStacks_CursorNavigationAndClamping(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr1 := model.PullRequest{
		Number:            101,
		Title:             "Alpha base",
		RepoNameWithOwner: "org/repo",
		Stack:             &model.PRStack{ID: "PRS_ALPHA", Number: 1, Size: 3, Position: 1},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-3 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	pr2 := model.PullRequest{
		Number:            102,
		Title:             "Alpha middle",
		RepoNameWithOwner: "org/repo",
		Stack:             &model.PRStack{ID: "PRS_ALPHA", Number: 1, Size: 3, Position: 2},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-3 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	pr3 := model.PullRequest{
		Number:            103,
		Title:             "Alpha top",
		RepoNameWithOwner: "org/repo",
		Stack:             &model.PRStack{ID: "PRS_ALPHA", Number: 1, Size: 3, Position: 3},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-3 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	prSolo := model.PullRequest{
		Number:            200,
		Title:             "Standalone next",
		RepoNameWithOwner: "org/repo",
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
		},
	}

	q := model.Queue{Inbox: []model.PullRequest{pr1, pr2, pr3, prSolo}}
	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)

	// When collapsed, cursor moving down should skip hidden child PRs and land directly on prSolo
	mDown, _ := sendKey(m, tea.KeyDown)
	selected := mDown.(tui.Model).SelectedPR()
	require.NotNil(t, selected)
	assert.Equal(t, 200, selected.Number, "cursor down should skip collapsed stack items to next visible PR")

	// Move back up to bottom PR
	mUp, _ := sendKey(mDown, tea.KeyUp)
	selectedUp := mUp.(tui.Model).SelectedPR()
	require.NotNil(t, selectedUp)
	assert.Equal(t, 101, selectedUp.Number)

	// Expand stack
	mExp, _ := sendKey(mUp, tea.KeySpace)

	// Navigate down to PR 102
	mToChild, _ := sendKey(mExp, tea.KeyDown)
	selectedChild := mToChild.(tui.Model).SelectedPR()
	require.NotNil(t, selectedChild)
	assert.Equal(t, 102, selectedChild.Number)

	// Collapse stack while cursor is on PR 102 -> cursor must snap to bottom PR 101
	mColFromChild, _ := sendKey(mToChild, tea.KeySpace)
	selectedClamped := mColFromChild.(tui.Model).SelectedPR()
	require.NotNil(t, selectedClamped)
	assert.Equal(t, 101, selectedClamped.Number, "collapsing while on child PR must snap cursor to bottom PR")
}

func TestModel_TableView_CIDuration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	startedRunning := now.Add(-14*time.Minute - 12*time.Second)
	startedDone := now.Add(-10 * time.Minute)
	completedDone := now.Add(-6 * time.Minute)
	startedFailed := now.Add(-15 * time.Minute)
	completedFailed := now.Add(-3 * time.Minute)

	prRunning := model.PullRequest{
		Number:            101,
		Title:             "Running CI PR",
		RepoNameWithOwner: "org/repo",
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          2,
			ReqDone:           1,
			ReqRunning:        1,
			StartedAt:         &startedRunning,
		},
	}

	prCompleted := model.PullRequest{
		Number:            102,
		Title:             "Completed CI PR",
		RepoNameWithOwner: "org/repo",
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          2,
			ReqDone:           2,
			ReqFailed:         0,
			StartedAt:         &startedDone,
			CompletedAt:       &completedDone,
		},
	}

	prFailed := model.PullRequest{
		Number:            103,
		Title:             "Failed CI PR",
		RepoNameWithOwner: "org/repo",
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          1,
			ReqDone:           1,
			ReqFailed:         1,
			StartedAt:         &startedFailed,
			CompletedAt:       &completedFailed,
		},
	}

	prNoTimes := model.PullRequest{
		Number:            104,
		Title:             "No Times PR",
		RepoNameWithOwner: "org/repo",
		Checks: model.ChecksSummary{
			HasRequiredChecks: true,
			ReqTotal:          2,
			ReqDone:           2,
			ReqFailed:         0,
		},
	}

	q := model.Queue{
		Inbox: []model.PullRequest{prRunning, prCompleted, prFailed, prNoTimes},
	}

	m := tui.New(q, tui.WithNow(now), tui.WithDimensions(140, 30), tui.WithActiveTab(tui.TabInbox))
	view := m.View()

	// Running checks show spinner and compact duration
	assert.Contains(t, view, "1/2 req (1 ⠋)")
	assert.Contains(t, view, "14m12s")
	assert.NotContains(t, view, "1/2 req (1 ⠋) 14m12s")
	// Completed checks show pass and compact duration
	assert.Contains(t, view, "✓ 2/2")
	assert.Contains(t, view, "4m0s")
	assert.NotContains(t, view, "✓ 2/2 4m0s")
	// Completed failed checks show failure and compact duration
	assert.Contains(t, view, "✗ 1 req failed")
	assert.Contains(t, view, "12m0s")
	assert.NotContains(t, view, "✗ 1 req failed 12m0s")

	// No timestamps check retains standard badge without duration
	mOnly := tui.New(
		model.Queue{Inbox: []model.PullRequest{prNoTimes}},
		tui.WithNow(now),
		tui.WithDimensions(140, 30),
		tui.WithActiveTab(tui.TabInbox),
	)
	viewOnly := mOnly.View()
	assert.Contains(t, viewOnly, "✓ 2/2")
	assert.NotRegexp(t, `✓ 2/2 \d+`, viewOnly)
}
