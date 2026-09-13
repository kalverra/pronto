package logging_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/logging"
)

func TestLogPath_Precedence(t *testing.T) {
	t.Run("PRONTO_LOG_FILE overrides all", func(t *testing.T) {
		custom := filepath.Join(t.TempDir(), "custom.log")
		t.Setenv("PRONTO_LOG_FILE", custom)
		t.Setenv("PRONTO_CONFIG_DIR", "/should/be/ignored")
		t.Setenv("XDG_CONFIG_HOME", "/also/ignored")

		assert.Equal(t, custom, logging.LogPath())
	})

	t.Run("PRONTO_CONFIG_DIR overrides default config dir", func(t *testing.T) {
		t.Setenv("PRONTO_LOG_FILE", "")
		cfgDir := filepath.Join(t.TempDir(), "pronto-cfg")
		t.Setenv("PRONTO_CONFIG_DIR", cfgDir)

		assert.Equal(t, filepath.Join(cfgDir, "pronto.jsonl"), logging.LogPath())
	})

	t.Run("XDG_CONFIG_HOME used when set", func(t *testing.T) {
		t.Setenv("PRONTO_LOG_FILE", "")
		t.Setenv("PRONTO_CONFIG_DIR", "")
		xdgDir := filepath.Join(t.TempDir(), "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdgDir)

		assert.Equal(t, filepath.Join(xdgDir, "pronto", "pronto.jsonl"), logging.LogPath())
	})

	t.Run("default uses dot-config or UserConfigDir", func(t *testing.T) {
		t.Setenv("PRONTO_LOG_FILE", "")
		t.Setenv("PRONTO_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "")

		path := logging.LogPath()
		assert.NotEmpty(t, path)
		assert.True(t, filepath.IsAbs(path), "path must be absolute")
		assert.Equal(t, "pronto.jsonl", filepath.Base(path))
	})
}

func TestOpen_CreatesDirectoryAndFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "nested", "dir", "test.log")
	t.Setenv("PRONTO_LOG_FILE", logPath)

	f, err := logging.Open()
	require.NoError(t, err)
	require.NotNil(t, f)
	t.Cleanup(func() {
		_ = f.Close()
	})

	assert.FileExists(t, logPath)
}

func TestOpen_RotatesPriorLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "pronto.jsonl")
	t.Setenv("PRONTO_LOG_FILE", logPath)

	// First session writes to log
	f1, err := logging.Open()
	require.NoError(t, err)
	_, err = f1.WriteString("{\"msg\":\"session 1 logs\"}\n")
	require.NoError(t, err)
	require.NoError(t, f1.Close())

	// Second session opens log
	f2, err := logging.Open()
	require.NoError(t, err)
	_, err = f2.WriteString("{\"msg\":\"session 2 logs\"}\n")
	require.NoError(t, err)
	require.NoError(t, f2.Close())

	oldPath := logPath + ".old"
	assert.FileExists(t, oldPath, "prior log should be rotated to .old")

	// #nosec G304 — test files in TempDir.
	oldContent, err := os.ReadFile(oldPath)
	require.NoError(t, err)
	assert.Contains(t, string(oldContent), "session 1 logs")
	assert.NotContains(t, string(oldContent), "session 2 logs")

	// #nosec G304 — test files in TempDir.
	newContent, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(newContent), "session 2 logs")
	assert.NotContains(t, string(newContent), "session 1 logs")
}

func TestNew_DefaultInfoLevel(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "default_level_test.jsonl")
	t.Setenv("PRONTO_LOG_FILE", logPath)
	t.Setenv("PRONTO_LOG_LEVEL", "")

	logger, closer, err := logging.New()
	require.NoError(t, err)
	require.NotNil(t, closer)
	t.Cleanup(func() {
		_ = closer.Close()
	})

	assert.Equal(t, zerolog.InfoLevel, logger.GetLevel(), "default level must be info")

	logger.Debug().Msg("should be filtered")
	logger.Info().Msg("should be logged")

	// #nosec G304 — test log file created in TempDir.
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "should be filtered")
	assert.Contains(t, string(content), "should be logged")
}

func TestNew_DebugLevelWritesToFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "debug_test.jsonl")
	t.Setenv("PRONTO_LOG_FILE", logPath)

	logger, closer, err := logging.New(zerolog.DebugLevel)
	require.NoError(t, err)
	require.NotNil(t, closer)
	t.Cleanup(func() {
		_ = closer.Close()
	})

	assert.Equal(t, zerolog.DebugLevel, logger.GetLevel(), "logger must allow debug level logs")

	logger.Debug().Str("key", "val").Msg("debug test message")
	logger.Info().Msg("info test message")

	// #nosec G304 — test log file created in TempDir.
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Len(t, lines, 2)

	var entry1 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry1), "line 1 must be valid json")
	assert.Equal(t, "debug", entry1["level"])
	assert.Equal(t, "debug test message", entry1["message"])
	assert.Equal(t, "val", entry1["key"])

	var entry2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &entry2), "line 2 must be valid json")
	assert.Equal(t, "info", entry2["level"])
	assert.Equal(t, "info test message", entry2["message"])
}

func TestNew_FallbackOnInvalidPath(t *testing.T) {
	// Point to an invalid path where a directory cannot be created (e.g. existing file as parent)
	tmpFile := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(tmpFile, []byte("x"), 0o600))
	t.Setenv("PRONTO_LOG_FILE", filepath.Join(tmpFile, "cannot-create", "pronto.jsonl"))

	logger, closer, err := logging.New()
	require.Error(t, err)
	if closer != nil {
		t.Cleanup(func() {
			_ = closer.Close()
		})
	}
	logger.Info().Msg("should not panic")
}

func TestNew_TimestampIncludesMilliseconds(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "timestamp_test.jsonl")
	t.Setenv("PRONTO_LOG_FILE", logPath)

	logger, closer, err := logging.New()
	require.NoError(t, err)
	require.NotNil(t, closer)
	t.Cleanup(func() {
		_ = closer.Close()
	})

	logger.Info().Msg("timestamp check")

	// #nosec G304 — test log file created in TempDir.
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	var entry map[string]any
	require.NoError(t, json.Unmarshal(content, &entry), "log entry must be valid json")
	timeStr, ok := entry["time"].(string)
	require.True(t, ok, "time field must be string")

	// Verify timestamp contains milliseconds without timezone (e.g. 2006-01-02 15:04:05.000)
	matched, err := regexp.MatchString(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}$`, timeStr)
	require.NoError(t, err)
	assert.True(t, matched, "log timestamp must include milliseconds without timezone, got: %s", timeStr)
}
