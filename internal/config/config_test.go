package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
)

func TestPath_ReturnsTomlPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	assert.Equal(t, filepath.Join(tmpDir, "pronto.toml"), config.Path())
}

func TestLoad_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_VIEW", "")
	t.Setenv("PRONTO_PR_VIEW_COMMAND", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.ViewCondensed, cfg.PRView)
	assert.Empty(t, cfg.PRViewCommand)
}

func TestLoad_TomlParse(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_VIEW", "")
	t.Setenv("PRONTO_PR_VIEW_COMMAND", "")

	configTOML := `
pr_view = "vscode"
pr_view_command = "code --open-url {url}"
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(configTOML), 0o600)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.ViewVSCode, cfg.PRView)
	assert.Equal(t, "code --open-url {url}", cfg.PRViewCommand)
}

func TestLoad_InvalidTomlSyntaxError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)

	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte("invalid = ["), 0o600)
	require.NoError(t, err)

	_, err = config.Load()
	require.Error(t, err)
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_VIEW", "vscode")
	t.Setenv("PRONTO_PR_VIEW_COMMAND", "code --open-url")

	configTOML := `
pr_view = "terminal"
pr_view_command = "gh pr view"
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(configTOML), 0o600)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.ViewVSCode, cfg.PRView)
	assert.Equal(t, "code --open-url", cfg.PRViewCommand)
}

func TestLoadFile_ExplicitPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_PR_VIEW", "")
	t.Setenv("PRONTO_PR_VIEW_COMMAND", "")

	customPath := filepath.Join(tmpDir, "custom.toml")
	configTOML := `
pr_view = "terminal"
`
	err := os.WriteFile(customPath, []byte(configTOML), 0o600)
	require.NoError(t, err)

	cfg, err := config.LoadFile(customPath)
	require.NoError(t, err)
	assert.Equal(t, config.ViewTerminal, cfg.PRView)
}

func TestLoad_UnknownViewError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_VIEW", "invalid-view")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown pr_view")
}

func TestLoad_CustomWithoutCommandError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_VIEW", "custom")
	t.Setenv("PRONTO_PR_VIEW_COMMAND", "")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "custom")
}
