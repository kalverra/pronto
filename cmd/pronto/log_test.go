package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultLogger_DoesNotLogToStderr(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "pronto.jsonl")
	t.Setenv("PRONTO_LOG_FILE", logFile)

	var stderr bytes.Buffer
	logger, closer := defaultLogger()
	require.NotNil(t, logger)
	if closer != nil {
		defer func() {
			_ = closer.Close()
		}()
	}

	logger.Info().Msg("testing default logger output")

	assert.Empty(t, stderr.String(), "logs must not go to stderr")
	assert.FileExists(t, logFile)
}
