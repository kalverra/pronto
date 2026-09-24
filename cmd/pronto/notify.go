package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/logging"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/tui"
)

// notifierFactory builds the desktop notifier from user config. It is the same
// factory the TUI uses, so `notify --test` exercises the real delivery path.
var notifierFactory = tui.DefaultNotifierFactory

// installNativeHelperFn and registerHelperFn are the injectable seams for
// `notify setup`; tests stub them out so no real Swift compile, codesign, or
// LaunchServices call happens.
var (
	installNativeHelperFn = notify.InstallNativeHelper
	registerHelperFn      = notify.RegisterHelper
	helperModeFn          = runHelperMode
)

// ErrNoNotificationChannels is returned when every notification channel is disabled.
var ErrNoNotificationChannels = errors.New("no notification channels enabled")

// notifyBackend describes a delivery program and whether it is installed.
type notifyBackend struct {
	name string
	path string
	role string
}

// notifyAsset describes a per-trigger image or sound file from config.
type notifyAsset struct {
	kind    string
	trigger string
	path    string
	missing bool
}

// notifyReport is the resolved notification setup, rendered by formatNotifyReport.
type notifyReport struct {
	configPath  string
	logPath     string
	mode        string
	popups      bool
	sound       bool
	helperState string // "installed", "stale", "missing", or "" on non-darwin
	authStatus  string // "authorized", "denied", "notDetermined", or "" if unknown
	backends    []notifyBackend
	assets      []notifyAsset
}

func buildNotifyReport(ctx context.Context, cfg config.NotificationConfig) notifyReport {
	r := notifyReport{
		configPath: config.Path(),
		logPath:    logging.LogPath(),
		mode:       cfg.Mode,
		popups:     cfg.Popups,
		sound:      cfg.Sound,
	}

	helperPath := notify.DiscoverNativeHelper()
	for _, b := range []notifyBackend{
		{name: "terminal-notifier", role: "terminal mode: click-to-open PR, grouping, images"},
		{name: "osascript", role: "terminal mode fallback: text only"},
		{
			name: "native helper", path: helperPath,
			role: "native mode: Pronto branding, click to open PR, grouping (setup: pronto notify setup)",
		},
	} {
		if b.name != "native helper" {
			if path, err := exec.LookPath(b.name); err == nil {
				b.path = path
			}
		}
		r.backends = append(r.backends, b)
	}

	r.helperState, r.authStatus = nativeHelperStatus(ctx, helperPath)

	r.assets = append(r.assets, collectAssets("image", cfg.Images)...)
	r.assets = append(r.assets, collectAssets("sound", cfg.Sounds)...)
	return r
}

// nativeHelperStatus reports the installed/stale/missing state of the native
// helper bundle and, when it's runnable, its current authorization status.
func nativeHelperStatus(ctx context.Context, helperPath string) (state, auth string) {
	state = string(notify.NativeHelperState(helperPath))
	if state == string(notify.HelperMissing) {
		return state, ""
	}
	// Bounded: this runs on every plain `pronto notify`, so a wedged helper
	// (no GUI session, hung XPC) must never hang the CLI.
	queryCtx, cancel := context.WithTimeout(ctx, helperStatusQueryTimeout)
	defer cancel()
	status, err := helperModeFn(queryCtx, helperPath, "--status")
	if err != nil {
		return state, ""
	}
	return state, status.Status
}

// helperStatusQueryTimeout bounds the passive `pronto notify` status check
// against the native helper; the guided `notify setup` authorize step is not
// subject to this (a user actively waiting for a permission prompt).
const helperStatusQueryTimeout = 2 * time.Second

// collectAssets resolves configured per-trigger files, flagging any that are
// absent: a missing image or sound is a silent delivery failure at runtime.
// Sounds are system sound names rather than files, so they are reported but
// never flagged as missing here; config.Validate already rejects unknown
// sound names at load time.
func collectAssets(kind string, paths map[string]string) []notifyAsset {
	triggers := make([]string, 0, len(paths))
	for trigger := range paths {
		triggers = append(triggers, trigger)
	}
	slices.Sort(triggers)

	assets := make([]notifyAsset, 0, len(triggers))
	for _, trigger := range triggers {
		value := paths[trigger]
		asset := notifyAsset{kind: kind, trigger: trigger, path: value}
		if kind == "image" {
			path := config.ExpandPath(value)
			asset.path = path
			_, err := os.Stat(path)
			asset.missing = err != nil
		}
		assets = append(assets, asset)
	}
	return assets
}

func formatNotifyReport(r notifyReport) string {
	var b strings.Builder
	b.WriteString("Notifications\n\n")
	fmt.Fprintf(&b, "  Config:  %s\n", r.configPath)
	fmt.Fprintf(&b, "  Log:     %s (delivery failures are logged here)\n\n", r.logPath)
	fmt.Fprintf(&b, "  Mode:    %s\n", r.mode)
	fmt.Fprintf(&b, "  Popups:  %s\n", enabledLabel(r.popups))
	fmt.Fprintf(&b, "  Sound:   %s\n\n", enabledLabel(r.sound))

	b.WriteString("Backends:\n")
	for _, backend := range r.backends {
		mark, location := "x", "not found"
		if backend.path != "" {
			mark, location = "+", backend.path
		}
		fmt.Fprintf(&b, "  %s %-18s %s\n", mark, backend.name, location)
		fmt.Fprintf(&b, "      %s\n", backend.role)
	}

	if r.helperState != "" {
		fmt.Fprintf(&b, "\nNative helper: %s\n", r.helperState)
		if r.authStatus != "" {
			fmt.Fprintf(&b, "  Authorization: %s\n", r.authStatus)
		}
		switch {
		case r.authStatus == "denied":
			b.WriteString("  Allow \"Pronto\" in System Settings > Notifications:\n")
			b.WriteString("    open x-apple.systempreferences:com.apple.Notifications-Settings.extension\n")
		case r.helperState != "installed" || r.authStatus == "notDetermined":
			b.WriteString("  Run: pronto notify setup\n")
		}
		if r.mode == config.NotifyNative && (r.helperState == "missing" || r.authStatus == "denied" ||
			r.authStatus == "notDetermined") {
			b.WriteString("  Until then, notifications fall back to terminal mode.\n")
		}
	}

	if len(r.assets) > 0 {
		b.WriteString("\nPer-trigger assets:\n")
		for _, asset := range r.assets {
			suffix := ""
			if asset.missing {
				suffix = "  (missing)"
			}
			fmt.Fprintf(&b, "  %-5s %-16s %s%s\n", asset.kind, asset.trigger, asset.path, suffix)
		}
	}

	b.WriteString("\nIf no banner appears:\n")
	b.WriteString("  - System Settings > Notifications: allow \"terminal-notifier\" or \"Pronto\"\n")
	b.WriteString("  - Turn off Focus / Do Not Disturb\n")
	fmt.Fprintf(&b, "  - Check delivery errors: rg \"delivering notification failed\" %s\n", r.logPath)

	return b.String()
}

func enabledLabel(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled"
}

func newNotifyCmd() *cobra.Command {
	var test bool

	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Show notification setup, or send a test notification with --test",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			out := cmd.OutOrStdout()
			if _, err := fmt.Fprint(
				out,
				formatNotifyReport(buildNotifyReport(cmd.Context(), cfg.Notifications)),
			); err != nil {
				return err
			}

			if !test {
				_, err := fmt.Fprint(out, "\nSend a test notification: pronto notify --test\n")
				return err
			}
			return sendTestNotification(cmd.Context(), out, cfg.Notifications)
		},
	}
	cmd.Flags().BoolVar(&test, "test", false, "Send a test notification through the configured channels")
	cmd.AddCommand(newNotifySetupCmd())
	return cmd
}

// setupOptions configures newNotifySetupCmd's flags.
type setupOptions struct {
	output     string
	force      bool
	noRegister bool
	bundleID   string
}

// newNotifySetupCmd is the guided, idempotent path to native notifications:
// build the helper if it's missing or stale, register it with LaunchServices,
// request authorization, and send a real test banner.
func newNotifySetupCmd() *cobra.Command {
	var opts setupOptions

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Guided setup for native macOS notifications",
		Long: "Build (if needed) and install the bundled Swift helper, register it with " +
			"LaunchServices, request the one-time notification permission, and send a test " +
			"banner. Safe to run repeatedly: each step is skipped when already satisfied.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNotifySetup(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.output, "output", "",
		"Build the app bundle at this path instead of ~/.local/share/pronto")
	cmd.Flags().BoolVar(&opts.force, "force", false, "Rebuild even if the helper is already up to date")
	cmd.Flags().BoolVar(&opts.noRegister, "no-register", false,
		"Skip LaunchServices registration; for throwaway/dev builds (used by mise run bundle)")
	cmd.Flags().StringVar(&opts.bundleID, "bundle-id", notify.HelperBundleID,
		"Bundle identifier for the helper; dev builds use "+notify.DevHelperBundleID+
			" so they never collide with the installed helper")
	return cmd
}

func runNotifySetup(ctx context.Context, out io.Writer, opts setupOptions) error {
	if runtime.GOOS != "darwin" {
		step(out, "x  native notifications require macOS")
		return errors.New("native notifications require macOS")
	}
	step(out, "+  macOS detected")

	if notify.SwiftCPath() == "" {
		step(out, "x  swiftc not found")
		return errors.New(
			"swiftc not found; install the Xcode command line tools with 'xcode-select --install' (free, no Apple Developer account)",
		)
	}
	step(out, "+  swiftc found")

	dest := opts.output
	if dest == "" {
		dest = notify.DefaultHelperAppPath()
	}
	if dest == "" {
		return errors.New("cannot resolve an install location; pass --output")
	}

	installOpts := []notify.InstallOption{notify.WithBundleID(opts.bundleID)}
	if opts.noRegister {
		installOpts = append(installOpts, notify.WithoutRegister())
	}

	// Check before registering: registration purges these, so this is the
	// only chance to see what was there.
	staleInstalls := notify.StaleHelperInstalls(dest)

	installedVersion, _ := notify.InstalledHelperVersion(dest)
	installedID, _ := notify.InstalledHelperBundleID(dest)
	if opts.force || installedVersion != notify.HelperVersion() || installedID != opts.bundleID {
		path, err := installNativeHelperFn(dest, installOpts...)
		if err != nil {
			step(out, "x  build failed: %v", err)
			return err
		}
		step(out, "+  built and installed: %s", path)
	} else {
		step(out, "+  helper already up to date: %s", dest)
		if err := registerHelperFn(ctx, dest, installOpts...); err != nil {
			step(out, "x  LaunchServices registration failed: %v", err)
			return err
		}
	}

	if opts.noRegister {
		step(out, "-  LaunchServices registration skipped (--no-register)")
		return nil
	}
	step(out, "+  registered with LaunchServices")
	if len(staleInstalls) > 0 {
		step(out, "+  unregistered other installs of the helper (%s); if the app icon "+
			"still looks wrong, run once: killall usernoted NotificationCenter (safe, both respawn)",
			strings.Join(staleInstalls, ", "))
	}

	helperBinary := filepath.Join(dest, "Contents", "MacOS", notify.HelperBinaryName)
	status, err := authorizeHelper(ctx, helperBinary)
	if err != nil {
		step(out, "x  could not request notification authorization: %v", err)
		return err
	}
	switch status.Status {
	case "authorized":
		step(out, "+  notifications authorized")
	case "denied":
		step(out, "x  notifications denied for Pronto; allow them in System Settings > Notifications:")
		step(out, "     open x-apple.systempreferences:com.apple.Notifications-Settings.extension")
	default:
		step(out, "-  notification authorization: %s", status.Status)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := sendTestNotification(ctx, out, cfg.Notifications); err != nil {
		return err
	}

	if cfg.Notifications.Mode == config.NotifyTerminal {
		step(out, "\nnotifications.mode is %q in %s; native is installed but not selected.",
			config.NotifyTerminal, config.Path())
	}
	return nil
}

// step prints one setup progress line. The write error is deliberately
// ignored: a status writer that fails mid-run has bigger problems than a
// dropped progress line, and failing setup over it would be unhelpful.
func step(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format+"\n", args...)
}

// authorizeAttempts and authorizeRetryDelay bound how long setup respawns
// the helper's --authorize while usernoted still rejects a freshly signed
// bundle. A var so tests can skip the delay.
const authorizeAttempts = 10

var authorizeRetryDelay = 500 * time.Millisecond

// authorizeHelper requests notification authorization, respawning the helper
// while the status stays notDetermined. Right after install, usernoted can
// reject the new bundle before showing the prompt and caches that rejection
// per process; once the prompt shows, --authorize blocks until the user
// decides, which always leaves notDetermined.
func authorizeHelper(ctx context.Context, helperBinary string) (helperStatus, error) {
	var status helperStatus
	for i := range authorizeAttempts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return status, ctx.Err()
			case <-time.After(authorizeRetryDelay):
			}
		}
		var err error
		status, err = helperModeFn(ctx, helperBinary, "--authorize")
		if err != nil || status.Status != "notDetermined" {
			return status, err
		}
	}
	return status, nil
}

// helperStatus is the JSON status the native helper prints for --authorize
// and --status: {"status":"authorized|denied|notDetermined"}.
type helperStatus struct {
	Status string `json:"status"`
}

// runHelperMode runs the installed helper binary with a mode flag
// ("--authorize" or "--status") and parses its JSON status response.
func runHelperMode(ctx context.Context, helperPath, mode string) (helperStatus, error) {
	// #nosec G204 -- helperPath is our own installed bundle's binary; mode is a fixed internal flag.
	cmd := exec.CommandContext(ctx, helperPath, mode)
	out, err := cmd.Output()
	if err != nil {
		return helperStatus{}, err
	}
	var hs helperStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &hs); err != nil {
		return helperStatus{}, fmt.Errorf("parse helper status: %w", err)
	}
	return hs, nil
}

// testNotificationURL is opened when the test banner is clicked.
const testNotificationURL = "https://github.com/pulls"

// sendTestNotification delivers one notification through the configured
// channels, so a broken or suppressed backend can be caught without waiting
// for a real pull request event.
func sendTestNotification(ctx context.Context, out io.Writer, cfg config.NotificationConfig) error {
	notifier := notifierFactory(cfg)
	if notifier == nil {
		return fmt.Errorf(
			"%w: set notifications.popups or notifications.sound in %s",
			ErrNoNotificationChannels,
			config.Path(),
		)
	}

	if ctx == nil {
		ctx = context.Background()
	}

	n := notify.Notification{
		Trigger: notify.TriggerCIPassed,
		Title:   "pronto notification test",
		Message: "Notifications are working. Click to open your pull requests.",
		// A URL makes the banner clickable, so the test also exercises
		// click-to-open in the native helper.
		URL: testNotificationURL,
	}
	if snd, ok := cfg.Sounds[string(notify.TriggerCIPassed)]; ok {
		n.Sound = snd
	}
	if img, ok := cfg.Images[string(notify.TriggerCIPassed)]; ok {
		n.ImagePath = config.ExpandPath(img)
	} else {
		n.ImagePath = notify.DefaultTriggerImage(n)
	}

	if err := notifier.Notify(ctx, n); err != nil {
		_, _ = fmt.Fprintf(out, "\nTest:\n  FAILED: %v\n", err)
		return fmt.Errorf("send test notification: %w", err)
	}

	// A native notifier that fell back still "succeeds"; say so, or the
	// user sees a terminal-notifier banner and assumes native works.
	if h, ok := notifier.(interface{ Health() error }); ok {
		if herr := h.Health(); herr != nil && !errors.Is(herr, notify.ErrHelperStale) {
			_, _ = fmt.Fprintf(out, "\nTest:\n  Sent via terminal fallback: %v\n", herr)
			return nil
		}
	}

	_, _ = fmt.Fprint(
		out,
		"\nTest:\n  Sent. No banner: allow \"Pronto\" in System Settings > Notifications and turn off Focus.\n",
	)
	return nil
}
