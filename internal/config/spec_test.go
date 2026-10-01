package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/notify"
)

func TestSpecs_DescribeEveryConfigKey(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, config.Specs)

	got := map[string]config.KeySpec{}
	for _, spec := range config.Specs {
		assert.NotEmpty(t, spec.Key)
		assert.NotEmpty(t, spec.Type, "%s needs a type", spec.Key)
		assert.NotEmpty(t, spec.Doc, "%s needs doc text", spec.Key)
		got[spec.Key] = spec
	}

	// Every key LoadFile binds, with its env override.
	wantEnv := map[string]string{
		"pr_view":                         "PRONTO_PR_VIEW",
		"pr_view_command":                 "PRONTO_PR_VIEW_COMMAND",
		"notifications.mode":              "PRONTO_NOTIFICATIONS_MODE",
		"notifications.popups":            "PRONTO_NOTIFICATIONS_POPUPS",
		"notifications.sound":             "PRONTO_NOTIFICATIONS_SOUND",
		"notifications.focus.triggers":    "PRONTO_NOTIFICATIONS_FOCUS_TRIGGERS",
		"notifications.mine.triggers":     "PRONTO_NOTIFICATIONS_MINE_TRIGGERS",
		"notifications.priority.triggers": "PRONTO_NOTIFICATIONS_PRIORITY_TRIGGERS",
		"notifications.inbox.triggers":    "PRONTO_NOTIFICATIONS_INBOX_TRIGGERS",
		"focus.authors":                   "PRONTO_FOCUS_AUTHORS",
		"focus.repos":                     "PRONTO_FOCUS_REPOS",
		"server.poll_interval":            "PRONTO_POLL_INTERVAL",
		"server.hot_interval":             "PRONTO_HOT_INTERVAL",
		"server.idle_interval":            "PRONTO_IDLE_INTERVAL",
		"server.pprof_addr":               "PRONTO_PPROF_ADDR",
		"server.leak_check_interval":      "PRONTO_LEAK_CHECK_INTERVAL",
	}
	for key, env := range wantEnv {
		spec, ok := got[key]
		if assert.True(t, ok, "spec missing for key %s", key) {
			assert.Equal(t, env, spec.Env, "%s env override", key)
		}
	}

	// Keys without env overrides still need specs.
	for _, key := range []string{"notifications.sounds", "notifications.images"} {
		assert.Contains(t, got, key, "spec missing for key %s", key)
	}

	// Diff configuration was removed entirely.
	for _, key := range []string{"pr_diff", "pr_diff_command", "diff.scan_root", "diff.repos"} {
		assert.NotContains(t, got, key, "removed diff key must not have a spec: %s", key)
	}

	// Defaults must match LoadFile behavior.
	assert.Equal(t, config.ViewCondensed, got["pr_view"].Default)
	assert.Equal(t, config.NotifyNative, got["notifications.mode"].Default)
	assert.Equal(t, true, got["notifications.popups"].Default)
	assert.Equal(t, false, got["notifications.sound"].Default)
	assert.NotContains(t, got, "notifications.groups")
	assert.Equal(t, []string{"all", "!entered"}, got["notifications.focus.triggers"].Default)
	assert.Equal(t, []string{"all", "!new_commits", "!entered"}, got["notifications.mine.triggers"].Default)
	assert.Equal(t, []string{"entered"}, got["notifications.priority.triggers"].Default)
	assert.Equal(t, []string{}, got["notifications.inbox.triggers"].Default)
	assert.Equal(t, []string{"inherit", "!entered"}, got["notifications.priority.blocked"].Default)
	assert.Equal(t, []string{"inherit"}, got["notifications.mine.in_review"].Default)
	assert.Contains(t, got, "notifications.focus.action_required")
	assert.NotContains(t, got, "notifications.mine.attention", "mine has no attention section")
	assert.Empty(t, got["pr_view_command"].Default)
	assert.Empty(t, got["pr_diff_command"].Default)
	assert.Empty(t, got["server.pprof_addr"].Default)
	assert.Equal(t, "1h", got["server.leak_check_interval"].Default)

	// Allowed values.
	assert.ElementsMatch(t,
		[]string{
			config.ViewCondensed, config.ViewTerminal, config.ViewVSCode,
			config.ViewWeb, config.ViewCustom,
		},
		got["pr_view"].Valid,
	)
	assert.ElementsMatch(t,
		[]string{config.NotifyTerminal, config.NotifyNative},
		got["notifications.mode"].Valid,
	)
}

func TestSpecs_TriggerValuesMatchNotifyVocabulary(t *testing.T) {
	t.Parallel()

	byKey := map[string]config.KeySpec{}
	for _, spec := range config.Specs {
		byKey[spec.Key] = spec
	}

	notifyTriggers := make([]string, len(notify.Triggers))
	for i, tr := range notify.Triggers {
		notifyTriggers[i] = string(tr)
	}
	for _, key := range []string{"notifications.sounds", "notifications.images"} {
		spec, ok := byKey[key]
		require.True(t, ok, "missing spec for %s", key)
		assert.ElementsMatch(t, notifyTriggers, spec.Valid,
			"%s trigger values must match the notify vocabulary", key)
	}

	// Trigger names also mirror the trigger-named event types.
	for _, trigger := range notifyTriggers {
		assert.True(t, events.ValidTypes[events.Type(trigger)],
			"trigger %q should be a valid event type", trigger)
	}
}
