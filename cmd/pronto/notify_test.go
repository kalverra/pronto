package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/logging"
	"github.com/kalverra/pronto/internal/notify"
)

type recordingNotifier struct {
	mu   sync.Mutex
	got  []notify.Notification
	fail error
}

func (r *recordingNotifier) Notify(_ context.Context, n notify.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, n)
	return r.fail
}

func (r *recordingNotifier) notes() []notify.Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notify.Notification(nil), r.got...)
}

func stubNotifierFactory(t *testing.T, n notify.Notifier) *config.NotificationConfig {
	t.Helper()
	var seen config.NotificationConfig
	old := notifierFactory
	notifierFactory = func(cfg config.NotificationConfig) notify.Notifier {
		seen = cfg
		return n
	}
	t.Cleanup(func() { notifierFactory = old })
	return &seen
}

func TestRun_Notify_ReportsConfigAndBackends(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(), []string{"notify"}, nil, &stdout, &stderr))

	out := stdout.String()
	assert.Contains(t, out, "Popups:")
	assert.Contains(t, out, "enabled", "popups default to enabled")
	assert.Contains(t, out, "osascript", "fallback backend must be reported")
	assert.Contains(t, out, logging.LogPath(), "must point at the log that records delivery failures")
	assert.Contains(t, out, "pronto notify --test")
}

func TestRun_Notify_ReportsMissingAssetPaths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	missing := filepath.Join(dir, "nope.wav")
	cfgPath := filepath.Join(dir, "pronto.toml")
	cfgBody := "[notifications]\nsound = true\n\n[notifications.sounds]\nci_failed = \"" + missing + "\"\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgBody), 0o600))

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(), []string{"notify"}, nil, &stdout, &stderr))

	out := stdout.String()
	assert.Contains(t, out, "ci_failed")
	assert.Contains(t, out, "missing", "a configured sound file that does not exist must be flagged")
}

func TestRun_NotifyTest_DeliversThroughConfiguredNotifier(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)

	notifier := &recordingNotifier{}
	seen := stubNotifierFactory(t, notifier)

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(), []string{"notify", "--test"}, nil, &stdout, &stderr))

	assert.True(t, seen.Popups, "test must build the notifier from user config")
	notes := notifier.notes()
	require.Len(t, notes, 1, "--test must deliver exactly one notification")
	assert.NotEmpty(t, notes[0].Title)
	assert.NotEmpty(t, notes[0].Message)
	assert.Contains(t, notes[0].ImagePath, "ci-passed.png", "--test must attach default image when unconfigured")
	assert.Contains(t, stdout.String(), "Sent")
}

func TestRun_NotifyTest_DeliveryFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)

	stubNotifierFactory(t, &recordingNotifier{fail: assert.AnError})

	var stdout, stderr syncBuffer
	err := Run(context.Background(), []string{"notify", "--test"}, nil, &stdout, &stderr)
	require.Error(t, err, "a failed test notification must exit non-zero")
	assert.Contains(t, err.Error(), assert.AnError.Error())
}

func TestRun_NotifyTest_NoChannelsEnabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("PRONTO_NOTIFICATIONS_POPUPS", "false")

	var stdout, stderr syncBuffer
	err := Run(context.Background(), []string{"notify", "--test"}, nil, &stdout, &stderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no notification channels enabled")
}
