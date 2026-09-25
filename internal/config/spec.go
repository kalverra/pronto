package config

import (
	"slices"

	"github.com/kalverra/pronto/internal/events"
)

// KeySpec describes one pronto configuration key: its TOML path, environment
// override, type, default, allowed values, and documentation. LoadFile,
// Validate, and docs generation (tools/gendocs) all consume Specs, so the
// three can never disagree.
type KeySpec struct {
	// Key is the TOML/viper path, e.g. "notifications.popups".
	Key string
	// Env is the environment variable that overrides the TOML value; empty
	// means no override exists.
	Env string
	// Type describes the value: "string", "bool", "duration", or
	// "map[trigger]string" (keys restricted to notification triggers).
	Type string
	// Default is the value used when nothing is set; nil means no default.
	Default any
	// Valid lists allowed values; nil means free-form.
	Valid []string
	// Doc is a short human-facing description used by generated docs.
	Doc string
}

// Specs describes every supported configuration key, in doc order.
var Specs = []KeySpec{
	{
		Key:     "pr_view",
		Env:     "PRONTO_PR_VIEW",
		Type:    "string",
		Default: ViewCondensed,
		Valid:   []string{ViewCondensed, ViewTerminal, ViewVSCode, ViewWeb, ViewCustom},
		Doc:     "Viewer used to display a pull request.",
	},
	{
		Key:     "pr_view_command",
		Env:     "PRONTO_PR_VIEW_COMMAND",
		Type:    "string",
		Default: "",
		Doc:     "Command template run for pr_view = \"custom\"; {url} is substituted with the PR URL.",
	},
	{
		Key:     "notifications.mode",
		Env:     "PRONTO_NOTIFICATIONS_MODE",
		Type:    "string",
		Default: NotifyNative,
		Valid:   []string{NotifyTerminal, NotifyNative},
		Doc: "Delivery backend for desktop notifications: \"native\" uses the bundled macOS " +
			"notification helper (run `pronto notify setup` once), falling back to \"terminal\" " +
			"(terminal-notifier/osascript) when the helper isn't installed or authorized.",
	},
	{
		Key:     "notifications.popups",
		Env:     "PRONTO_NOTIFICATIONS_POPUPS",
		Type:    "bool",
		Default: true,
		Doc:     "Whether desktop notification popups are enabled.",
	},
	{
		Key:     "notifications.sound",
		Env:     "PRONTO_NOTIFICATIONS_SOUND",
		Type:    "bool",
		Default: false,
		Doc:     "Whether notification sounds are enabled.",
	},
	{
		Key:     "notifications.groups",
		Env:     "PRONTO_NOTIFICATIONS_GROUPS",
		Type:    "[]string",
		Default: []string{GroupFocus, GroupMine},
		Valid:   ValidNotificationGroups,
		Doc:     "PR groups that trigger desktop notifications: \"focus\", \"mine\", \"inbox\".",
	},
	{
		Key:   "notifications.sounds",
		Type:  "map[trigger]string",
		Valid: events.TriggerStrings(),
		Doc: "macOS system sound name per notification trigger (e.g. \"Glass\"); must match a sound " +
			"under /System/Library/Sounds or ~/Library/Sounds, or \"default\" for the OS alert sound.",
	},
	{
		Key:   "notifications.images",
		Type:  "map[trigger]string",
		Valid: events.TriggerStrings(),
		Doc:   "Static image paths per notification trigger.",
	},
	{
		Key:     "focus.authors",
		Env:     "PRONTO_FOCUS_AUTHORS",
		Type:    "[]string",
		Default: []string{},
		Doc:     "PR authors whose pull requests should automatically be focused.",
	},
	{
		Key:     "focus.repos",
		Env:     "PRONTO_FOCUS_REPOS",
		Type:    "[]string",
		Default: []string{},
		Doc:     "Repositories whose pull requests should automatically be focused.",
	},
	{
		Key:  "focus.rules",
		Type: "[]rule",
		Doc:  "Fine-grained auto-focus rules matching PRs by repo, keywords, files, directories, or authors.",
	},
	{
		Key:  "server.poll_interval",
		Env:  "PRONTO_POLL_INTERVAL",
		Type: "duration",
		Doc:  "Daemon poll interval as a Go duration string, e.g. \"30s\"; default 60s; minimum 10s.",
	},
	{
		Key:     "server.pprof_addr",
		Env:     "PRONTO_PPROF_ADDR",
		Type:    "string",
		Default: "",
		Doc:     "Loopback address for the net/http/pprof endpoint in `pronto serve`, e.g. \"localhost:6060\"; empty disables it.",
	},
	{
		Key:     "server.leak_check_interval",
		Env:     "PRONTO_LEAK_CHECK_INTERVAL",
		Type:    "duration",
		Default: "1h",
		Doc:     "Interval between goroutine leak profile checks in `pronto serve` as a Go duration string; \"0\" disables.",
	},
}

// spec returns the KeySpec for a TOML key, or nil when the key is unknown to
// the spec table.
func spec(key string) *KeySpec {
	for i := range Specs {
		if Specs[i].Key == key {
			return &Specs[i]
		}
	}
	return nil
}

// validValue reports whether value is allowed for keys with a Valid list.
func validValue(values []string, value string) bool {
	return slices.Contains(values, value)
}
