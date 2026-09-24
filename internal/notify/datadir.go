package notify

import (
	"os"
	"path/filepath"
)

// dataDir resolves pronto's per-user data directory: $XDG_DATA_HOME/pronto,
// or ~/.local/share/pronto. Both the native helper installer and discovery
// need this location, so it lives in one place. Returns "" if the home
// directory cannot be resolved.
func dataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "pronto")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "pronto")
	}
	return ""
}
