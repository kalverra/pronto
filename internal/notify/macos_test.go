package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/notify"
)

func TestMacNotifier_TerminalNotifier_CommandArgs(t *testing.T) {
	t.Parallel()

	var executedName string
	var executedArgs []string

	runner := func(_ context.Context, name string, args ...string) error {
		executedName = name
		executedArgs = args
		return nil
	}

	notifier := notify.NewMacNotifier(
		notify.WithTerminalNotifierPath("/opt/homebrew/bin/terminal-notifier"),
		notify.WithCommandRunner(runner),
	)

	n := notify.Notification{
		Trigger:  notify.TriggerCIPassed,
		PRNumber: 123,
		PRTitle:  "Fix critical bug",
		Repo:     "kalverra/pronto",
		URL:      "https://github.com/kalverra/pronto/pull/123",
		Title:    "PRonto: CI Passed (#123)",
		Message:  "Checks passed for \"Fix critical bug\" (kalverra/pronto#123)",
	}

	err := notifier.Notify(context.Background(), n)
	require.NoError(t, err)

	assert.Equal(t, "/opt/homebrew/bin/terminal-notifier", executedName)
	argStr := strings.Join(executedArgs, " ")
	assert.Contains(t, argStr, "-title PRonto: CI Passed (#123)")
	assert.Contains(t, argStr, "-message Checks passed for \"Fix critical bug\" (kalverra/pronto#123)")
	assert.Contains(t, argStr, "-open https://github.com/kalverra/pronto/pull/123")
	assert.NotContains(t, argStr, "-sound", "sound is opt-in and must be off by default")
}

func TestMacNotifier_Sound(t *testing.T) {
	t.Parallel()

	t.Run("terminal-notifier passes the default sound when enabled without an override", func(t *testing.T) {
		t.Parallel()
		var executedArgs []string
		runner := func(_ context.Context, _ string, args ...string) error {
			executedArgs = args
			return nil
		}

		notifier := notify.NewMacNotifier(
			notify.WithTerminalNotifierPath("/opt/homebrew/bin/terminal-notifier"),
			notify.WithCommandRunner(runner),
			notify.WithSoundEnabled(true),
		)

		err := notifier.Notify(
			context.Background(),
			notify.Notification{Trigger: notify.TriggerCIPassed, Title: "t", Message: "m"},
		)
		require.NoError(t, err)
		assert.Contains(t, strings.Join(executedArgs, " "), "-sound default")
	})

	t.Run("terminal-notifier passes the configured system sound name", func(t *testing.T) {
		t.Parallel()
		var executedArgs []string
		runner := func(_ context.Context, _ string, args ...string) error {
			executedArgs = args
			return nil
		}

		notifier := notify.NewMacNotifier(
			notify.WithTerminalNotifierPath("/opt/homebrew/bin/terminal-notifier"),
			notify.WithCommandRunner(runner),
			notify.WithSoundEnabled(true),
		)

		err := notifier.Notify(
			context.Background(),
			notify.Notification{Trigger: notify.TriggerCIPassed, Title: "t", Message: "m", Sound: "Glass"},
		)
		require.NoError(t, err)
		assert.Contains(t, strings.Join(executedArgs, " "), "-sound Glass")
	})

	t.Run("osascript includes the sound name when enabled", func(t *testing.T) {
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
			notify.WithSoundEnabled(true),
		)

		err := notifier.Notify(
			context.Background(),
			notify.Notification{Trigger: notify.TriggerCIPassed, Title: "t", Message: "m", Sound: "Glass"},
		)
		require.NoError(t, err)
		assert.Contains(t, script, `sound name "Glass"`)
	})

	t.Run("osascript omits sound name when disabled", func(t *testing.T) {
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

		err := notifier.Notify(
			context.Background(),
			notify.Notification{Trigger: notify.TriggerCIPassed, Title: "t", Message: "m"},
		)
		require.NoError(t, err)
		assert.NotContains(t, script, "sound name")
	})
}

func TestMacNotifier_OSAScript_Fallback(t *testing.T) {
	t.Parallel()

	var executedName string
	var executedArgs []string

	runner := func(_ context.Context, name string, args ...string) error {
		executedName = name
		executedArgs = args
		return nil
	}

	notifier := notify.NewMacNotifier(
		notify.WithTerminalNotifierPath(""), // no terminal-notifier
		notify.WithCommandRunner(runner),
	)

	n := notify.Notification{
		Trigger:  notify.TriggerCIPassed,
		PRNumber: 123,
		PRTitle:  "Fix critical bug",
		Repo:     "kalverra/pronto",
		URL:      "https://github.com/kalverra/pronto/pull/123",
		Title:    "PRonto: CI Passed (#123)",
		Message:  "Checks passed for \"Fix critical bug\" (kalverra/pronto#123)",
	}

	err := notifier.Notify(context.Background(), n)
	require.NoError(t, err)

	assert.Equal(t, "osascript", executedName)
	require.Len(t, executedArgs, 2)
	assert.Equal(t, "-e", executedArgs[0])
	assert.Contains(t, executedArgs[1], "display notification")
	assert.Contains(t, executedArgs[1], "PRonto: CI Passed (#123)")
}

func TestMacNotifier_TerminalNotifier_ContentImage(t *testing.T) {
	t.Parallel()

	var executedArgs []string
	runner := func(_ context.Context, _ string, args ...string) error {
		executedArgs = args
		return nil
	}

	notifier := notify.NewMacNotifier(
		notify.WithTerminalNotifierPath("/opt/homebrew/bin/terminal-notifier"),
		notify.WithCommandRunner(runner),
	)

	n := notify.Notification{
		Trigger:   notify.TriggerCIPassed,
		PRNumber:  123,
		PRTitle:   "Fix critical bug",
		Repo:      "kalverra/pronto",
		ImagePath: "/tmp/success.png",
		Title:     "PRonto: CI Passed (#123)",
		Message:   "Checks passed",
	}

	err := notifier.Notify(context.Background(), n)
	require.NoError(t, err)

	argStr := strings.Join(executedArgs, " ")
	assert.Contains(t, argStr, "-contentImage /tmp/success.png")
}

func TestMacNotifier_TerminalNotifier_Failure_FallsBackToOSAScript(t *testing.T) {
	t.Parallel()

	var executedCommands []string
	runner := func(_ context.Context, name string, _ ...string) error {
		executedCommands = append(executedCommands, name)
		if name == "/opt/homebrew/bin/terminal-notifier" {
			return assert.AnError
		}
		return nil
	}

	notifier := notify.NewMacNotifier(
		notify.WithTerminalNotifierPath("/opt/homebrew/bin/terminal-notifier"),
		notify.WithCommandRunner(runner),
	)

	n := notify.Notification{
		Trigger:  notify.TriggerCIPassed,
		PRNumber: 123,
		PRTitle:  "Fix critical bug",
		Repo:     "kalverra/pronto",
		Title:    "PRonto: CI Passed (#123)",
		Message:  "Checks passed",
	}

	err := notifier.Notify(context.Background(), n)
	require.NoError(t, err)

	require.Len(t, executedCommands, 2)
	assert.Equal(t, "/opt/homebrew/bin/terminal-notifier", executedCommands[0])
	assert.Equal(t, "osascript", executedCommands[1])
}

func TestMacNotifier_TerminalNotifier_DefaultImageFallback(t *testing.T) {
	t.Parallel()

	var executedArgs []string
	runner := func(_ context.Context, _ string, args ...string) error {
		executedArgs = args
		return nil
	}

	notifier := notify.NewMacNotifier(
		notify.WithTerminalNotifierPath("/opt/homebrew/bin/terminal-notifier"),
		notify.WithCommandRunner(runner),
	)

	n := notify.Notification{
		Trigger:  notify.TriggerCIPassed,
		PRNumber: 123,
		PRTitle:  "Fix critical bug",
		Repo:     "kalverra/pronto",
		Title:    "PRonto: CI Passed (#123)",
		Message:  "Checks passed",
	}

	err := notifier.Notify(context.Background(), n)
	require.NoError(t, err)

	argStr := strings.Join(executedArgs, " ")
	assert.Contains(t, argStr, "-contentImage")
	assert.Contains(t, argStr, "ci-passed.png")
}
