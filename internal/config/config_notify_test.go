package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
)

func TestLoadFile_NotificationsDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadFile("testdata/notifications_defaults.toml")
	require.NoError(t, err)
	assert.True(t, cfg.Notifications.Popups, "popups should default to true")
	assert.False(t, cfg.Notifications.Sound, "sound should default to false")
	assert.Equal(t, config.NotifyNative, cfg.Notifications.Mode, "mode should default to native")
	assert.Empty(t, cfg.Notifications.Sounds)
	assert.Empty(t, cfg.Notifications.Images)
}

func TestLoadFile_NotificationsFull(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadFile("testdata/notifications_full.toml")
	require.NoError(t, err)

	assert.False(t, cfg.Notifications.Popups)
	assert.True(t, cfg.Notifications.Sound)
	assert.Equal(t, config.NotifyNative, cfg.Notifications.Mode)
	assert.Equal(t, "Glass", cfg.Notifications.Sounds["ci_passed"])
	assert.Equal(t, "Basso", cfg.Notifications.Sounds["ci_failed"])
	assert.Equal(t, "/icons/pass.png", cfg.Notifications.Images["ci_passed"])
	assert.Equal(t, "/icons/fail.png", cfg.Notifications.Images["ci_failed"])
}

func TestLoadFile_InvalidNotificationSound(t *testing.T) {
	t.Parallel()

	_, err := config.LoadFile("testdata/invalid_notification_sound.toml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-real-sound")
}

func TestLoadFile_InvalidNotificationMode(t *testing.T) {
	t.Parallel()

	_, err := config.LoadFile("testdata/invalid_notification_mode.toml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notifications.mode")
}

func TestLoadFile_NotificationModeEnvOverride(t *testing.T) {
	t.Setenv("PRONTO_NOTIFICATIONS_MODE", config.NotifyNative)

	cfg, err := config.LoadFile("testdata/notifications_defaults.toml")
	require.NoError(t, err)
	assert.Equal(t, config.NotifyNative, cfg.Notifications.Mode)
}

func TestLoadFile_NotificationsPopupsDisabled(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadFile("testdata/notifications_popups_disabled.toml")
	require.NoError(t, err)

	assert.False(t, cfg.Notifications.Popups)
	assert.False(t, cfg.Notifications.Sound)
}

func TestLoadFile_InvalidTriggers(t *testing.T) {
	t.Parallel()

	t.Run("invalid sound trigger fails validation", func(t *testing.T) {
		t.Parallel()
		_, err := config.LoadFile("testdata/invalid_sound_trigger.toml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown_trigger")
	})

	t.Run("invalid image trigger fails validation", func(t *testing.T) {
		t.Parallel()
		_, err := config.LoadFile("testdata/invalid_image_trigger.toml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad_trigger")
	})
}

func TestExpandPath(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(home, "sounds", "ping.wav"), config.ExpandPath("~/sounds/ping.wav"))
	assert.Equal(t, "/absolute/path.wav", config.ExpandPath("/absolute/path.wav"))
	assert.Equal(t, "relative/path.wav", config.ExpandPath("relative/path.wav"))
	assert.Empty(t, config.ExpandPath(""))
}
