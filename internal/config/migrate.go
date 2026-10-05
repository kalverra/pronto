package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
)

var (
	terminalModeRE = regexp.MustCompile(`(?m)^(\s*mode\s*=\s*)["']terminal["']`)
	popupsFalseRE  = regexp.MustCompile(`(?m)^(\s*popups\s*=\s*)false\b`)
)

// MigrationResult describes the updates applied to pronto.toml.
type MigrationResult struct {
	ModeChanged   bool
	PopupsEnabled bool
}

// MigrateNotificationConfig inspects the config file at path (defaulting to
// Path() if empty) and replaces any legacy notification settings:
// - Replaces mode = "terminal" with mode = "native"
// - Replaces popups = false with popups = true
//
// All other file contents, formatting, and comments are preserved. If the file
// does not exist, an empty result is returned without error.
func MigrateNotificationConfig(path string) (MigrationResult, error) {
	if path == "" {
		path = Path()
	}

	// #nosec G304 -- configuration file path is trusted local path.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return MigrationResult{}, nil
		}
		return MigrationResult{}, fmt.Errorf("read config: %w", err)
	}

	var res MigrationResult
	updated := data

	if terminalModeRE.Match(updated) {
		updated = terminalModeRE.ReplaceAll(updated, []byte(`${1}"native"`))
		res.ModeChanged = true
	}

	if popupsFalseRE.Match(updated) {
		updated = popupsFalseRE.ReplaceAll(updated, []byte(`${1}true`))
		res.PopupsEnabled = true
	}

	if !res.ModeChanged && !res.PopupsEnabled {
		return res, nil
	}

	// #nosec G306,G703 -- configuration file created/modified with user-read-write permissions; path is local config path.
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		return res, fmt.Errorf("write migrated config: %w", err)
	}

	return res, nil
}
