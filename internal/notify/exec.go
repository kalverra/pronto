package notify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// defaultNotifyTimeout bounds a single delivery or build attempt. Both
// terminal-notifier and the native helper can block for seconds on an
// unavailable notification service (no GUI session, hung XPC), which would
// otherwise stall every queued notification behind it.
const defaultNotifyTimeout = 3 * time.Second

// CommandRunner executes a system command with arguments, folding combined
// output into the returned error. It is the injectable seam shared by
// MacNotifier and the native helper installer.
type CommandRunner func(ctx context.Context, name string, args ...string) error

// runCommand is the production CommandRunner: it runs name with args and
// reports combined stdout+stderr alongside a non-zero exit so callers can log
// why a delivery or build step failed.
func runCommand(ctx context.Context, name string, args ...string) error {
	// #nosec G204 -- name and args are restricted to trusted local binaries
	// (terminal-notifier, osascript, swiftc, codesign, lsregister).
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return combinedOutputError(out, err)
}

// combinedOutputError folds a command's combined output into err so the
// caller's error message explains what went wrong, not just that it did.
func combinedOutputError(out []byte, err error) error {
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
