package tui_test

import (
	"context"
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
