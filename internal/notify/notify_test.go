package notify_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/notify"
)

func TestMultiNotifier(t *testing.T) {
	t.Parallel()

	var notif1, notif2 []notify.Notification
	mock1 := &mockTestNotifier{onNotify: func(n notify.Notification) { notif1 = append(notif1, n) }}
	mock2 := &mockTestNotifier{onNotify: func(n notify.Notification) { notif2 = append(notif2, n) }}

	multi := notify.MultiNotifier{mock1, mock2}
	n := notify.Notification{Trigger: notify.TriggerPRMerged, Title: "Merged"}

	err := multi.Notify(context.Background(), n)
	require.NoError(t, err)
	assert.Len(t, notif1, 1)
	assert.Len(t, notif2, 1)
}

type mockTestNotifier struct {
	onNotify func(n notify.Notification)
}

func (m *mockTestNotifier) Notify(_ context.Context, n notify.Notification) error {
	if m.onNotify != nil {
		m.onNotify(n)
	}
	return nil
}

func TestNewNotifier_TerminalMode(t *testing.T) {
	t.Parallel()

	notifier := notify.NewNotifier(notify.Options{Mode: notify.ModeTerminal})
	assert.IsType(t, &notify.MacNotifier{}, notifier)
}

func TestNewNotifier_NativeModeWithHelper(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native mode is macOS-only")
	}
	t.Setenv("PRONTO_NOTIFY_HELPER", "/custom/helper")

	notifier := notify.NewNotifier(notify.Options{Mode: notify.ModeNative})
	require.IsType(t, &notify.FallbackNotifier{}, notifier,
		"native delivery must fall back to terminal when the helper is not authorized")
	assert.NoError(t, notifier.(*notify.FallbackNotifier).Health())
}

func TestNewNotifier_NativeModeWithoutHelperFallsBackToTerminal(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native mode is macOS-only")
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	notifier := notify.NewNotifier(notify.Options{Mode: notify.ModeNative})
	require.IsType(t, &notify.FallbackNotifier{}, notifier)
	assert.ErrorIs(t, notifier.(*notify.FallbackNotifier).Health(), notify.ErrHelperNotFound,
		"a missing helper must be reported so the TUI can suggest setup")
}

func TestNewNotifier_NativeModeOffMacOSUsesTerminal(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("covers non-macOS hosts")
	}
	t.Parallel()

	notifier := notify.NewNotifier(notify.Options{Mode: notify.ModeNative})
	assert.IsType(t, &notify.MacNotifier{}, notifier)
}

func TestNewNotifier_UnknownMode(t *testing.T) {
	t.Parallel()

	notifier := notify.NewNotifier(notify.Options{Mode: "carrier-pigeon"})
	assert.IsType(t, &notify.MacNotifier{}, notifier)
}
