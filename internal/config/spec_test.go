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
		"pr_view":                    "PRONTO_PR_VIEW",
		"pr_view_command":            "PRONTO_PR_VIEW_COMMAND",
		"notifications.mode":         "PRONTO_NOTIFICATIONS_MODE",
		"notifications.popups":       "PRONTO_NOTIFICATIONS_POPUPS",
		"notifications.sound":        "PRONTO_NOTIFICATIONS_SOUND",
		"notifications.groups":       "PRONTO_NOTIFICATIONS_GROUPS",
		"focus.authors":              "PRONTO_FOCUS_AUTHORS",
		"focus.repos":                "PRONTO_FOCUS_REPOS",
		"server.poll_interval":       "PRONTO_POLL_INTERVAL",
		"server.pprof_addr":          "PRONTO_PPROF_ADDR",
		"server.leak_check_interval": "PRONTO_LEAK_CHECK_INTERVAL",
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
	assert.Equal(t, []string{config.GroupFocus, config.GroupMine}, got["notifications.groups"].Default)
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
	assert.ElementsMatch(t,
		config.ValidNotificationGroups,
		got["notifications.groups"].Valid,
	)
}

func TestSpecs_TriggerValuesMatchNotifyVocabulary(t *testing.T) {
	t.Parallel()

	byKey := map[string]config.KeySpec{}
	for _, spec := range config.Specs {
		byKey[spec.Key] = spec
	}

	notifyTriggers := []string{
		string(notify.TriggerCIPassed),
		string(notify.TriggerCIFailed),
		string(notify.TriggerConflict),
		string(notify.TriggerReviewReceived),
		string(notify.TriggerPRMerged),
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
