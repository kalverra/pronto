package tui_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

type focusTestStore struct {
	mu      sync.Mutex
	queue   model.Queue
	keys    []model.PRKey
	savedAt time.Time
	ok      bool
}

func (s *focusTestStore) Identity(context.Context) (cache.Identity, time.Time, bool) {
	return cache.Identity{}, time.Time{}, false
}
func (s *focusTestStore) SaveIdentity(context.Context, cache.Identity) error { return nil }
func (s *focusTestStore) PR(context.Context, string, int) (model.PullRequest, time.Time, bool) {
	return model.PullRequest{}, time.Time{}, false
}

func (s *focusTestStore) SavePR(context.Context, string, int, model.PullRequest) error { return nil }

func (s *focusTestStore) TouchPR(context.Context, string, int) error { return nil }

func (s *focusTestStore) PrunePRs(context.Context, time.Duration) (int, error) { return 0, nil }

func (s *focusTestStore) Queue(context.Context) (model.Queue, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queue, s.savedAt, s.ok
}

func (s *focusTestStore) SaveQueue(_ context.Context, q model.Queue) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = q
	s.savedAt = time.Now()
	s.ok = true
	return nil
}

func (s *focusTestStore) Focus(context.Context) ([]model.PRKey, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys, s.savedAt, s.ok
}

func (s *focusTestStore) SaveFocus(_ context.Context, keys []model.PRKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = keys
	s.savedAt = time.Now()
	s.ok = true
	return nil
}

func TestModel_ToggleFocusKey(t *testing.T) {
	t.Parallel()

	t.Run("toggle focus on inbox PR", func(t *testing.T) {
		t.Parallel()
		q := makeTestQueue()
		m := tui.New(q, tui.WithViewer("kalverra"), tui.WithActiveTab(tui.TabInbox))

		selected := m.SelectedPR()
		require.NotNil(t, selected)
		targetKey := selected.Key()

		assert.False(t, m.IsFocused(targetKey))
		assert.Empty(t, m.FocusItems())
		assert.Contains(t, m.View(), "1: Focus (0)")

		// Press 'f' to focus
		m2, cmd := sendRune(m, 'f')
		model2 := m2.(tui.Model)
		assert.Nil(t, cmd) // no store configured
		assert.True(t, model2.IsFocused(targetKey))
		assert.Len(t, model2.FocusItems(), 1)
		assert.Equal(t, targetKey, model2.FocusItems()[0].PR.Key())
		assert.Contains(t, model2.View(), "1: Focus (1)")

		// Press 'f' again to unfocus
		m3, _ := sendRune(model2, 'f')
		model3 := m3.(tui.Model)
		assert.False(t, model3.IsFocused(targetKey))
		assert.Empty(t, model3.FocusItems())
		assert.Contains(t, model3.View(), "1: Focus (0)")
	})

	t.Run("toggle focus on mine PR", func(t *testing.T) {
		t.Parallel()
		q := makeTestQueue()
		m := tui.New(q, tui.WithViewer("kalverra"), tui.WithActiveTab(tui.TabMine))

		selected := m.SelectedPR()
		require.NotNil(t, selected)
		targetKey := selected.Key()

		assert.False(t, m.IsFocused(targetKey))

		m2, _ := sendRune(m, 'f')
		model2 := m2.(tui.Model)
		assert.True(t, model2.IsFocused(targetKey))
		assert.Len(t, model2.FocusItems(), 1)
		assert.Equal(t, targetKey, model2.FocusItems()[0].PR.Key())
	})

	t.Run("unfocus PR while on focus tab", func(t *testing.T) {
		t.Parallel()
		q := makeTestQueue()
		inboxPR := q.Inbox[0]
		m := tui.New(
			q,
			tui.WithViewer("kalverra"),
			tui.WithActiveTab(tui.TabFocus),
			tui.WithFocusedPRs([]model.PRKey{inboxPR.Key()}),
		)

		assert.Len(t, m.FocusItems(), 1)
		assert.Equal(t, 0, m.Cursor())

		m2, _ := sendRune(m, 'f')
		model2 := m2.(tui.Model)
		assert.Empty(t, model2.FocusItems())
		assert.False(t, model2.IsFocused(inboxPR.Key()))
		assert.Equal(t, 0, model2.Cursor())
		assert.Contains(t, model2.View(), "1: Focus (0)")
	})

	t.Run("remove closed PR clears focus state", func(t *testing.T) {
		t.Parallel()
		q := makeTestQueue()
		inboxPR := q.Inbox[0]
		m := tui.New(
			q,
			tui.WithViewer("kalverra"),
			tui.WithFocusedPRs([]model.PRKey{inboxPR.Key()}),
		)
		assert.True(t, m.IsFocused(inboxPR.Key()))
		assert.Len(t, m.FocusItems(), 1)

		// Close/remove PR via ClosePRMsg
		m2, _ := m.Update(tui.ClosePRMsg{PR: inboxPR})
		model2 := m2.(tui.Model)
		assert.False(t, model2.IsFocused(inboxPR.Key()))
		assert.Empty(t, model2.FocusItems())
	})
}

func TestModel_FocusPersistence(t *testing.T) {
	t.Parallel()

	t.Run("toggle focus triggers async save when store present", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		store := &focusTestStore{}
		q := makeTestQueue()
		m := tui.New(
			q,
			tui.WithViewer("kalverra"),
			tui.WithActiveTab(tui.TabInbox),
			tui.WithStore(store),
		)

		selected := m.SelectedPR()
		require.NotNil(t, selected)
		targetKey := selected.Key()

		m2, cmd := sendRune(m, 'f')
		require.NotNil(t, cmd)

		// Execute cmd returned by Bubbletea
		msg := cmd()
		require.IsType(t, tui.SaveFocusMsg{}, msg)
		require.NoError(t, msg.(tui.SaveFocusMsg).Err)

		// Verify store state
		savedKeys, _, ok := store.Focus(ctx)
		assert.True(t, ok)
		assert.Equal(t, []model.PRKey{targetKey}, savedKeys)

		// Unfocus triggers another save
		m3, cmd2 := sendRune(m2, 'f')
		require.NotNil(t, cmd2)
		msg2 := cmd2()
		require.NoError(t, msg2.(tui.SaveFocusMsg).Err)

		savedKeys2, _, ok2 := store.Focus(ctx)
		assert.True(t, ok2)
		assert.Empty(t, savedKeys2)
		_ = m3
	})

	t.Run("startup model reloads focus keys from store", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		q := makeTestQueue()
		targetKey := q.Inbox[0].Key()
		store := &focusTestStore{
			queue:   q,
			keys:    []model.PRKey{targetKey},
			savedAt: time.Now(),
			ok:      true,
		}

		m := tui.StartupModel(ctx, nil, store, tui.WithViewer("kalverra"))
		assert.True(t, m.IsFocused(targetKey))
		assert.Len(t, m.FocusItems(), 1)
		assert.Equal(t, targetKey, m.FocusItems()[0].PR.Key())
	})
}

func TestModel_FocusedPR_SectionPinning(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	t.Run("inbox tab pins focused PR to top of category section", func(t *testing.T) {
		t.Parallel()

		pr1 := model.PullRequest{
			Number:            101,
			Title:             "Alpha PR One",
			RepoOwner:         "org",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			Author:            "alice",
			Mergeable:         "MERGEABLE",
			MergeStateStatus:  "CLEAN",
			MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
			Checks:            model.ChecksSummary{HasRequiredChecks: true, ReqTotal: 2, ReqDone: 2},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-4 * time.Hour), ReviewerUser: "kalverra"},
			},
		}
		pr2 := model.PullRequest{
			Number:            102,
			Title:             "Beta PR Two",
			RepoOwner:         "org",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			Author:            "bob",
			Mergeable:         "MERGEABLE",
			MergeStateStatus:  "CLEAN",
			MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
			Checks:            model.ChecksSummary{HasRequiredChecks: true, ReqTotal: 2, ReqDone: 2},
			TimelineItems: []model.TimelineItem{
				{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
			},
		}

		q := model.Queue{Inbox: []model.PullRequest{pr1, pr2}}

		// Without focus, pr1 is ranked above pr2 by score
		mUnfocused := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithActiveTab(tui.TabInbox))
		viewUnfocused := mUnfocused.View()
		idxPr1Unfocused := strings.Index(viewUnfocused, "Alpha PR One")
		idxPr2Unfocused := strings.Index(viewUnfocused, "Beta PR Two")
		require.True(t, idxPr1Unfocused >= 0 && idxPr2Unfocused >= 0)
		assert.Less(t, idxPr1Unfocused, idxPr2Unfocused, "originally PR 101 comes before PR 102")

		// With PR 102 focused, PR 102 pins to top of NEEDS YOUR ATTENTION section
		mFocused := tui.New(
			q,
			tui.WithViewer("kalverra"),
			tui.WithNow(now),
			tui.WithActiveTab(tui.TabInbox),
			tui.WithFocusedPRs([]model.PRKey{pr2.Key()}),
		)
		viewFocused := mFocused.View()
		idxDividerFocused := strings.Index(viewFocused, "NEEDS YOUR ATTENTION")
		idxPr1Focused := strings.Index(viewFocused, "Alpha PR One")
		idxPr2Focused := strings.Index(viewFocused, "Beta PR Two")
		require.True(t, idxDividerFocused >= 0 && idxPr1Focused >= 0 && idxPr2Focused >= 0)
		assert.Less(t, idxDividerFocused, idxPr2Focused, "divider must appear before items")
		assert.Less(t, idxPr2Focused, idxPr1Focused, "focused PR 102 must pin above unfocused PR 101 in section")

		// Navigating to top of list selects the pinned focused PR
		mNav, _ := sendRune(mFocused, 'g')
		assert.Equal(t, 102, mNav.(tui.Model).SelectedPR().Number, "top of visible list should be pinned focused PR")
	})

	t.Run("mine tab pins focused PR to top of category section", func(t *testing.T) {
		t.Parallel()

		pr1 := model.PullRequest{
			Number:            201,
			Title:             "Mine Action PR One",
			RepoOwner:         "org",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			Author:            "kalverra",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-2 * time.Hour),
		}
		pr2 := model.PullRequest{
			Number:            202,
			Title:             "Mine Action PR Two",
			RepoOwner:         "org",
			RepoName:          "repo",
			RepoNameWithOwner: "org/repo",
			Author:            "kalverra",
			ReviewDecision:    "CHANGES_REQUESTED",
			UpdatedAt:         now.Add(-1 * time.Hour),
		}

		q := model.Queue{Authored: []model.PullRequest{pr1, pr2}}

		// Without focus, pr1 is ranked above pr2 by score
		mUnfocused := tui.New(q, tui.WithViewer("kalverra"), tui.WithNow(now), tui.WithActiveTab(tui.TabMine))
		viewUnfocused := mUnfocused.View()
		idxPr1Unfocused := strings.Index(viewUnfocused, "Mine Action PR One")
		idxPr2Unfocused := strings.Index(viewUnfocused, "Mine Action PR Two")
		require.True(t, idxPr1Unfocused >= 0 && idxPr2Unfocused >= 0)
		assert.Less(t, idxPr1Unfocused, idxPr2Unfocused, "originally PR 201 comes before PR 202")

		// With PR 202 focused, PR 202 pins to top of ACTION REQUIRED section
		mFocused := tui.New(
			q,
			tui.WithViewer("kalverra"),
			tui.WithNow(now),
			tui.WithActiveTab(tui.TabMine),
			tui.WithFocusedPRs([]model.PRKey{pr2.Key()}),
		)
		viewFocused := mFocused.View()
		idxDividerFocused := strings.Index(viewFocused, "ACTION REQUIRED")
		idxPr1Focused := strings.Index(viewFocused, "Mine Action PR One")
		idxPr2Focused := strings.Index(viewFocused, "Mine Action PR Two")
		require.True(t, idxDividerFocused >= 0 && idxPr1Focused >= 0 && idxPr2Focused >= 0)
		assert.Less(t, idxDividerFocused, idxPr2Focused, "divider must appear before items")
		assert.Less(t, idxPr2Focused, idxPr1Focused, "focused PR 202 must pin above unfocused PR 201 in section")

		// Navigating to top of list selects the pinned focused PR
		mNav, _ := sendRune(mFocused, 'g')
		assert.Equal(t, 202, mNav.(tui.Model).SelectedPR().Number, "top of visible list should be pinned focused PR")
	})

	t.Run("stably preserves score order within focused and unfocused partitions", func(t *testing.T) {
		t.Parallel()

		makePR := func(num int, title string, hoursAgo int) model.PullRequest {
			return model.PullRequest{
				Number:            num,
				Title:             title,
				RepoOwner:         "org",
				RepoName:          "repo",
				RepoNameWithOwner: "org/repo",
				Author:            "alice",
				Mergeable:         "MERGEABLE",
				MergeStateStatus:  "CLEAN",
				MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
				Checks:            model.ChecksSummary{HasRequiredChecks: true, ReqTotal: 2, ReqDone: 2},
				TimelineItems: []model.TimelineItem{
					{
						Type:         model.TimelineItemReviewRequested,
						CreatedAt:    now.Add(-time.Duration(hoursAgo) * time.Hour),
						ReviewerUser: "kalverra",
					},
				},
			}
		}

		pr1 := makePR(101, "PR Rank 1", 6)
		pr2 := makePR(102, "PR Rank 2", 4)
		pr3 := makePR(103, "PR Rank 3", 2)
		pr4 := makePR(104, "PR Rank 4", 1)

		q := model.Queue{Inbox: []model.PullRequest{pr1, pr2, pr3, pr4}}

		// Focus PR 2 and PR 4 (both in CategoryAttention)
		mFocused := tui.New(
			q,
			tui.WithViewer("kalverra"),
			tui.WithNow(now),
			tui.WithActiveTab(tui.TabInbox),
			tui.WithFocusedPRs([]model.PRKey{pr2.Key(), pr4.Key()}),
		)

		view := mFocused.View()
		idx1 := strings.Index(view, "PR Rank 1")
		idx2 := strings.Index(view, "PR Rank 2")
		idx3 := strings.Index(view, "PR Rank 3")
		idx4 := strings.Index(view, "PR Rank 4")

		require.True(t, idx1 >= 0 && idx2 >= 0 && idx3 >= 0 && idx4 >= 0)
		// Focused items (2, 4) appear before unfocused items (1, 3)
		assert.Less(t, idx2, idx4, "focused PR Rank 2 should precede focused PR Rank 4")
		assert.Less(t, idx4, idx1, "focused PR Rank 4 should precede unfocused PR Rank 1")
		assert.Less(t, idx1, idx3, "unfocused PR Rank 1 should precede unfocused PR Rank 3")
	})
}

func TestModel_FocusedPR_StarPrefix(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	pr1 := model.PullRequest{
		Number:            101,
		Title:             "Alpha PR One",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
		Checks:            model.ChecksSummary{HasRequiredChecks: true, ReqTotal: 2, ReqDone: 2},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-4 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	pr2 := model.PullRequest{
		Number:            102,
		Title:             "Beta PR Two",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Author:            "bob",
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
		Checks:            model.ChecksSummary{HasRequiredChecks: true, ReqTotal: 2, ReqDone: 2},
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: now.Add(-1 * time.Hour), ReviewerUser: "kalverra"},
		},
	}
	q := model.Queue{Inbox: []model.PullRequest{pr1, pr2}}

	m := tui.New(
		q,
		tui.WithViewer("kalverra"),
		tui.WithNow(now),
		tui.WithActiveTab(tui.TabInbox),
		tui.WithFocusedPRs([]model.PRKey{pr2.Key()}),
	)

	view := m.View()

	// Focused PR has star prefix
	assert.Contains(t, view, "★ Beta PR Two")
	// Unfocused PR does not have star prefix
	assert.NotContains(t, view, "★ Alpha PR One")
	assert.Contains(t, view, "Alpha PR One")

	// Help bar displays 'f: focus'
	assert.Contains(t, view, "f: focus")
}
