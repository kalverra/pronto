package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/notify"
)

type okNotifier struct{}

func (okNotifier) Notify(context.Context, notify.Notification) error { return nil }

//nolint:paralleltest // mutates the package-level notifierFactory
func TestSendTestNotification_ReportsTerminalFallback(t *testing.T) {
	prev := notifierFactory
	t.Cleanup(func() { notifierFactory = prev })
	notifierFactory = func(config.NotificationConfig) notify.Notifier {
		return notify.NewFallbackNotifier(okNotifier{}, okNotifier{}, notify.WithFallbackReason(notify.ErrDenied))
	}

	var out bytes.Buffer
	require.NoError(t, sendTestNotification(context.Background(), &out, config.NotificationConfig{Popups: true}))
	assert.Contains(t, out.String(), "Sent via terminal fallback")
	assert.Contains(t, out.String(), "System Settings")
}

//nolint:paralleltest // mutates the package-level notifierFactory
func TestSendTestNotification_StaleHelperIsNotFallback(t *testing.T) {
	prev := notifierFactory
	t.Cleanup(func() { notifierFactory = prev })
	notifierFactory = func(config.NotificationConfig) notify.Notifier {
		return notify.NewFallbackNotifier(
			okNotifier{},
			okNotifier{},
			notify.WithFallbackAdvisory(notify.ErrHelperStale),
		)
	}

	var out bytes.Buffer
	require.NoError(t, sendTestNotification(context.Background(), &out, config.NotificationConfig{Popups: true}))
	assert.NotContains(t, out.String(), "fallback")
	assert.Contains(t, out.String(), "Sent.")
}

func TestFormatNotifyReport_NativeHints(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		state      string
		auth       string
		want       []string
		wantAbsent []string
	}{
		{
			name:  "not authorized suggests setup",
			state: "installed", auth: "notDetermined",
			want: []string{"Run: pronto notify setup", "fall back to terminal mode"},
		},
		{
			name:  "denied links System Settings",
			state: "installed", auth: "denied",
			want:       []string{"x-apple.systempreferences", "fall back to terminal mode"},
			wantAbsent: []string{"Run: pronto notify setup"},
		},
		{
			name:  "missing suggests setup",
			state: "missing",
			want:  []string{"Run: pronto notify setup", "fall back to terminal mode"},
		},
		{
			name:  "stale still delivers natively",
			state: "stale", auth: "authorized",
			want:       []string{"Run: pronto notify setup"},
			wantAbsent: []string{"fall back"},
		},
		{
			name:  "healthy",
			state: "installed", auth: "authorized",
			wantAbsent: []string{"Run: pronto notify setup", "fall back"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatNotifyReport(
				notifyReport{mode: config.NotifyNative, helperState: tc.state, authStatus: tc.auth},
			)
			for _, w := range tc.want {
				assert.Contains(t, got, w)
			}
			for _, w := range tc.wantAbsent {
				assert.NotContains(t, got, w)
			}
		})
	}
}
