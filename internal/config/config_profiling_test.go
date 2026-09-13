package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
)

func TestLoad_ProfilingDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PPROF_ADDR", "")
	t.Setenv("PRONTO_LEAK_CHECK_INTERVAL", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.Server.PProfAddr, "pprof endpoint off by default")
	assert.Equal(t, "1h", cfg.Server.LeakCheckInterval, "leak check defaults to hourly")
}

func TestLoad_ProfilingEnvOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PPROF_ADDR", "localhost:0")
	t.Setenv("PRONTO_LEAK_CHECK_INTERVAL", "30m")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "localhost:0", cfg.Server.PProfAddr)
	assert.Equal(t, "30m", cfg.Server.LeakCheckInterval)
}

func TestLoad_ProfilingFromToml(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PPROF_ADDR", "")
	t.Setenv("PRONTO_LEAK_CHECK_INTERVAL", "")

	configTOML := `
[server]
pprof_addr = "127.0.0.1:6060"
leak_check_interval = "15m"
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(configTOML), 0o600)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:6060", cfg.Server.PProfAddr)
	assert.Equal(t, "15m", cfg.Server.LeakCheckInterval)
}

func TestLoad_EnvOverridesTomlProfiling(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_PPROF_ADDR", "localhost:0")
	t.Setenv("PRONTO_LEAK_CHECK_INTERVAL", "5m")

	configTOML := `
[server]
pprof_addr = "127.0.0.1:6060"
leak_check_interval = "15m"
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(configTOML), 0o600)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "localhost:0", cfg.Server.PProfAddr)
	assert.Equal(t, "5m", cfg.Server.LeakCheckInterval)
}

func TestLoad_InvalidLeakCheckIntervalError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_LEAK_CHECK_INTERVAL", "not-a-duration")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "leak_check_interval")
}

func TestLoad_DisabledLeakCheckIntervalIsValid(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	t.Setenv("PRONTO_LEAK_CHECK_INTERVAL", "0")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "0", cfg.Server.LeakCheckInterval)
}
