package notify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/notify"
)

func TestNativeNotifier_PayloadShape(t *testing.T) {
	t.Parallel()

	var gotPayload []byte
	runner := func(_ context.Context, _ string, payload []byte) error {
		gotPayload = payload
		return nil
	}

	notifier := notify.NewNativeNotifier(
		notify.WithHelperPath("/local/pronto/ProntoNotify.app/Contents/MacOS/pronto-notify"),
		notify.WithNativeRunner(runner),
	)

	err := notifier.Notify(context.Background(), notify.Notification{
		Trigger:   notify.TriggerCIPassed,
		PRNumber:  123,
		PRTitle:   "Fix \"critical\" bug",
		Repo:      "kalverra/pronto",
		URL:       "https://github.com/kalverra/pronto/pull/123",
		Title:     "PRonto: CI Passed (#123)",
		Message:   "Checks passed",
		ImagePath: "/tmp/icon.png",
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotPayload, &payload))

	assert.Equal(t, "PRonto: CI Passed (#123)", payload["title"])
	assert.Equal(t, "Checks passed", payload["body"])
	assert.Equal(t, "kalverra/pronto#123", payload["subtitle"])
	assert.Equal(t, "pronto-kalverra_pronto-123", payload["thread_id"])
	assert.Equal(t, "https://github.com/kalverra/pronto/pull/123", payload["url"])
	assert.Equal(t, "/tmp/icon.png", payload["image_path"])
	assert.NotContains(t, payload, "sound", "sound is opt-in and must be off by default")
}

func TestNativeNotifier_SoundEnabled_DefaultsToOSDefault(t *testing.T) {
	t.Parallel()

	var gotPayload []byte
	runner := func(_ context.Context, _ string, payload []byte) error {
		gotPayload = payload
		return nil
	}

	notifier := notify.NewNativeNotifier(
		notify.WithHelperPath("/helper"),
		notify.WithNativeRunner(runner),
		notify.WithNativeSoundEnabled(true),
	)

	err := notifier.Notify(context.Background(), notify.Notification{
		Trigger:  notify.TriggerCIFailed,
		PRNumber: 1,
		Repo:     "o/r",
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotPayload, &payload))
	assert.Equal(t, "default", payload["sound"])
}

func TestNativeNotifier_SoundOverride(t *testing.T) {
	t.Parallel()

	var gotPayload []byte
	runner := func(_ context.Context, _ string, payload []byte) error {
		gotPayload = payload
		return nil
	}

	notifier := notify.NewNativeNotifier(
		notify.WithHelperPath("/helper"),
		notify.WithNativeRunner(runner),
	)

	err := notifier.Notify(context.Background(), notify.Notification{
		Trigger:  notify.TriggerCIFailed,
		PRNumber: 1,
		Repo:     "o/r",
		Sound:    "Basso",
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotPayload, &payload))
	assert.Equal(t, "Basso", payload["sound"])
}

func TestNativeNotifier_DefaultImageResolved(t *testing.T) {
	t.Parallel()

	var gotPayload []byte
	runner := func(_ context.Context, _ string, payload []byte) error {
		gotPayload = payload
		return nil
	}

	notifier := notify.NewNativeNotifier(
		notify.WithHelperPath("/helper"),
		notify.WithNativeRunner(runner),
	)

	err := notifier.Notify(context.Background(), notify.Notification{
		Trigger:  notify.TriggerCIPassed,
		PRNumber: 1,
		Repo:     "o/r",
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotPayload, &payload))
	image, _ := payload["image_path"].(string)
	assert.True(t, image == "" || strings.HasSuffix(image, "ci-passed.png"),
		"image should be empty or the extracted trigger icon, got %q", image)
}

func TestNativeNotifier_SanitizesControlBytes(t *testing.T) {
	t.Parallel()

	var gotPayload []byte
	runner := func(_ context.Context, _ string, payload []byte) error {
		gotPayload = payload
		return nil
	}

	notifier := notify.NewNativeNotifier(
		notify.WithHelperPath("/helper"),
		notify.WithNativeRunner(runner),
	)

	err := notifier.Notify(context.Background(), notify.Notification{
		Trigger:  notify.TriggerReviewReceived,
		PRNumber: 7,
		Repo:     "o/r",
		Title:    "bad\x07title\n",
		Message:  "bad\rmessage\t",
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotPayload, &payload))
	assert.Equal(t, "badtitle ", payload["title"])
	assert.Equal(t, "bad message ", payload["body"])
}

func TestNativeNotifier_MissingHelper(t *testing.T) {
	t.Parallel()

	notifier := notify.NewNativeNotifier(
		notify.WithHelperPath(""),
		notify.WithNativeRunner(func(context.Context, string, []byte) error {
			t.Fatal("runner must not be called without a helper")
			return nil
		}),
		// Keep discovery away from real user directories.
		notify.WithNativeDiscoverOptions(notify.WithSearchPaths(t.TempDir()), notify.WithoutUserLocations()),
	)

	err := notifier.Notify(context.Background(), notify.Notification{PRNumber: 1, Repo: "o/r"})
	require.ErrorIs(t, err, notify.ErrHelperNotFound)
	assert.Contains(t, err.Error(), "pronto notify setup")
}

func TestNativeNotifier_RunnerFailureIncludesOutput(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		notifier := notify.NewNativeNotifier(
			notify.WithHelperPath("/helper"),
			notify.WithNativeRunner(func(context.Context, string, []byte) error {
				calls.Add(1)
				return &execExitError{stderr: "helper exploded"}
			}),
		)

		err := notifier.Notify(context.Background(), notify.Notification{PRNumber: 1, Repo: "o/r"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "helper exploded")
		assert.Equal(t, int32(3), calls.Load(), "a persistent failure is retried, then reported")
	})
}

// A fresh signature (rebuild/reinstall) makes usernoted briefly reject the
// helper, and it caches that rejection per process, so delivery must respawn
// the helper rather than give up on the first failure.
func TestNativeNotifier_RetriesTransientHelperFailure(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		notifier := notify.NewNativeNotifier(
			notify.WithHelperPath("/helper"),
			notify.WithNativeRunner(func(context.Context, string, []byte) error {
				if calls.Add(1) < 3 {
					return &execExitError{stderr: "Notifications are not allowed for this application"}
				}
				return nil
			}),
		)

		require.NoError(t, notifier.Notify(context.Background(), notify.Notification{PRNumber: 1, Repo: "o/r"}))
		assert.Equal(t, int32(3), calls.Load())
	})
}

func TestNativeNotifier_RetryStopsAtTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		notifier := notify.NewNativeNotifier(
			notify.WithHelperPath("/helper"),
			notify.WithNativeTimeout(time.Millisecond),
			notify.WithNativeRunner(func(context.Context, string, []byte) error {
				calls.Add(1)
				return &execExitError{stderr: "nope"}
			}),
		)

		require.Error(t, notifier.Notify(context.Background(), notify.Notification{PRNumber: 1, Repo: "o/r"}))
		assert.Equal(t, int32(1), calls.Load(), "no retry once the delivery timeout has passed")
	})
}

type execExitError struct {
	stderr string
}

func (e *execExitError) Error() string { return "exit status 1: " + e.stderr }

func TestNativeNotifier_TimeoutBoundsDelivery(t *testing.T) {
	t.Parallel()

	var gotDeadline time.Time
	runner := func(ctx context.Context, _ string, _ []byte) error {
		deadline, ok := ctx.Deadline()
		gotDeadline = deadline
		assert.True(t, ok, "runner context should carry a deadline")
		return nil
	}

	notifier := notify.NewNativeNotifier(
		notify.WithHelperPath("/helper"),
		notify.WithNativeRunner(runner),
		notify.WithNativeTimeout(10*time.Millisecond),
	)

	err := notifier.Notify(context.Background(), notify.Notification{PRNumber: 1, Repo: "o/r"})
	require.NoError(t, err)
	assert.False(t, gotDeadline.IsZero(), "runner context should carry the delivery timeout")
}

func TestDiscoverNativeHelper_EnvOverride(t *testing.T) {
	t.Setenv("PRONTO_NOTIFY_HELPER", "/custom/helper")

	assert.Equal(t, "/custom/helper", notify.DiscoverNativeHelper())
}

func TestDiscoverNativeHelper_SearchPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bundle := filepath.Join(dir, "ProntoNotify.app", "Contents", "MacOS")
	require.NoError(t, os.MkdirAll(bundle, 0o750))
	helper := filepath.Join(bundle, "pronto-notify")
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o600))
	// #nosec G302 -- discovery requires the executable bit; test-only fixture.
	require.NoError(t, os.Chmod(helper, 0o700))

	got := notify.DiscoverNativeHelper(
		notify.WithSearchPaths(dir),
		notify.WithoutUserLocations(),
	)
	assert.Equal(t, helper, got)
}

func TestDiscoverNativeHelper_NotFound(t *testing.T) {
	t.Parallel()

	got := notify.DiscoverNativeHelper(
		notify.WithSearchPaths(t.TempDir()),
		notify.WithoutUserLocations(),
	)
	assert.Empty(t, got)
}

// TestNativeNotifier_PayloadKeysMatchHelper guards the Go→Swift JSON contract:
// Swift's JSONDecoder silently drops keys it doesn't declare, so every key
// pronto emits must be a HelperPayload property name or a CodingKeys raw value.
func TestNativeNotifier_PayloadKeysMatchHelper(t *testing.T) {
	t.Parallel()

	var gotPayload []byte
	notifier := notify.NewNativeNotifier(
		notify.WithHelperPath("/local/pronto/ProntoNotify.app/Contents/MacOS/pronto-notify"),
		notify.WithNativeSoundEnabled(true),
		notify.WithNativeRunner(func(_ context.Context, _ string, payload []byte) error {
			gotPayload = payload
			return nil
		}),
	)
	require.NoError(t, notifier.Notify(context.Background(), notify.Notification{
		Trigger:   notify.TriggerCIPassed,
		PRNumber:  1,
		Repo:      "kalverra/pronto",
		URL:       "https://github.com/kalverra/pronto/pull/1",
		Title:     "title",
		Message:   "body",
		ImagePath: "/tmp/icon.png",
	}))

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotPayload, &payload))

	for key := range payload {
		declared := regexp.MustCompile(`(?:let|var)\s+` + key + `\s*:|case\s+\w+\s*=\s*"` + key + `"`)
		assert.Truef(t, declared.MatchString(notify.HelperMainSwift),
			"payload key %q is not decoded by the Swift helper", key)
	}
}

// fakeHelper writes an executable shell script standing in for the helper
// binary, so tests exercise the real spawn + exit-code classification.
func fakeHelper(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pronto-notify")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o600))
	// #nosec G302 -- the fake helper must be executable; test-only fixture.
	require.NoError(t, os.Chmod(path, 0o700))
	return path
}

// TestNativeNotifier_HelperExitCodes guards the helper's exit-code contract
// (see exitNotAuthorized/exitDenied in nativehelper/main.swift): the codes
// let pronto fall back to terminal delivery instead of failing silently.
func TestNativeNotifier_HelperExitCodes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		code int
		want error
	}{
		{3, notify.ErrNotAuthorized},
		{4, notify.ErrDenied},
	} {
		t.Run(tc.want.Error(), func(t *testing.T) {
			t.Parallel()

			helper := fakeHelper(t, fmt.Sprintf("cat >/dev/null; echo 'pronto-notify: nope' >&2; exit %d", tc.code))
			notifier := notify.NewNativeNotifier(notify.WithHelperPath(helper))

			err := notifier.Notify(context.Background(), notify.Notification{PRNumber: 1, Repo: "o/r"})
			require.ErrorIs(t, err, tc.want)
			assert.Contains(t, err.Error(), "pronto-notify: nope", "helper diagnostics must survive classification")
		})
	}

	assert.Regexp(t, `exitNotAuthorized:\s*Int32\s*=\s*3`, notify.HelperMainSwift)
	assert.Regexp(t, `exitDenied:\s*Int32\s*=\s*4`, notify.HelperMainSwift)
}

func TestNativeNotifier_DeniedIsNotRetried(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		notifier := notify.NewNativeNotifier(
			notify.WithHelperPath("/helper"),
			notify.WithNativeRunner(func(context.Context, string, []byte) error {
				calls.Add(1)
				return notify.ErrDenied
			}),
		)

		require.ErrorIs(
			t,
			notifier.Notify(context.Background(), notify.Notification{PRNumber: 1, Repo: "o/r"}),
			notify.ErrDenied,
		)
		assert.Equal(t, int32(1), calls.Load(), "denial is a user decision; respawning cannot change it")
	})
}

// TestNativeNotifier_NoActionButtons guards the click-only UX: clicking the
// banner opens the PR, and no action buttons are sent or registered.
func TestNativeNotifier_NoActionButtons(t *testing.T) {
	t.Parallel()

	var gotPayload []byte
	notifier := notify.NewNativeNotifier(
		notify.WithHelperPath("/helper"),
		notify.WithNativeRunner(func(_ context.Context, _ string, payload []byte) error {
			gotPayload = payload
			return nil
		}),
	)
	require.NoError(t, notifier.Notify(context.Background(), notify.Notification{
		PRNumber: 1, Repo: "o/r", URL: "https://github.com/o/r/pull/1",
	}))

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotPayload, &payload))
	assert.NotContains(t, payload, "actions")
	assert.Equal(t, "https://github.com/o/r/pull/1", payload["url"], "click-to-open needs the URL")
	assert.NotContains(t, notify.HelperMainSwift, "setNotificationCategories")
	assert.Contains(t, notify.HelperMainSwift, "UNNotificationDefaultActionIdentifier")
}
