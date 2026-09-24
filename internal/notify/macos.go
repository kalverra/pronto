package notify

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// MacNotifier sends macOS notifications using terminal-notifier or osascript.
type MacNotifier struct {
	terminalNotifierPath string
	runner               CommandRunner
	soundEnabled         bool
	timeout              time.Duration
}

// MacNotifierOption configures a MacNotifier.
type MacNotifierOption func(*MacNotifier)

// WithTerminalNotifierPath overrides the path to terminal-notifier.
func WithTerminalNotifierPath(path string) MacNotifierOption {
	return func(m *MacNotifier) {
		m.terminalNotifierPath = path
	}
}

// WithCommandRunner overrides the command executor for testing.
func WithCommandRunner(runner CommandRunner) MacNotifierOption {
	return func(m *MacNotifier) {
		m.runner = runner
	}
}

// WithSoundEnabled configures whether macOS notifications should play sound.
// Disabled by default: sound is opt-in, matching notifications.sound.
func WithSoundEnabled(enabled bool) MacNotifierOption {
	return func(m *MacNotifier) {
		m.soundEnabled = enabled
	}
}

// WithNotifyTimeout overrides the per-backend delivery timeout.
func WithNotifyTimeout(timeout time.Duration) MacNotifierOption {
	return func(m *MacNotifier) {
		m.timeout = timeout
	}
}

// NewMacNotifier creates an initialized MacNotifier.
func NewMacNotifier(opts ...MacNotifierOption) *MacNotifier {
	m := &MacNotifier{
		timeout: defaultNotifyTimeout,
	}
	if path, err := exec.LookPath("terminal-notifier"); err == nil {
		m.terminalNotifierPath = path
	}
	m.runner = runCommand
	for _, opt := range opts {
		opt(m)
	}
	if m.timeout <= 0 {
		m.timeout = defaultNotifyTimeout
	}
	return m
}

// soundName picks the system sound name to pass to a backend: the
// notification's override if set, otherwise "default".
func soundName(n Notification) string {
	if n.Sound != "" {
		return n.Sound
	}
	return "default"
}

// Notify delivers a desktop notification via terminal-notifier or osascript.
// Backends are tried in order and every failure is reported, so a caller can
// log why nothing reached the screen.
func (m *MacNotifier) Notify(ctx context.Context, n Notification) error {
	runner := m.runner
	if runner == nil {
		runner = runCommand
	}

	title := sanitizeText(n.Title)
	message := sanitizeText(n.Message)

	var errs []error

	if m.terminalNotifierPath != "" {
		args := []string{
			"-title", title,
			"-message", message,
		}
		if m.soundEnabled {
			args = append(args, "-sound", soundName(n))
		}
		imagePath := n.ImagePath
		if imagePath == "" {
			imagePath = DefaultTriggerImage(n)
		}
		if imagePath != "" {
			args = append(args, "-contentImage", imagePath)
		}
		if n.URL != "" {
			args = append(args, "-open", n.URL)
		}
		if group := prontoGroupID(n.Repo, n.PRNumber); group != "" {
			args = append(args, "-group", group)
		}
		err := m.run(ctx, runner, m.terminalNotifierPath, args...)
		if err == nil {
			return nil
		}
		errs = append(errs, fmt.Errorf("terminal-notifier: %w", err))
	}

	// Fallback to osascript
	script := fmt.Sprintf(
		"display notification %s with title %s",
		appleScriptString(message),
		appleScriptString(title),
	)
	if m.soundEnabled {
		script += " sound name " + appleScriptString(soundName(n))
	}
	if err := m.run(ctx, runner, "osascript", "-e", script); err != nil {
		errs = append(errs, fmt.Errorf("osascript: %w", err))
		return errors.Join(errs...)
	}
	return nil
}

// run bounds a single delivery attempt so one wedged backend cannot stall the
// rest of the chain.
func (m *MacNotifier) run(ctx context.Context, runner CommandRunner, name string, args ...string) error {
	timeout := m.timeout
	if timeout <= 0 {
		timeout = defaultNotifyTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runner(runCtx, name, args...)
}

// sanitizeText removes control bytes from notification text. Titles and bodies
// originate from GitHub, where anyone opening a pull request controls them, so
// they must not be able to inject terminal escape sequences or terminate an
// AppleScript string literal.
func sanitizeText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, s)
}

// appleScriptString renders s as an AppleScript string literal. Go's %q verb is
// not AppleScript-safe: it emits Go escapes the interpreter does not share.
func appleScriptString(s string) string {
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
