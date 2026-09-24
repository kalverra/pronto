package tui_test

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/tui"
)

// healthNotifier is a notifier reporting a fixed Health, like
// notify.FallbackNotifier after a native setup problem.
type healthNotifier struct{ health error }

func (healthNotifier) Notify(context.Context, notify.Notification) error { return nil }
func (h healthNotifier) Health() error                                   { return h.health }

func TestModel_View_NotificationHealthHint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		health error
		want   string
	}{
		{notify.ErrHelperNotFound, "pronto notify setup"},
		{notify.ErrNotAuthorized, "pronto notify setup"},
		{notify.ErrDenied, "System Settings"},
		{notify.ErrHelperStale, "pronto notify setup"},
	} {
		t.Run(tc.health.Error(), func(t *testing.T) {
			t.Parallel()
			m := tui.New(model.Queue{}, tui.WithNotifier(healthNotifier{health: tc.health}))
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			assert.Contains(t, updated.(tui.Model).View(), tc.want)
		})
	}
}

func TestModel_View_NoHintWhenNotificationsHealthy(t *testing.T) {
	t.Parallel()

	m := tui.New(model.Queue{}, tui.WithNotifier(healthNotifier{}))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	assert.NotContains(t, updated.(tui.Model).View(), "pronto notify setup")
}
