package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
)

func TestMigrateNotificationConfig_MissingFileReturnsNil(t *testing.T) {
	t.Parallel()

	res, err := config.MigrateNotificationConfig(filepath.Join(t.TempDir(), "nonexistent.toml"))
	require.NoError(t, err)
	assert.False(t, res.ModeChanged)
	assert.False(t, res.PopupsEnabled)
}

func TestMigrateNotificationConfig_ReplacesTerminalModeAndEnablesPopups(t *testing.T) {
	t.Parallel()

	content := `# User pronto configuration
pr_view = "condensed"

[notifications]
mode = "terminal"
popups = false
sound = true
`
	file := filepath.Join(t.TempDir(), "pronto.toml")
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))

	res, err := config.MigrateNotificationConfig(file)
	require.NoError(t, err)
	assert.True(t, res.ModeChanged)
	assert.True(t, res.PopupsEnabled)

	// #nosec G304 -- test file path.
	data, err := os.ReadFile(file)
	require.NoError(t, err)

	updated := string(data)
	assert.Contains(t, updated, `mode = "native"`)
	assert.Contains(t, updated, `popups = true`)
	assert.Contains(t, updated, `sound = true`)
	assert.Contains(t, updated, `# User pronto configuration`)
	assert.NotContains(t, updated, `"terminal"`)
	assert.NotContains(t, updated, `popups = false`)
}

func TestMigrateNotificationConfig_SingleQuotedTerminalMode(t *testing.T) {
	t.Parallel()

	content := `[notifications]
mode = 'terminal'
`
	file := filepath.Join(t.TempDir(), "pronto.toml")
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))

	res, err := config.MigrateNotificationConfig(file)
	require.NoError(t, err)
	assert.True(t, res.ModeChanged)
	assert.False(t, res.PopupsEnabled)

	// #nosec G304 -- test file path.
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, string(data), `mode = "native"`)
}

func TestMigrateNotificationConfig_UnmodifiedWhenAlreadyNative(t *testing.T) {
	t.Parallel()

	content := `[notifications]
mode = "native"
popups = true
`
	file := filepath.Join(t.TempDir(), "pronto.toml")
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))

	res, err := config.MigrateNotificationConfig(file)
	require.NoError(t, err)
	assert.False(t, res.ModeChanged)
	assert.False(t, res.PopupsEnabled)

	// #nosec G304 -- test file path.
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestMigrateNotificationConfig_PreservesExplicitPopupsFalseWhenAlreadyNative(t *testing.T) {
	t.Parallel()

	content := `[notifications]
mode = "native"
popups = false
sound = true
`
	file := filepath.Join(t.TempDir(), "pronto.toml")
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))

	res, err := config.MigrateNotificationConfig(file)
	require.NoError(t, err)
	assert.False(t, res.ModeChanged)
	assert.False(t, res.PopupsEnabled)

	// #nosec G304 -- test file path.
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}
