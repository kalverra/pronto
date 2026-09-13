package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
)

func TestLoad_DiffDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_VIEW", "")
	t.Setenv("PRONTO_PR_DIFF", "")
	t.Setenv("PRONTO_PR_DIFF_COMMAND", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.ViewCondensed, cfg.PRView)
	assert.Equal(t, config.DiffDifftastic, cfg.PRDiff)
	assert.Empty(t, cfg.PRDiffCommand)
}

func TestLoad_TomlDiff(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_VIEW", "")
	t.Setenv("PRONTO_PR_DIFF", "")
	t.Setenv("PRONTO_PR_DIFF_COMMAND", "")

	configTOML := `
pr_view = "condensed"
pr_diff = "zed"
pr_diff_command = "zed --diff"
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(configTOML), 0o600)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.ViewCondensed, cfg.PRView)
	assert.Equal(t, config.DiffZed, cfg.PRDiff)
	assert.Equal(t, "zed --diff", cfg.PRDiffCommand)
}

func TestLoad_DisallowDiffAsPRView(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_VIEW", "")
	t.Setenv("PRONTO_PR_DIFF", "")

	// Setting a diff tool as pr_view is rejected without backwards compatibility
	configTOML := `
pr_view = "difftastic"
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(configTOML), 0o600)
	require.NoError(t, err)

	_, err = config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown pr_view: "difftastic"`)
}

func TestLoad_EnvDiffOverridesFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_DIFF", "vscode")
	t.Setenv("PRONTO_PR_DIFF_COMMAND", "code --diff")

	configTOML := `
pr_diff = "terminal"
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(configTOML), 0o600)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.DiffVSCode, cfg.PRDiff)
	assert.Equal(t, "code --diff", cfg.PRDiffCommand)
}
