package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
)

func TestLoad_DiffConfigRemoved(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PR_VIEW", "")
	t.Setenv("PRONTO_PR_VIEW_COMMAND", "")

	// pr_diff keys are no longer configured; unknown keys are ignored, and
	// the config loads cleanly without them.
	configTOML := `
pr_view = "condensed"
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(configTOML), 0o600)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.ViewCondensed, cfg.PRView)
}
