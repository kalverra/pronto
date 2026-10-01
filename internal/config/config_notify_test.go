package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

func TestLoadFile_NotificationsDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadFile("testdata/notifications_defaults.toml")
	require.NoError(t, err)
	assert.True(t, cfg.Notifications.Popups, "popups should default to true")
	assert.False(t, cfg.Notifications.Sound, "sound should default to false")
	assert.Equal(t, config.NotifyNative, cfg.Notifications.Mode, "mode should default to native")
	assert.Empty(t, cfg.Notifications.Sounds)
	assert.Empty(t, cfg.Notifications.Images)
}

func triggerNames(set notify.TriggerSet) []string {
	out := make([]string, 0, len(set))
	for t, ok := range set {
		if ok {
			out = append(out, string(t))
		}
	}
	return out
}

func allTriggersExcept(except ...notify.Trigger) []string {
	var out []string
	for _, t := range events.TriggerTypes {
		if !slices.Contains(except, t) {
			out = append(out, string(t))
		}
	}
	return out
}

func TestLoad_NotificationPolicyDefaults(t *testing.T) {
	t.Setenv("PRONTO_CONFIG_DIR", t.TempDir())

	cfg, err := config.Load()
	require.NoError(t, err)
	policy := cfg.Notifications.Policy()

	tab := func(t notify.Tab) notify.Scope { return notify.Scope{Tab: t} }
	sec := func(t notify.Tab, s model.Section) notify.Scope { return notify.Scope{Tab: t, Section: s} }

	assert.ElementsMatch(t, allTriggersExcept(notify.TriggerEntered), triggerNames(policy[tab(notify.TabFocus)]))
	assert.ElementsMatch(t, allTriggersExcept(notify.TriggerEntered),
		triggerNames(policy[sec(notify.TabFocus, model.SectionBlocked)]), "focus sections inherit")
	assert.ElementsMatch(t, allTriggersExcept(notify.TriggerNewCommits, notify.TriggerEntered),
		triggerNames(policy[tab(notify.TabMine)]))
	assert.ElementsMatch(t, allTriggersExcept(notify.TriggerNewCommits, notify.TriggerEntered),
		triggerNames(policy[sec(notify.TabMine, model.SectionReadyToMerge)]))

	assert.ElementsMatch(t, []string{"entered"}, triggerNames(policy[tab(notify.TabPriority)]))
	assert.ElementsMatch(t, []string{"entered"}, triggerNames(policy[sec(notify.TabPriority, model.SectionAttention)]))
	assert.Empty(t, triggerNames(policy[sec(notify.TabPriority, model.SectionBlocked)]))
	assert.Empty(t, triggerNames(policy[sec(notify.TabPriority, model.SectionStale)]))

	for _, sc := range append([]notify.Scope{tab(notify.TabInbox)}, sec(notify.TabInbox, model.SectionAttention),
		sec(notify.TabInbox, model.SectionBlocked), sec(notify.TabInbox, model.SectionStale)) {
		assert.Empty(t, triggerNames(policy[sc]), "inbox scope %v", sc)
	}
}

func TestZeroValueNotificationConfig_UsesDefaultPolicy(t *testing.T) {
	t.Setenv("PRONTO_CONFIG_DIR", t.TempDir())
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, cfg.Notifications.Policy(), config.NotificationConfig{}.Policy())
}

func loadNotifyTOML(t *testing.T, content string) (config.Config, error) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pronto.toml"), []byte(content), 0o600))
	return config.Load()
}

func TestLoad_NotificationPolicyTokens(t *testing.T) { //nolint:paralleltest // loadNotifyTOML sets env
	cfg, err := loadNotifyTOML(t, `
[notifications.mine]
triggers = ["ci_failed", "review_received"]
in_review = ["inherit", "!review_received", "pr_merged"]
drafts = []
ready_to_merge = ["all"]

[notifications.inbox]
triggers = ["pr_merged"]
`)
	require.NoError(t, err)
	p := cfg.Notifications.Policy()

	mine := notify.Scope{Tab: notify.TabMine}
	assert.ElementsMatch(t, []string{"ci_failed", "review_received"}, triggerNames(p[mine]))
	assert.ElementsMatch(t, []string{"ci_failed", "pr_merged"},
		triggerNames(p[notify.Scope{Tab: notify.TabMine, Section: model.SectionInReview}]))
	assert.Empty(t, triggerNames(p[notify.Scope{Tab: notify.TabMine, Section: model.SectionDrafts}]),
		"explicit [] is empty, not default")
	assert.ElementsMatch(t, events.TriggerStrings(),
		triggerNames(p[notify.Scope{Tab: notify.TabMine, Section: model.SectionReadyToMerge}]))
	assert.ElementsMatch(t, []string{"ci_failed", "review_received"},
		triggerNames(p[notify.Scope{Tab: notify.TabMine, Section: model.SectionStale}]), "unset section inherits")
	assert.ElementsMatch(t, []string{"pr_merged"}, triggerNames(p[notify.Scope{Tab: notify.TabInbox}]))
}

func TestLoad_NotificationPolicyEmptyTabTriggers(t *testing.T) { //nolint:paralleltest // loadNotifyTOML sets env
	cfg, err := loadNotifyTOML(t, "[notifications.mine]\ntriggers = []\n")
	require.NoError(t, err)
	assert.Empty(t, triggerNames(cfg.Notifications.Policy()[notify.Scope{Tab: notify.TabMine}]))
}

func TestLoad_NotificationPolicyEnvOverride(t *testing.T) {
	t.Setenv("PRONTO_NOTIFICATIONS_MINE_TRIGGERS", "all,!new_commits,!entered,!ci_passed")
	cfg, err := loadNotifyTOML(t, "")
	require.NoError(t, err)
	assert.ElementsMatch(t,
		allTriggersExcept(notify.TriggerNewCommits, notify.TriggerEntered, notify.TriggerCIPassed),
		triggerNames(cfg.Notifications.Policy()[notify.Scope{Tab: notify.TabMine}]))
}

func TestLoad_NotificationPolicyInvalid(t *testing.T) { //nolint:paralleltest // loadNotifyTOML sets env
	cases := map[string]struct{ toml, wantErr string }{
		"unknown token": {
			"[notifications.priority]\nblocked = [\"foo\"]\n",
			`notifications.priority.blocked: unknown trigger token "foo"`,
		},
		"inherit at tab level": {
			"[notifications.focus]\ntriggers = [\"inherit\"]\n",
			"notifications.focus.triggers",
		},
		"negated all": {
			"[notifications.mine]\ntriggers = [\"!all\"]\n",
			"notifications.mine.triggers",
		},
		"legacy groups": {
			"[notifications]\ngroups = [\"focus\"]\n",
			"notifications.groups was removed",
		},
	}
	for name, tc := range cases { //nolint:paralleltest // loadNotifyTOML sets env
		t.Run(name, func(t *testing.T) {
			_, err := loadNotifyTOML(t, tc.toml)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLoadFile_NotificationsFull(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadFile("testdata/notifications_full.toml")
	require.NoError(t, err)

	assert.False(t, cfg.Notifications.Popups)
	assert.True(t, cfg.Notifications.Sound)
	assert.Equal(t, config.NotifyNative, cfg.Notifications.Mode)
	assert.Equal(t, "Glass", cfg.Notifications.Sounds["ci_passed"])
	assert.Equal(t, "Basso", cfg.Notifications.Sounds["ci_failed"])
	assert.Equal(t, "/icons/pass.png", cfg.Notifications.Images["ci_passed"])
	assert.Equal(t, "/icons/fail.png", cfg.Notifications.Images["ci_failed"])
}

func TestLoadFile_InvalidNotificationSound(t *testing.T) {
	t.Parallel()

	_, err := config.LoadFile("testdata/invalid_notification_sound.toml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-real-sound")
}

func TestLoadFile_InvalidNotificationMode(t *testing.T) {
	t.Parallel()

	_, err := config.LoadFile("testdata/invalid_notification_mode.toml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notifications.mode")
}

func TestLoadFile_NotificationModeEnvOverride(t *testing.T) {
	t.Setenv("PRONTO_NOTIFICATIONS_MODE", config.NotifyNative)

	cfg, err := config.LoadFile("testdata/notifications_defaults.toml")
	require.NoError(t, err)
	assert.Equal(t, config.NotifyNative, cfg.Notifications.Mode)
}

func TestLoadFile_NotificationsPopupsDisabled(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadFile("testdata/notifications_popups_disabled.toml")
	require.NoError(t, err)

	assert.False(t, cfg.Notifications.Popups)
	assert.False(t, cfg.Notifications.Sound)
}

func TestLoadFile_InvalidTriggers(t *testing.T) {
	t.Parallel()

	t.Run("invalid sound trigger fails validation", func(t *testing.T) {
		t.Parallel()
		_, err := config.LoadFile("testdata/invalid_sound_trigger.toml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown_trigger")
	})

	t.Run("invalid image trigger fails validation", func(t *testing.T) {
		t.Parallel()
		_, err := config.LoadFile("testdata/invalid_image_trigger.toml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad_trigger")
	})
}

func TestExpandPath(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(home, "sounds", "ping.wav"), config.ExpandPath("~/sounds/ping.wav"))
	assert.Equal(t, "/absolute/path.wav", config.ExpandPath("/absolute/path.wav"))
	assert.Equal(t, "relative/path.wav", config.ExpandPath("relative/path.wav"))
	assert.Empty(t, config.ExpandPath(""))
}

func TestNotificationPolicy_MergeQueueKickedOutAlias(t *testing.T) {
	t.Parallel()

	cfg := config.NotificationConfig{
		Mine: config.MineNotify{
			Triggers: []string{"merge_queue_kicked_out"},
		},
	}
	policy := cfg.Policy()
	assert.True(t, policy[notify.Scope{Tab: notify.TabMine}][notify.TriggerMergeQueueLeft])
}
