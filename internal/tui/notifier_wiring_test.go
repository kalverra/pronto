package tui_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/tui"
)

type failingNotifier struct {
	calls int
}

func (f *failingNotifier) Notify(context.Context, notify.Notification) error {
	f.calls++
	return assert.AnError
}

// drainNotification runs cmd, unwrapping tea batches, and returns the first
// NotificationMsg produced.
func drainNotification(t *testing.T, cmd tea.Cmd) (tui.NotificationMsg, bool) {
	t.Helper()
	if cmd == nil {
		return tui.NotificationMsg{}, false
	}
	msg := cmd()
	if n, ok := msg.(tui.NotificationMsg); ok {
		return n, true
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if n, ok := c().(tui.NotificationMsg); ok {
				return n, true
			}
		}
	}
	return tui.NotificationMsg{}, false
}

// In daemon mode the daemon owns change detection, but the TUI is the only
// component with a desktop notifier, so trigger events must still be delivered.
func TestStartupModel_DaemonSource_DeliversTriggerEventToNotifier(t *testing.T) {
	t.Parallel()

	notifier := &mockNotifier{}
	var factoryCfg config.NotificationConfig

	m := tui.StartupModel(
		context.Background(),
		&fakeDaemonSource{q: model.Queue{}},
		nil,
		tui.WithNotificationConfig(config.NotificationConfig{Popups: true}),
		tui.WithNotifierFactory(func(cfg config.NotificationConfig) notify.Notifier {
			factoryCfg = cfg
			return notifier
		}),
	)

	ev := events.Event{
		Type:  events.TypeCIPassed,
		Repo:  "kalverra/pronto",
		PR:    7,
		Title: "Fix auth middleware",
	}
	_, cmd := m.Update(tui.EventMsg{Event: ev})

	notifMsg, ok := drainNotification(t, cmd)
	require.True(t, ok, "trigger event must dispatch a notification command")
	require.Len(t, notifMsg.Notifications, 1)

	assert.True(t, factoryCfg.Popups, "notifier factory must receive user notification config")
	notes := notifier.getNotes()
	require.Len(t, notes, 1, "daemon trigger event must reach the desktop notifier")
	assert.Equal(t, notify.TriggerCIPassed, notes[0].Trigger)
	assert.Equal(t, "https://github.com/kalverra/pronto/pull/7", notes[0].URL)
}

func TestStartupModel_DaemonSource_AttachesDefaultImage(t *testing.T) {
	t.Parallel()

	notifier := &mockNotifier{}
	m := tui.StartupModel(
		context.Background(),
		&fakeDaemonSource{q: model.Queue{}},
		nil,
		tui.WithNotificationConfig(config.NotificationConfig{Popups: true}),
		tui.WithNotifier(notifier),
	)

	ev := events.Event{
		Type:  events.TypeCIPassed,
		Repo:  "kalverra/pronto",
		PR:    7,
		Title: "Fix auth middleware",
	}
	_, cmd := m.Update(tui.EventMsg{Event: ev})

	notifMsg, ok := drainNotification(t, cmd)
	require.True(t, ok)
	require.Len(t, notifMsg.Notifications, 1)
	assert.Contains(t, notifMsg.Notifications[0].ImagePath, "ci-passed.png")

	notes := notifier.getNotes()
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0].ImagePath, "ci-passed.png")
}

func TestDefaultNotifierFactory(t *testing.T) {
	t.Parallel()

	assert.Nil(
		t,
		tui.DefaultNotifierFactory(config.NotificationConfig{Popups: false, Sound: false}),
		"all channels disabled must yield no notifier, not an empty one",
	)
	assert.NotNil(t, tui.DefaultNotifierFactory(config.NotificationConfig{Popups: true}))
	assert.Nil(
		t,
		tui.DefaultNotifierFactory(config.NotificationConfig{Popups: false, Sound: true}),
		"sound has no independent channel: without popups there is nothing to attach it to",
	)
}

func TestModel_NotificationDeliveryFailure_IsLogged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	notifier := &failingNotifier{}

	m := tui.New(
		model.Queue{},
		tui.WithNotifier(notifier),
		tui.WithLogger(zerolog.New(&buf)),
	)

	ev := events.Event{
		Type:  events.TypeCIFailed,
		Repo:  "kalverra/pronto",
		PR:    9,
		Title: "Broken",
	}
	_, cmd := m.Update(tui.EventMsg{Event: ev})

	_, ok := drainNotification(t, cmd)
	require.True(t, ok)
	assert.Equal(t, 1, notifier.calls)
	assert.Contains(t, buf.String(), "delivering notification failed")
	assert.Contains(t, strings.ToLower(buf.String()), "error")
}
