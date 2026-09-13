package notify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CommandRunner executes a system command with arguments.
type CommandRunner func(ctx context.Context, name string, args ...string) error

func defaultRunner(ctx context.Context, name string, args ...string) error {
	// #nosec G204 -- name and args are restricted to terminal-notifier or osascript.
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// MacNotifier sends macOS notifications using terminal-notifier or osascript.
type MacNotifier struct {
	terminalNotifierPath string
	runner               CommandRunner
	soundEnabled         bool
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
func WithSoundEnabled(enabled bool) MacNotifierOption {
	return func(m *MacNotifier) {
		m.soundEnabled = enabled
	}
}

// NewMacNotifier creates an initialized MacNotifier.
func NewMacNotifier(opts ...MacNotifierOption) *MacNotifier {
	m := &MacNotifier{
		soundEnabled: true,
	}
	if path, err := exec.LookPath("terminal-notifier"); err == nil {
		m.terminalNotifierPath = path
	}
	m.runner = defaultRunner
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Notify delivers a desktop notification via terminal-notifier or osascript.
func (m *MacNotifier) Notify(ctx context.Context, n Notification) error {
	runner := m.runner
	if runner == nil {
		runner = defaultRunner
	}

	if m.terminalNotifierPath != "" {
		args := []string{
			"-title", n.Title,
			"-message", n.Message,
		}
		if m.soundEnabled {
			args = append(args, "-sound", "default")
		}
		if n.ImagePath != "" {
			args = append(args, "-contentImage", n.ImagePath)
		}
		if n.URL != "" {
			args = append(args, "-open", n.URL)
		}
		if n.Repo != "" && n.PRNumber > 0 {
			args = append(args, "-group", fmt.Sprintf("pronto-%s-%d", strings.ReplaceAll(n.Repo, "/", "_"), n.PRNumber))
		}
		if err := runner(ctx, m.terminalNotifierPath, args...); err == nil {
			return nil
		}
	}

	// Fallback to osascript
	var script string
	if m.soundEnabled {
		script = fmt.Sprintf(`display notification %q with title %q sound name "default"`, n.Message, n.Title)
	} else {
		script = fmt.Sprintf(`display notification %q with title %q`, n.Message, n.Title)
	}
	return runner(ctx, "osascript", "-e", script)
}
