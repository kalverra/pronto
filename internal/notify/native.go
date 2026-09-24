package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// HelperAppName is the app bundle (and executable) name the native helper is
// installed under.
const HelperAppName = "ProntoNotify.app"

// HelperBinaryName is the executable name inside the helper app bundle.
const HelperBinaryName = "pronto-notify"

// Supported desktop notification delivery modes.
const (
	// ModeTerminal delivers notifications via terminal-notifier/osascript.
	ModeTerminal = "terminal"
	// ModeNative delivers notifications via the bundled UserNotifications
	// helper app.
	ModeNative = "native"
)

// helperEnvOverride names an explicit helper binary, bypassing discovery.
const helperEnvOverride = "PRONTO_NOTIFY_HELPER"

// Native delivery setup errors. FallbackNotifier falls back to terminal
// delivery on any of them; each message tells the user how to fix it.
var (
	// ErrHelperNotFound means the helper app is not installed.
	ErrHelperNotFound = errors.New("native notification helper not found; run 'pronto notify setup'")
	// ErrNotAuthorized means the helper has not been granted permission yet
	// (or usernoted rejected it right after a rebuild).
	ErrNotAuthorized = errors.New("native notifications not authorized; run 'pronto notify setup'")
	// ErrDenied means the user turned Pronto notifications off.
	ErrDenied = errors.New(
		"native notifications denied; allow Pronto in System Settings > Notifications",
	)
	// ErrHelperStale means the installed helper predates this pronto build.
	// It still delivers; it is advisory only.
	ErrHelperStale = errors.New("native notification helper is out of date; run 'pronto notify setup'")
)

// Helper exit codes for setup problems; must match exitNotAuthorized and
// exitDenied in nativehelper/main.swift.
const (
	helperExitNotAuthorized = 3
	helperExitDenied        = 4
)

// NativeRunner executes the native helper with a JSON payload on stdin.
type NativeRunner func(ctx context.Context, helperPath string, payload []byte) error

// helperPayload is the JSON contract between pronto and the helper app.
type helperPayload struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Subtitle string `json:"subtitle,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	Sound    string `json:"sound,omitempty"`
	Image    string `json:"image_path,omitempty"`
	URL      string `json:"url,omitempty"`
}

// NativeNotifier sends macOS notifications through the bundled UserNotifications
// helper app, giving native branding, click-to-open, and grouping.
type NativeNotifier struct {
	helperPath   string
	discoverOpts []DiscoverOption
	runner       NativeRunner
	soundEnabled bool
	timeout      time.Duration
}

// NativeNotifierOption configures a NativeNotifier.
type NativeNotifierOption func(*NativeNotifier)

// WithHelperPath overrides the helper binary path, disabling discovery.
func WithHelperPath(path string) NativeNotifierOption {
	return func(n *NativeNotifier) {
		n.helperPath = path
	}
}

// WithNativeRunner overrides the helper executor for testing.
func WithNativeRunner(runner NativeRunner) NativeNotifierOption {
	return func(n *NativeNotifier) {
		n.runner = runner
	}
}

// WithNativeSoundEnabled configures whether native notifications play sound.
// Disabled by default: sound is opt-in, matching notifications.sound.
func WithNativeSoundEnabled(enabled bool) NativeNotifierOption {
	return func(n *NativeNotifier) {
		n.soundEnabled = enabled
	}
}

// WithNativeTimeout overrides the per-delivery timeout.
func WithNativeTimeout(timeout time.Duration) NativeNotifierOption {
	return func(n *NativeNotifier) {
		n.timeout = timeout
	}
}

// WithNativeDiscoverOptions configures how the helper app bundle is
// discovered when no explicit helper path is set, reusing the same
// DiscoverOption set as DiscoverNativeHelper.
func WithNativeDiscoverOptions(opts ...DiscoverOption) NativeNotifierOption {
	return func(n *NativeNotifier) {
		n.discoverOpts = append(n.discoverOpts, opts...)
	}
}

// NewNativeNotifier creates an initialized NativeNotifier. When no explicit
// helper path is configured, the helper is discovered at Notify time (see
// DiscoverNativeHelper); Notify reports a helpful error if it cannot be found.
func NewNativeNotifier(opts ...NativeNotifierOption) *NativeNotifier {
	n := &NativeNotifier{
		timeout: defaultNotifyTimeout,
		runner:  defaultNativeRunner,
	}
	for _, opt := range opts {
		opt(n)
	}
	if n.timeout <= 0 {
		n.timeout = defaultNotifyTimeout
	}
	if n.runner == nil {
		n.runner = defaultNativeRunner
	}
	return n
}

// DiscoverOption customizes helper discovery.
type DiscoverOption func(*discovery)

type discovery struct {
	searchPaths []string
	useUserDirs bool
}

// WithSearchPaths adds directories searched for the helper app bundle.
func WithSearchPaths(dirs ...string) DiscoverOption {
	return func(d *discovery) {
		d.searchPaths = append(d.searchPaths, dirs...)
	}
}

// WithoutUserLocations skips the executable-relative and user data locations.
func WithoutUserLocations() DiscoverOption {
	return func(d *discovery) {
		d.useUserDirs = false
	}
}

// DiscoverNativeHelper locates the native helper binary. Order: the
// PRONTO_NOTIFY_HELPER environment variable, explicit search paths, the
// directory of the running executable, and ~/.local/share/pronto.
func DiscoverNativeHelper(opts ...DiscoverOption) string {
	d := &discovery{useUserDirs: true}
	for _, opt := range opts {
		opt(d)
	}
	if path := strings.TrimSpace(os.Getenv(helperEnvOverride)); path != "" {
		return path
	}

	binary := filepath.Join(HelperAppName, "Contents", "MacOS", HelperBinaryName)

	locations := make([]string, 0, len(d.searchPaths)+2)
	locations = append(locations, d.searchPaths...)
	if d.useUserDirs {
		if exe, err := os.Executable(); err == nil {
			locations = append(locations, filepath.Dir(exe))
		}
		if dir := dataDir(); dir != "" {
			locations = append(locations, dir)
		}
	}

	for _, dir := range locations {
		if dir == "" {
			continue
		}
		// #nosec G703 -- candidate paths come from trusted local configuration
		// (discovery locations or PRONTO_NOTIFY_HELPER), not user input.
		candidate := filepath.Join(dir, binary)
		// #nosec G703 -- see above; path is locally configured, not tainted.
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

// Notify delivers a desktop notification through the native helper app.
func (n *NativeNotifier) Notify(ctx context.Context, notif Notification) error {
	helperPath := n.helperPath
	if helperPath == "" {
		helperPath = DiscoverNativeHelper(n.discoverOpts...)
	}
	if helperPath == "" {
		return fmt.Errorf("%w (or set %s)", ErrHelperNotFound, helperEnvOverride)
	}

	sound := ""
	switch {
	case notif.Sound != "":
		sound = notif.Sound
	case n.soundEnabled:
		sound = "default"
	}

	payload := helperPayload{
		Title:    sanitizeText(notif.Title),
		Body:     sanitizeText(notif.Message),
		Subtitle: helperSubtitle(notif),
		ThreadID: prontoGroupID(notif.Repo, notif.PRNumber),
		Sound:    sound,
		Image:    notif.ImagePath,
		URL:      notif.URL,
	}
	if payload.Image == "" {
		payload.Image = DefaultTriggerImage(notif)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("native notification payload: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	if err := runWithRetry(runCtx, func() error { return n.runner(runCtx, helperPath, data) }); err != nil {
		return fmt.Errorf("native helper: %w", err)
	}
	return nil
}

// nativeAttempts and nativeRetryDelay bound helper respawns within one
// delivery timeout.
const (
	nativeAttempts   = 3
	nativeRetryDelay = 250 * time.Millisecond
)

// runWithRetry runs attempt up to nativeAttempts times, pausing between
// failures until ctx expires. Right after the helper is re-signed
// (rebuild/reinstall), usernoted briefly rejects it ("Notifications are not
// allowed") and caches that rejection for the life of the process, so only a
// fresh helper process can succeed.
func runWithRetry(ctx context.Context, attempt func() error) error {
	var err error
	for i := range nativeAttempts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return err
			case <-time.After(nativeRetryDelay):
			}
		}
		// Denial is a user decision; a fresh process can't change it.
		if err = attempt(); err == nil || ctx.Err() != nil || errors.Is(err, ErrDenied) {
			return err
		}
	}
	return err
}

// helperSubtitle renders the repo#number context line for native notifications.
func helperSubtitle(n Notification) string {
	if n.Repo == "" && n.PRNumber <= 0 {
		return ""
	}
	return fmt.Sprintf("%s#%d", n.Repo, n.PRNumber)
}

// prontoGroupID builds the per-PR grouping identifier shared by notification
// backends so replacements and threads land on one stack entry.
func prontoGroupID(repo string, number int) string {
	if repo == "" || number <= 0 {
		return ""
	}
	return fmt.Sprintf("pronto-%s-%d", strings.ReplaceAll(repo, "/", "_"), number)
}

// defaultNativeRunner spawns the helper, feeds it the payload on stdin, and
// surfaces non-zero exits with the helper's diagnostic output.
func defaultNativeRunner(ctx context.Context, helperPath string, payload []byte) error {
	// #nosec G204 -- helperPath comes from discovery or explicit configuration.
	cmd := exec.CommandContext(ctx, helperPath)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	return classifyHelperExit(combinedOutputError(out, err))
}

// classifyHelperExit maps the helper's setup-problem exit codes onto
// ErrNotAuthorized/ErrDenied, keeping the helper's diagnostic text.
func classifyHelperExit(err error) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	switch exitErr.ExitCode() {
	case helperExitNotAuthorized:
		return fmt.Errorf("%w: %w", ErrNotAuthorized, err)
	case helperExitDenied:
		return fmt.Errorf("%w: %w", ErrDenied, err)
	default:
		return err
	}
}
