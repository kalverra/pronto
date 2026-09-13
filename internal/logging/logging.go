// Package logging provides centralized file-based logging for pronto.
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"
)

func init() {
	zerolog.TimeFieldFormat = "2006-01-02 15:04:05.000"
}

// LogPath returns the resolved absolute path to the pronto log file.
// Resolution order:
// 1. PRONTO_LOG_FILE environment variable
// 2. PRONTO_CONFIG_DIR environment variable + "/pronto.jsonl"
// 3. XDG_CONFIG_HOME environment variable + "/pronto/pronto.jsonl"
// 4. ~/.config/pronto/pronto.jsonl (if ~/.config exists)
// 5. os.UserConfigDir()/pronto/pronto.jsonl
func LogPath() string {
	if file := os.Getenv("PRONTO_LOG_FILE"); file != "" {
		return file
	}
	if dir := os.Getenv("PRONTO_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "pronto.jsonl")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pronto", "pronto.jsonl")
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dotConfig := filepath.Join(home, ".config")
		if stat, err := os.Stat(dotConfig); err == nil && stat.IsDir() {
			return filepath.Join(dotConfig, "pronto", "pronto.jsonl")
		}
	}

	if base, err := os.UserConfigDir(); err == nil && base != "" {
		return filepath.Join(base, "pronto", "pronto.jsonl")
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".pronto", "pronto.jsonl")
	}

	return filepath.Clean("pronto.jsonl")
}

// Open opens or creates the log file, ensuring parent directories exist.
// If an existing log file is present, it is rotated to <path>.old.
func Open() (*os.File, error) {
	path := LogPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log dir %q: %w", dir, err)
	}

	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, path+".old"); err != nil {
			return nil, fmt.Errorf("rotate log file %q: %w", path, err)
		}
	}

	// #nosec G304 — path is resolved internally from config/env.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", path, err)
	}
	return f, nil
}

func resolveLevel(levels ...zerolog.Level) zerolog.Level {
	if len(levels) > 0 {
		return levels[0]
	}
	if lvlStr := os.Getenv("PRONTO_LOG_LEVEL"); lvlStr != "" {
		if lvl, err := zerolog.ParseLevel(lvlStr); err == nil {
			return lvl
		}
	}
	return zerolog.InfoLevel
}

// New creates a zerolog.Logger writing JSONL to the resolved log file.
// Defaults to InfoLevel unless specified via parameter or PRONTO_LOG_LEVEL.
// If opening the file fails, it returns a discarded fallback logger along with the error.
func New(levels ...zerolog.Level) (zerolog.Logger, io.Closer, error) {
	level := resolveLevel(levels...)
	f, err := Open()
	if err != nil {
		return zerolog.New(io.Discard).Level(level), nil, err
	}

	logger := zerolog.New(f).
		Level(level).
		With().
		Timestamp().
		Logger()

	return logger, f, nil
}
