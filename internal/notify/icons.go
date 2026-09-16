package notify

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

//go:embed icons/*.png
var iconFS embed.FS

// defaultTriggerImages holds the embedded icon per trigger. Triggers without
// an entry here resolve icons another way (see reviewStateImages) or send
// imageless notifications.
var defaultTriggerImages = map[Trigger]string{
	TriggerCIPassed: "icons/ci-passed.png",
	TriggerCIFailed: "icons/ci-failed.png",
	TriggerConflict: "icons/conflict.png",
	TriggerPRMerged: "icons/merged.png",
}

// reviewStateImages maps review states to their icon. Review notifications are
// state-specific rather than trigger-specific, so the icon is picked by
// Notification.ReviewState when the trigger is TriggerReviewReceived.
var reviewStateImages = map[string]string{
	"APPROVED":          "icons/approved.png",
	"CHANGES_REQUESTED": "icons/rejected.png",
	"COMMENTED":         "icons/comment.png",
}

// iconCacheDir is where embedded icons are materialized on disk. Notification
// backends take a file path, not bytes, so embed.FS alone cannot deliver them.
var iconCacheDir string

// extractIconsOnce guards the one-time extraction of embedded icons.
var extractIconsOnce sync.Once

// DefaultTriggerImages returns per-trigger icon file paths for notifications.
// The embedded icons are extracted to a cache directory on first call; paths
// are empty for triggers without a default icon or if extraction fails.
func DefaultTriggerImages() map[Trigger]string {
	extractIconsOnce.Do(extractIcons)
	if iconCacheDir == "" {
		return map[Trigger]string{}
	}
	images := make(map[Trigger]string, len(defaultTriggerImages))
	for trigger, icon := range defaultTriggerImages {
		images[trigger] = filepath.Join(iconCacheDir, filepath.Base(icon))
	}
	return images
}

// DefaultTriggerImage resolves the default icon path for a notification.
func DefaultTriggerImage(n Notification) string {
	extractIconsOnce.Do(extractIcons)
	if iconCacheDir == "" {
		return ""
	}
	icon := defaultTriggerImages[n.Trigger]
	if n.Trigger == TriggerReviewReceived {
		if stateIcon, ok := reviewStateImages[n.ReviewState]; ok {
			icon = stateIcon
		} else {
			icon = reviewStateImages["COMMENTED"]
		}
	}
	if icon == "" {
		return ""
	}
	return filepath.Join(iconCacheDir, filepath.Base(icon))
}

// defaultTriggerImage resolves the default icon path for a notification.
func defaultTriggerImage(n Notification) string {
	return DefaultTriggerImage(n)
}

// extractIcons writes embedded icons to a cache directory, replacing the
// embedded source of truth on every call so icon updates propagate.
func extractIcons() {
	base, err := os.UserCacheDir()
	if err != nil {
		return
	}
	iconCacheDir = filepath.Join(base, "pronto", "icons")
	if err := os.MkdirAll(iconCacheDir, 0o700); err != nil {
		iconCacheDir = ""
		return
	}
	icons, err := fs.Glob(iconFS, "icons/*.png")
	if err != nil {
		iconCacheDir = ""
		return
	}
	for _, icon := range icons {
		if err := extractIcon(icon); err != nil {
			iconCacheDir = ""
			return
		}
	}
}

// extractIcon writes a single embedded icon into the icon cache directory.
func extractIcon(icon string) error {
	data, err := fs.ReadFile(iconFS, icon)
	if err != nil {
		return fmt.Errorf("read embedded icon %s: %w", icon, err)
	}
	path := filepath.Join(iconCacheDir, filepath.Base(icon))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write icon %s: %w", path, err)
	}
	return nil
}
