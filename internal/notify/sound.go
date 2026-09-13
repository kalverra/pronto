package notify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SoundPlayer plays an audio file or system sound.
type SoundPlayer interface {
	Play(ctx context.Context, sound string) error
}

// MacSoundPlayerOption configures a MacSoundPlayer.
type MacSoundPlayerOption func(*MacSoundPlayer)

// MacSoundPlayer plays audio files on macOS using afplay.
type MacSoundPlayer struct {
	runner       CommandRunner
	playerPath   string
	defaultSound string
	async        bool
}

// WithSoundPlayerRunner overrides the command executor for testing.
func WithSoundPlayerRunner(runner CommandRunner) MacSoundPlayerOption {
	return func(m *MacSoundPlayer) {
		m.runner = runner
	}
}

// WithSoundPlayerPath overrides the path to the audio player executable.
func WithSoundPlayerPath(path string) MacSoundPlayerOption {
	return func(m *MacSoundPlayer) {
		m.playerPath = path
	}
}

// WithDefaultSound overrides the fallback sound file path.
func WithDefaultSound(sound string) MacSoundPlayerOption {
	return func(m *MacSoundPlayer) {
		m.defaultSound = sound
	}
}

// WithSoundPlayerAsync controls whether playback runs asynchronously in a background goroutine.
func WithSoundPlayerAsync(async bool) MacSoundPlayerOption {
	return func(m *MacSoundPlayer) {
		m.async = async
	}
}

// NewMacSoundPlayer creates an initialized MacSoundPlayer.
func NewMacSoundPlayer(opts ...MacSoundPlayerOption) *MacSoundPlayer {
	m := &MacSoundPlayer{
		defaultSound: "/System/Library/Sounds/Ping.aiff",
		runner:       defaultRunner,
	}
	if path, err := exec.LookPath("afplay"); err == nil {
		m.playerPath = path
	} else {
		m.playerPath = "/usr/bin/afplay"
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func expandHome(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// Play resolves and plays the specified audio file or system sound name.
func (m *MacSoundPlayer) Play(ctx context.Context, sound string) error {
	runner := m.runner
	if runner == nil {
		runner = defaultRunner
	}

	sound = strings.TrimSpace(sound)
	switch {
	case sound == "" || sound == "default":
		sound = m.defaultSound
	case !strings.Contains(sound, "/") && filepath.Ext(sound) == "":
		candidate := filepath.Join("/System/Library/Sounds", sound+".aiff")
		sound = candidate
	default:
		sound = expandHome(sound)
	}

	if m.playerPath == "" {
		return nil
	}

	if m.async {
		go func() {
			_ = runner(ctx, m.playerPath, sound)
		}()
		return nil
	}

	return runner(ctx, m.playerPath, sound)
}

// SoundNotifier delivers sound notifications via a SoundPlayer.
type SoundNotifier struct {
	player  SoundPlayer
	enabled bool
}

// NewSoundNotifier creates an initialized SoundNotifier.
func NewSoundNotifier(player SoundPlayer, enabled bool) *SoundNotifier {
	return &SoundNotifier{
		player:  player,
		enabled: enabled,
	}
}

// Notify delivers a sound for the given notification if enabled.
func (s *SoundNotifier) Notify(ctx context.Context, n Notification) error {
	if !s.enabled || s.player == nil {
		return nil
	}
	return s.player.Play(ctx, n.SoundPath)
}
