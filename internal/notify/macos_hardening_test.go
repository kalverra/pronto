package notify_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/notify"
)

const tnPath = "/opt/homebrew/bin/terminal-notifier"

func testNotification() notify.Notification {
	return notify.Notification{
		Trigger:  notify.TriggerCIFailed,
		PRNumber: 123,
		PRTitle:  "Fix bug",
		Repo:     "kalverra/pronto",
		URL:      "https://github.com/kalverra/pronto/pull/123",
		Title:    "CI Failed (#123)",
		Message:  "CI failed for \"Fix bug\" (kalverra/pronto#123)",
	}
}

// A hung notification backend must not block delivery indefinitely: it has to
// time out and fall through to the next backend.
func TestMacNotifier_TerminalNotifierHangs_FallsBackToOSAScript(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var commands []string

	runner := func(ctx context.Context, name string, _ ...string) error {
		mu.Lock()
		commands = append(commands, name)
		mu.Unlock()
		if strings.HasSuffix(name, "terminal-notifier") {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}

	notifier := notify.NewMacNotifier(
		notify.WithTerminalNotifierPath(tnPath),
		notify.WithCommandRunner(runner),
		notify.WithNotifyTimeout(20*time.Millisecond),
	)

	err := notifier.Notify(context.Background(), testNotification())
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{tnPath, "osascript"}, commands)
}

// PR titles come from GitHub, so they are attacker-influenced: control bytes
// must never reach the terminal or the AppleScript interpreter.
func TestMacNotifier_StripsControlBytes(t *testing.T) {
	t.Parallel()

	var executedArgs []string
	runner := func(_ context.Context, _ string, args ...string) error {
		executedArgs = args
		return nil
	}

	notifier := notify.NewMacNotifier(
		notify.WithTerminalNotifierPath(tnPath),
		notify.WithCommandRunner(runner),
	)

	n := testNotification()
	n.Title = "CI Failed\x1b]777;notify;evil\x07"
	n.Message = "line1\nline2\x1b[2J\x00"

	require.NoError(t, notifier.Notify(context.Background(), n))

	joined := strings.Join(executedArgs, " ")
	for _, bad := range []string{"\x1b", "\x07", "\x00", "\n"} {
		assert.NotContains(t, joined, bad, "control byte must be stripped")
	}
	assert.Contains(t, joined, "CI Failed")
	assert.Contains(t, joined, "line1 line2")
}

func TestMacNotifier_OSAScript_EscapesQuotesAndBackslashes(t *testing.T) {
	t.Parallel()

	var script string
	runner := func(_ context.Context, name string, args ...string) error {
		if name == "osascript" && len(args) == 2 {
			script = args[1]
		}
		return nil
	}

	notifier := notify.NewMacNotifier(
		notify.WithTerminalNotifierPath(""),
		notify.WithCommandRunner(runner),
	)

	n := testNotification()
	n.Title = `path C:\temp`
	n.Message = `He said "hi"`

	require.NoError(t, notifier.Notify(context.Background(), n))

	assert.Contains(t, script, `\"hi\"`, "double quotes must be AppleScript-escaped")
	assert.Contains(t, script, `C:\\temp`, "backslashes must be AppleScript-escaped")
}

// Both backends failing must surface a diagnosable error, naming each attempt.
func TestMacNotifier_AllBackendsFail_ReportsBoth(t *testing.T) {
	t.Parallel()

	runner := func(_ context.Context, _ string, _ ...string) error {
		return assert.AnError
	}

	notifier := notify.NewMacNotifier(
		notify.WithTerminalNotifierPath(tnPath),
		notify.WithCommandRunner(runner),
	)

	err := notifier.Notify(context.Background(), testNotification())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "terminal-notifier")
	assert.Contains(t, err.Error(), "osascript")
}
