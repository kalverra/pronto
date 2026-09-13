package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/notify"
)

func TestMacNotifier_WithSoundEnabled_False(t *testing.T) {
	t.Parallel()

	t.Run("terminal-notifier omits -sound argument", func(t *testing.T) {
		t.Parallel()
		var executedArgs []string
		runner := func(_ context.Context, _ string, args ...string) error {
			executedArgs = args
			return nil
		}

		notifier := notify.NewMacNotifier(
			notify.WithTerminalNotifierPath("/opt/homebrew/bin/terminal-notifier"),
			notify.WithCommandRunner(runner),
			notify.WithSoundEnabled(false),
		)

		n := notify.Notification{
			Trigger: notify.TriggerCIPassed,
			Title:   "CI Passed",
			Message: "Checks passed",
		}

		err := notifier.Notify(context.Background(), n)
		require.NoError(t, err)

		argStr := strings.Join(executedArgs, " ")
		assert.NotContains(t, argStr, "-sound")
	})

	t.Run("osascript omits sound name", func(t *testing.T) {
		t.Parallel()
		var executedArgs []string
		runner := func(_ context.Context, _ string, args ...string) error {
			executedArgs = args
			return nil
		}

		notifier := notify.NewMacNotifier(
			notify.WithTerminalNotifierPath(""),
			notify.WithCommandRunner(runner),
			notify.WithSoundEnabled(false),
		)

		n := notify.Notification{
			Trigger: notify.TriggerCIPassed,
			Title:   "CI Passed",
			Message: "Checks passed",
		}

		err := notifier.Notify(context.Background(), n)
		require.NoError(t, err)

		require.Len(t, executedArgs, 2)
		assert.NotContains(t, executedArgs[1], "sound name")
	})
}

func TestMacSoundPlayer_Play(t *testing.T) {
	t.Parallel()

	t.Run("plays custom sound file", func(t *testing.T) {
		t.Parallel()
		var executedName string
		var executedArgs []string
		runner := func(_ context.Context, name string, args ...string) error {
			executedName = name
			executedArgs = args
			return nil
		}

		player := notify.NewMacSoundPlayer(
			notify.WithSoundPlayerRunner(runner),
			notify.WithSoundPlayerPath("/usr/bin/afplay"),
		)

		err := player.Play(context.Background(), "/tmp/custom.wav")
		require.NoError(t, err)
		assert.Equal(t, "/usr/bin/afplay", executedName)
		assert.Equal(t, []string{"/tmp/custom.wav"}, executedArgs)
	})

	t.Run("plays default sound when empty", func(t *testing.T) {
		t.Parallel()
		var executedArgs []string
		runner := func(_ context.Context, _ string, args ...string) error {
			executedArgs = args
			return nil
		}

		player := notify.NewMacSoundPlayer(
			notify.WithSoundPlayerRunner(runner),
			notify.WithSoundPlayerPath("/usr/bin/afplay"),
		)

		err := player.Play(context.Background(), "")
		require.NoError(t, err)
		assert.Equal(t, []string{"/System/Library/Sounds/Ping.aiff"}, executedArgs)
	})

	t.Run("resolves system sound name without path or extension", func(t *testing.T) {
		t.Parallel()
		var executedArgs []string
		runner := func(_ context.Context, _ string, args ...string) error {
			executedArgs = args
			return nil
		}

		player := notify.NewMacSoundPlayer(
			notify.WithSoundPlayerRunner(runner),
			notify.WithSoundPlayerPath("/usr/bin/afplay"),
		)

		err := player.Play(context.Background(), "Hero")
		require.NoError(t, err)
		assert.Equal(t, []string{"/System/Library/Sounds/Hero.aiff"}, executedArgs)
	})
}

func TestSoundNotifier_Notify(t *testing.T) {
	t.Parallel()

	t.Run("plays sound when enabled", func(t *testing.T) {
		t.Parallel()
		var playedSound string
		player := &mockSoundPlayer{
			playFunc: func(_ context.Context, sound string) error {
				playedSound = sound
				return nil
			},
		}

		notifier := notify.NewSoundNotifier(player, true)
		n := notify.Notification{
			Trigger:   notify.TriggerCIPassed,
			SoundPath: "/sounds/cheer.wav",
		}

		err := notifier.Notify(context.Background(), n)
		require.NoError(t, err)
		assert.Equal(t, "/sounds/cheer.wav", playedSound)
	})

	t.Run("does nothing when disabled", func(t *testing.T) {
		t.Parallel()
		called := false
		player := &mockSoundPlayer{
			playFunc: func(_ context.Context, _ string) error {
				called = true
				return nil
			},
		}

		notifier := notify.NewSoundNotifier(player, false)
		n := notify.Notification{
			Trigger:   notify.TriggerCIPassed,
			SoundPath: "/sounds/cheer.wav",
		}

		err := notifier.Notify(context.Background(), n)
		require.NoError(t, err)
		assert.False(t, called)
	})
}

func TestMultiNotifier(t *testing.T) {
	t.Parallel()

	var notif1, notif2 []notify.Notification
	mock1 := &mockTestNotifier{onNotify: func(n notify.Notification) { notif1 = append(notif1, n) }}
	mock2 := &mockTestNotifier{onNotify: func(n notify.Notification) { notif2 = append(notif2, n) }}

	multi := notify.MultiNotifier{mock1, mock2}
	n := notify.Notification{Trigger: notify.TriggerPRMerged, Title: "Merged"}

	err := multi.Notify(context.Background(), n)
	require.NoError(t, err)
	assert.Len(t, notif1, 1)
	assert.Len(t, notif2, 1)
}

type mockSoundPlayer struct {
	playFunc func(ctx context.Context, sound string) error
}

func (m *mockSoundPlayer) Play(ctx context.Context, sound string) error {
	if m.playFunc != nil {
		return m.playFunc(ctx, sound)
	}
	return nil
}

type mockTestNotifier struct {
	onNotify func(n notify.Notification)
}

func (m *mockTestNotifier) Notify(_ context.Context, n notify.Notification) error {
	if m.onNotify != nil {
		m.onNotify(n)
	}
	return nil
}
