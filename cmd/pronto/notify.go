package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/logging"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/tui"
)

// notifierFactory builds the desktop notifier from user config. It is the same
// factory the TUI uses, so `notify --test` exercises the real delivery path.
var notifierFactory = tui.DefaultNotifierFactory

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
	configPath string
	logPath    string
	popups     bool
	sound      bool
	backends   []notifyBackend
	assets     []notifyAsset
}

func buildNotifyReport(cfg config.NotificationConfig) notifyReport {
	r := notifyReport{
		configPath: config.Path(),
		logPath:    logging.LogPath(),
		popups:     cfg.Popups,
		sound:      cfg.Sound,
	}

	for _, b := range []notifyBackend{
		{name: "terminal-notifier", role: "primary popups: click-to-open PR, grouping, images"},
		{name: "osascript", role: "fallback popups: text only"},
		{name: "afplay", role: "sound playback"},
	} {
		if path, err := exec.LookPath(b.name); err == nil {
			b.path = path
		}
		r.backends = append(r.backends, b)
	}

	r.assets = append(r.assets, collectAssets("image", cfg.Images)...)
	r.assets = append(r.assets, collectAssets("sound", cfg.Sounds)...)
	return r
}

// collectAssets resolves configured per-trigger files, flagging any that are
// absent: a missing image or sound is a silent delivery failure at runtime.
func collectAssets(kind string, paths map[string]string) []notifyAsset {
	triggers := make([]string, 0, len(paths))
	for trigger := range paths {
		triggers = append(triggers, trigger)
	}
	slices.Sort(triggers)

	assets := make([]notifyAsset, 0, len(triggers))
	for _, trigger := range triggers {
		path := config.ExpandPath(paths[trigger])
		_, err := os.Stat(path)
		assets = append(assets, notifyAsset{
			kind:    kind,
			trigger: trigger,
			path:    path,
			missing: err != nil,
		})
	}
	return assets
}

func formatNotifyReport(r notifyReport) string {
	var b strings.Builder
	b.WriteString("Notifications\n\n")
	fmt.Fprintf(&b, "  Config:  %s\n", r.configPath)
	fmt.Fprintf(&b, "  Log:     %s (delivery failures are logged here)\n\n", r.logPath)
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
	b.WriteString("  - System Settings > Notifications: allow \"terminal-notifier\"\n")
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
			if _, err := fmt.Fprint(out, formatNotifyReport(buildNotifyReport(cfg.Notifications))); err != nil {
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
	return cmd
}

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
		Message: "Notifications are working. Sent by `pronto notify --test`.",
	}
	if snd, ok := cfg.Sounds[string(notify.TriggerCIPassed)]; ok {
		n.SoundPath = config.ExpandPath(snd)
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

	_, _ = fmt.Fprint(out, "\nTest:\n  Sent. No banner means macOS suppressed it: check the two items above.\n")
	return nil
}
