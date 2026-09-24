package notify

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// systemSoundsDir is where macOS ships its built-in notification sounds.
const systemSoundsDir = "/System/Library/Sounds"

// userSoundsDir returns the per-user sound library (~/Library/Sounds), or ""
// if the home directory cannot be resolved.
func userSoundsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Sounds")
}

// SystemSounds returns the valid system sound names: macOS's built-in sounds
// under /System/Library/Sounds, plus any user sounds under ~/Library/Sounds.
// Names are the file's base name without its extension, e.g. "Glass". Config
// validation reports this list when a configured sound name is unknown.
func SystemSounds() []string {
	names := map[string]bool{}
	collectSoundNames(systemSoundsDir, names)
	if dir := userSoundsDir(); dir != "" {
		collectSoundNames(dir, names)
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// collectSoundNames adds every sound file's base name (extension stripped)
// found directly under dir into into. A missing or unreadable dir is not an
// error: it simply contributes no names.
func collectSoundNames(dir string, into map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		into[soundBaseName(entry.Name())] = true
	}
}

// soundBaseName strips a sound file's extension, e.g. "Glass.aiff" -> "Glass".
func soundBaseName(filename string) string {
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

// ValidSoundName reports whether name is usable as a system sound: empty (no
// sound configured) or "default" (the OS default alert sound) are always
// valid; otherwise name must contain no path separator or extension, and must
// match an actual file under /System/Library/Sounds or ~/Library/Sounds.
func ValidSoundName(name string) bool {
	if name == "" || name == "default" {
		return true
	}
	if strings.ContainsRune(name, '/') || filepath.Ext(name) != "" {
		return false
	}
	if soundExists(systemSoundsDir, name) {
		return true
	}
	if dir := userSoundsDir(); dir != "" && soundExists(dir, name) {
		return true
	}
	return false
}

// soundExists reports whether dir contains a sound file whose base name
// (extension stripped) equals name.
func soundExists(dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if soundBaseName(entry.Name()) == name {
			return true
		}
	}
	return false
}
