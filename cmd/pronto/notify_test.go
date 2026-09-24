package main

import (
	"context"
	"errors"
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

// stubSetupSeams replaces the setup command's install/register/helper-mode
// hooks so tests never compile Swift, touch LaunchServices, or request a real
// notification permission. The notifier is stubbed too so setup's test banner
// never reaches the real desktop; tests may override it afterwards.
func stubSetupSeams(t *testing.T, status string, installErr, registerErr, statusErr error) *bool {
	t.Helper()
	registered := false
	stubNotifierFactory(t, &recordingNotifier{})

	oldInstall, oldRegister, oldMode := installNativeHelperFn, registerHelperFn, helperModeFn
	installNativeHelperFn = func(appPath string, _ ...notify.InstallOption) (string, error) {
		if installErr != nil {
			return "", installErr
		}
		require.NoError(t, os.MkdirAll(filepath.Join(appPath, "Contents", "MacOS"), 0o750))
		binary := filepath.Join(appPath, "Contents", "MacOS", notify.HelperBinaryName)
		require.NoError(t, os.WriteFile(binary, []byte("fake"), 0o600))
		// #nosec G302 -- discovery requires the executable bit; test-only fixture.
		require.NoError(t, os.Chmod(binary, 0o700))
		require.NoError(t, os.WriteFile(
			filepath.Join(appPath, "Contents", "Info.plist"),
			[]byte("<plist><key>CFBundleIdentifier</key><string>"+notify.HelperBundleID+"</string>"+
				"<key>CFBundleVersion</key><string>"+notify.HelperVersion()+"</string></plist>"), 0o600))
		return appPath, nil
	}
	registerHelperFn = func(context.Context, string, ...notify.InstallOption) error {
		registered = true
		return registerErr
	}
	helperModeFn = func(context.Context, string, string) (helperStatus, error) {
		if statusErr != nil {
			return helperStatus{}, statusErr
		}
		return helperStatus{Status: status}, nil
	}
	t.Cleanup(func() {
		installNativeHelperFn, registerHelperFn, helperModeFn = oldInstall, oldRegister, oldMode
	})
	return &registered
}

func TestRun_Notify_ReportsConfigAndBackends(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	// Isolate from any native helper actually installed on the host: this
	// test is about the report shape, not live LaunchServices/XPC state.
	t.Setenv("XDG_DATA_HOME", dir)

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(), []string{"notify"}, nil, &stdout, &stderr))

	out := stdout.String()
	assert.Contains(t, out, "Mode:")
	assert.Contains(t, out, "Popups:")
	assert.Contains(t, out, "enabled", "popups default to enabled")
	assert.Contains(t, out, "osascript", "fallback backend must be reported")
	assert.Contains(t, out, "native helper", "native backend must be reported")
	assert.NotContains(t, out, "afplay", "afplay backend was removed")
	assert.Contains(t, out, logging.LogPath(), "must point at the log that records delivery failures")
	assert.Contains(t, out, "pronto notify --test")
	assert.Contains(t, out, "\"Pronto\"", "allow-list hint must use the app's display name, not the binary name")
}

func TestRun_Notify_ReportsMissingAssetPaths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	missing := filepath.Join(dir, "nope.png")
	cfgPath := filepath.Join(dir, "pronto.toml")
	cfgBody := "[notifications]\npopups = true\n\n[notifications.images]\nci_failed = \"" + missing + "\"\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgBody), 0o600))

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(), []string{"notify"}, nil, &stdout, &stderr))

	out := stdout.String()
	assert.Contains(t, out, "ci_failed")
	assert.Contains(t, out, "missing", "a configured image file that does not exist must be flagged")
}

func TestRun_NotifyTest_DeliversThroughConfiguredNotifier(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", dir)

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
	t.Setenv("XDG_DATA_HOME", dir)

	stubNotifierFactory(t, &recordingNotifier{fail: assert.AnError})

	var stdout, stderr syncBuffer
	err := Run(context.Background(), []string{"notify", "--test"}, nil, &stdout, &stderr)
	require.Error(t, err, "a failed test notification must exit non-zero")
	assert.Contains(t, err.Error(), assert.AnError.Error())
}

func TestRun_NotifyTest_NoChannelsEnabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("PRONTO_NOTIFICATIONS_POPUPS", "false")

	var stdout, stderr syncBuffer
	err := Run(context.Background(), []string{"notify", "--test"}, nil, &stdout, &stderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no notification channels enabled")
}

func TestRun_NotifySetup_BuildsInstallsRegistersAndAuthorizes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dest := filepath.Join(dir, "ProntoNotify.app")

	// Registration for a fresh build happens inside the real InstallNativeHelper,
	// not through the separate registerHelperFn seam (that seam only fires when
	// the up-to-date helper is skipping a rebuild); see the next test.
	stubSetupSeams(t, "authorized", nil, nil, nil)
	notifier := &recordingNotifier{}
	stubNotifierFactory(t, notifier)

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(),
		[]string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr))

	out := stdout.String()
	assert.Contains(t, out, "macOS detected")
	assert.Contains(t, out, "built and installed")
	assert.Contains(t, out, "registered with LaunchServices")
	assert.Contains(t, out, "notifications authorized")
	assert.Contains(t, out, "Sent")
	assert.Len(t, notifier.notes(), 1, "setup must send a real test notification")

	info, err := os.Stat(filepath.Join(dest, "Contents", "MacOS", notify.HelperBinaryName))
	require.NoError(t, err)
	assert.False(t, info.IsDir())
}

func TestRun_NotifySetup_SkipsRebuildWhenUpToDate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dest := filepath.Join(dir, "ProntoNotify.app")

	registered := stubSetupSeams(t, "authorized", nil, nil, nil)
	stubNotifierFactory(t, &recordingNotifier{})

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(),
		[]string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr))
	require.Contains(t, stdout.String(), "built and installed")

	*registered = false
	stdout = syncBuffer{}
	require.NoError(t, Run(context.Background(),
		[]string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr))
	assert.Contains(t, stdout.String(), "already up to date")
	assert.NotContains(t, stdout.String(), "built and installed")
	assert.True(t, *registered, "an up-to-date helper must still be (re-)registered")
}

func TestRun_NotifySetup_RebuildsWhenBundleIDDiffers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dest := filepath.Join(dir, "ProntoNotify.app")

	// Current version, but built as a dev bundle: not the requested helper.
	require.NoError(t, os.MkdirAll(filepath.Join(dest, "Contents"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "Contents", "Info.plist"),
		[]byte("<key>CFBundleIdentifier</key><string>"+notify.DevHelperBundleID+"</string>"+
			"<key>CFBundleVersion</key><string>"+notify.HelperVersion()+"</string>"), 0o600))

	stubSetupSeams(t, "authorized", nil, nil, nil)

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(),
		[]string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr))
	assert.Contains(t, stdout.String(), "built and installed")
}

func TestRun_NotifySetup_RegisterFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dest := filepath.Join(dir, "ProntoNotify.app")

	stubSetupSeams(t, "authorized", nil, nil, nil)
	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(),
		[]string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr))

	// Re-run against the now up-to-date helper, but with registration failing.
	stubSetupSeams(t, "authorized", nil, errors.New("lsregister boom"), nil)
	stdout = syncBuffer{}
	err := Run(context.Background(), []string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lsregister boom")
}

func TestRun_NotifySetup_WarnsAboutStaleInstalls(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	dest := filepath.Join(dir, "ProntoNotify.app")

	// A stale install at the conventional data dir, distinct from --output,
	// so setup has something to warn about.
	staleApp := filepath.Join(xdg, "pronto", "ProntoNotify.app")
	require.NoError(t, os.MkdirAll(filepath.Join(staleApp, "Contents"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(staleApp, "Contents", "Info.plist"),
		[]byte("<key>CFBundleIdentifier</key><string>"+notify.HelperBundleID+"</string>"), 0o600))

	stubSetupSeams(t, "authorized", nil, nil, nil)
	stubNotifierFactory(t, &recordingNotifier{})

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(),
		[]string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr))

	out := stdout.String()
	assert.Contains(t, out, "killall usernoted NotificationCenter")
	assert.Contains(t, out, staleApp)
}

func TestRun_NotifySetup_NoRegisterSkipsAuthorizeAndTest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	dest := filepath.Join(dir, "ProntoNotify.app")

	stubSetupSeams(t, "authorized", nil, nil, nil)

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(),
		[]string{"notify", "setup", "--output", dest, "--no-register"}, nil, &stdout, &stderr))

	out := stdout.String()
	assert.Contains(t, out, "registration skipped")
	assert.NotContains(t, out, "registered with LaunchServices")
	assert.NotContains(t, out, "authorized")
}

// Right after install, usernoted may reject a helper process before its
// prompt can show (status stays notDetermined); setup must respawn --authorize.
func TestRun_NotifySetup_RetriesAuthorizeWhileNotDetermined(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dest := filepath.Join(dir, "ProntoNotify.app")

	stubSetupSeams(t, "authorized", nil, nil, nil)
	oldDelay := authorizeRetryDelay
	authorizeRetryDelay = 0
	calls := 0
	helperModeFn = func(context.Context, string, string) (helperStatus, error) {
		calls++
		if calls < 3 {
			return helperStatus{Status: "notDetermined"}, nil
		}
		return helperStatus{Status: "authorized"}, nil
	}
	t.Cleanup(func() { authorizeRetryDelay = oldDelay })

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(),
		[]string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr))
	assert.Contains(t, stdout.String(), "notifications authorized")
	assert.Equal(t, 3, calls)
}

func TestRun_NotifySetup_DeniedAuthorizationPrintsDeepLink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dest := filepath.Join(dir, "ProntoNotify.app")

	stubSetupSeams(t, "denied", nil, nil, nil)
	stubNotifierFactory(t, &recordingNotifier{})

	var stdout, stderr syncBuffer
	require.NoError(t, Run(context.Background(),
		[]string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr))

	assert.Contains(t, stdout.String(), "x-apple.systempreferences:com.apple.Notifications-Settings.extension")
}

func TestRun_NotifySetup_BuildFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dest := filepath.Join(dir, "ProntoNotify.app")

	stubSetupSeams(t, "authorized", errors.New("compile boom"), nil, nil)

	var stdout, stderr syncBuffer
	err := Run(context.Background(), []string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compile boom")
}

func TestRun_NotifySetup_AuthorizeFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dest := filepath.Join(dir, "ProntoNotify.app")

	stubSetupSeams(t, "authorized", nil, nil, errors.New("helper crashed"))

	var stdout, stderr syncBuffer
	err := Run(context.Background(), []string{"notify", "setup", "--output", dest}, nil, &stdout, &stderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "helper crashed")
}

func TestRun_NotifySetup_MissingSwiftc(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	t.Setenv("PATH", "")

	var stdout, stderr syncBuffer
	err := Run(context.Background(),
		[]string{"notify", "setup", "--output", filepath.Join(dir, "ProntoNotify.app")},
		nil, &stdout, &stderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "xcode-select")
}
