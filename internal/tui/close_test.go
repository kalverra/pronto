package tui_test

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

func TestModel_CloseStalePR_NonStaleIgnored(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:    101,
				Title:     "Active PR",
				UpdatedAt: now.Add(-1 * time.Hour),
			},
		},
	}

	var closerCalled bool
	closer := func(_ context.Context, _ model.PullRequest, _ string) error {
		closerCalled = true
		return nil
	}

	m := tui.New(q, tui.WithNow(now), tui.WithCloser(closer))

	m2, cmd := sendRune(m, 'x')
	assert.Nil(t, cmd)
	model2 := m2.(tui.Model)
	assert.Nil(t, model2.ConfirmingClosePR())
	assert.False(t, model2.IsClosing())
	assert.False(t, closerCalled)
}

func TestModel_CloseStalePR_EmptyQueue(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{}
	m := tui.New(q, tui.WithNow(now))

	m2, cmd := sendRune(m, 'x')
	assert.Nil(t, cmd)
	model2 := m2.(tui.Model)
	assert.Nil(t, model2.ConfirmingClosePR())
	assert.False(t, model2.IsClosing())
}

func TestModel_CloseStalePR_ConfirmationPrompt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	stalePR := model.PullRequest{
		Number:            101,
		Title:             "Old feature PR",
		RepoNameWithOwner: "org/repo",
		UpdatedAt:         now.Add(-40 * 24 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{stalePR},
	}

	m := tui.New(q, tui.WithNow(now), tui.WithActiveTab(tui.TabInbox))

	m2, cmd := sendRune(m, 'x')
	assert.Nil(t, cmd)
	model2 := m2.(tui.Model)
	require.NotNil(t, model2.ConfirmingClosePR())
	assert.Equal(t, 101, model2.ConfirmingClosePR().Number)

	view := model2.View()
	assert.Contains(t, view, "Close #101 as stale?")
}

func TestModel_CloseStalePR_CancelConfirmation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	q := model.Queue{
		Inbox: []model.PullRequest{
			{
				Number:    101,
				Title:     "Old feature PR",
				UpdatedAt: now.Add(-40 * 24 * time.Hour),
			},
		},
	}

	var closerCalled bool
	closer := func(_ context.Context, _ model.PullRequest, _ string) error {
		closerCalled = true
		return nil
	}

	m := tui.New(q, tui.WithNow(now), tui.WithCloser(closer), tui.WithActiveTab(tui.TabInbox))

	m2, _ := sendRune(m, 'x')
	model2 := m2.(tui.Model)
	require.NotNil(t, model2.ConfirmingClosePR())

	// Cancel with 'n'
	m3, cmd := sendRune(model2, 'n')
	assert.Nil(t, cmd)
	model3 := m3.(tui.Model)
	assert.Nil(t, model3.ConfirmingClosePR())
	assert.False(t, closerCalled)

	// Trigger confirmation again and cancel with 'esc'
	m4, _ := sendRune(model3, 'x')
	model4 := m4.(tui.Model)
	require.NotNil(t, model4.ConfirmingClosePR())

	m5, cmdEsc := sendKey(model4, tea.KeyEsc)
	assert.Nil(t, cmdEsc)
	model5 := m5.(tui.Model)
	assert.Nil(t, model5.ConfirmingClosePR())
	assert.False(t, closerCalled)
}

func TestModel_CloseStalePR_ConfirmAndExecute(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	stalePR := model.PullRequest{
		Number:            101,
		Title:             "Old feature PR",
		URL:               "https://github.com/org/repo/pull/101",
		RepoNameWithOwner: "org/repo",
		UpdatedAt:         now.Add(-40 * 24 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{stalePR},
	}

	var capturedPR model.PullRequest
	var capturedComment string
	closer := func(_ context.Context, pr model.PullRequest, comment string) error {
		capturedPR = pr
		capturedComment = comment
		return nil
	}

	m := tui.New(q, tui.WithNow(now), tui.WithCloser(closer), tui.WithActiveTab(tui.TabInbox))

	m2, _ := sendRune(m, 'x')
	model2 := m2.(tui.Model)
	require.NotNil(t, model2.ConfirmingClosePR())

	m3, cmd := sendRune(model2, 'y')
	require.NotNil(t, cmd)
	model3 := m3.(tui.Model)
	assert.Nil(t, model3.ConfirmingClosePR())
	assert.True(t, model3.IsClosing())

	assert.Contains(t, model3.View(), "closing #101 as stale…")

	msg := cmd()
	require.IsType(t, tui.ClosePRMsg{}, msg)
	closeMsg := msg.(tui.ClosePRMsg)
	require.NoError(t, closeMsg.Err)
	assert.Equal(t, 101, closeMsg.PR.Number)
	assert.Equal(t, "Closing PR as stale", capturedComment)
	assert.Equal(t, stalePR.URL, capturedPR.URL)

	m4, _ := model3.Update(closeMsg)
	model4 := m4.(tui.Model)
	assert.False(t, model4.IsClosing())
	assert.Empty(t, model4.InboxItems())
	assert.Contains(t, model4.View(), "closed #101 as stale")
}

func TestModel_CloseStalePR_ErrorHandling(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	stalePR := model.PullRequest{
		Number:    101,
		Title:     "Old feature PR",
		URL:       "https://github.com/org/repo/pull/101",
		UpdatedAt: now.Add(-40 * 24 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{stalePR},
	}

	closer := func(_ context.Context, _ model.PullRequest, _ string) error {
		return errors.New("GraphQL: Resource not accessible by integration")
	}

	m := tui.New(q, tui.WithNow(now), tui.WithCloser(closer), tui.WithActiveTab(tui.TabInbox))

	m2, _ := sendRune(m, 'x')
	m3, cmd := sendRune(m2, 'y')
	require.NotNil(t, cmd)

	msg := cmd()
	m4, _ := m3.Update(msg)
	model4 := m4.(tui.Model)

	assert.False(t, model4.IsClosing())
	require.Error(t, model4.CloseErr())
	assert.Contains(t, model4.CloseErr().Error(), "Resource not accessible")
	require.Len(t, model4.InboxItems(), 1)
	assert.Contains(t, model4.View(), "failed to close #101")
}

func TestModel_CloseStalePR_MineTab(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	stalePR := model.PullRequest{
		Number:    201,
		Title:     "My old branch",
		URL:       "https://github.com/org/repo/pull/201",
		UpdatedAt: now.Add(-45 * 24 * time.Hour),
	}
	q := model.Queue{
		Authored: []model.PullRequest{stalePR},
	}

	var capturedComment string
	closer := func(_ context.Context, _ model.PullRequest, comment string) error {
		capturedComment = comment
		return nil
	}

	m := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithCloser(closer))

	mMine, _ := sendKey(m, tea.KeyTab)
	modelMine := mMine.(tui.Model)
	assert.Equal(t, tui.TabMine, modelMine.ActiveTab())

	m2, _ := sendRune(modelMine, 'x')
	model2 := m2.(tui.Model)
	require.NotNil(t, model2.ConfirmingClosePR())
	assert.Equal(t, 201, model2.ConfirmingClosePR().Number)

	m3, cmd := sendRune(model2, 'y')
	require.NotNil(t, cmd)
	msg := cmd()
	m4, _ := m3.Update(msg)
	model4 := m4.(tui.Model)

	assert.Empty(t, model4.MineItems())
	assert.Equal(t, "Closing PR as stale", capturedComment)
}

func TestModel_HelpBar_ShowsCloseStaleKey(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	stalePR := model.PullRequest{
		Number:    101,
		Title:     "Old feature PR",
		UpdatedAt: now.Add(-40 * 24 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{stalePR},
	}

	m := tui.New(q, tui.WithNow(now), tui.WithDimensions(120, 30))
	view := m.View()
	assert.Contains(t, view, "x: close stale")
}

func TestModel_CloseStalePR_BannersClearOnNavigation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	stalePR := model.PullRequest{
		Number:    101,
		Title:     "Old feature PR",
		URL:       "https://github.com/org/repo/pull/101",
		UpdatedAt: now.Add(-40 * 24 * time.Hour),
	}
	q := model.Queue{
		Inbox: []model.PullRequest{stalePR},
	}

	closer := func(_ context.Context, _ model.PullRequest, _ string) error {
		return nil
	}

	m := tui.New(q, tui.WithNow(now), tui.WithCloser(closer), tui.WithActiveTab(tui.TabInbox))

	m2, _ := sendRune(m, 'x')
	m3, cmd := sendRune(m2.(tui.Model), 'y')
	require.NotNil(t, cmd)
	msg := cmd()
	m4, _ := m3.(tui.Model).Update(msg)
	successModel := m4.(tui.Model)
	assert.Contains(t, successModel.View(), "closed #101 as stale")

	// Any navigation key acknowledges the success banner.
	m5, _ := sendKey(successModel, tea.KeyDown)
	navigated := m5.(tui.Model)
	assert.NotContains(t, navigated.View(), "closed #101 as stale")

	// The error banner clears on navigation too.
	failCloser := func(_ context.Context, _ model.PullRequest, _ string) error {
		return errors.New("boom")
	}
	em := tui.New(q, tui.WithNow(now), tui.WithCloser(failCloser), tui.WithActiveTab(tui.TabInbox))
	em2, _ := sendRune(em, 'x')
	em3, cmd := sendRune(em2.(tui.Model), 'y')
	require.NotNil(t, cmd)
	emsg := cmd()
	em4, _ := em3.(tui.Model).Update(emsg)
	errModel := em4.(tui.Model)
	assert.Contains(t, errModel.View(), "failed to close #101")

	em5, _ := sendKey(errModel, tea.KeyDown)
	errNavigated := em5.(tui.Model)
	assert.NotContains(t, errNavigated.View(), "failed to close #101")
}
