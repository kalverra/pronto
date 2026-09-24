package notify_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

func TestDefaultTriggerImage_MappedTriggers(t *testing.T) {
	t.Parallel()

	assert.Equal(
		t,
		"conflict.png",
		filepath.Base(notify.DefaultTriggerImage(notify.Notification{Trigger: notify.TriggerConflict})),
	)
	assert.Equal(
		t,
		"merged.png",
		filepath.Base(notify.DefaultTriggerImage(notify.Notification{Trigger: notify.TriggerPRMerged})),
	)
	assert.Equal(
		t,
		"ci-failed.png",
		filepath.Base(notify.DefaultTriggerImage(notify.Notification{Trigger: notify.TriggerCIFailed})),
	)
	assert.Equal(
		t,
		"ci-passed.png",
		filepath.Base(notify.DefaultTriggerImage(notify.Notification{Trigger: notify.TriggerCIPassed})),
	)
}

func TestDefaultTriggerImage_ExtractedFileExists(t *testing.T) {
	t.Parallel()

	path := notify.DefaultTriggerImage(notify.Notification{Trigger: notify.TriggerCIPassed})
	require.NotEmpty(t, path)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, info.Size(), "must not be an empty file")

	// #nosec G304 -- path comes from DefaultTriggerImage, a controlled cache dir.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(data), 4)
	assert.Equal(t, []byte{0x89, 'P', 'N', 'G'}, data[:4], "must be a PNG")
}

func TestDefaultTriggerImage_StablePath(t *testing.T) {
	t.Parallel()

	first := notify.DefaultTriggerImage(notify.Notification{Trigger: notify.TriggerCIPassed})
	second := notify.DefaultTriggerImage(notify.Notification{Trigger: notify.TriggerCIPassed})

	assert.Equal(t, first, second)
}

func TestDetector_ReviewStateDefaultIcons(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"APPROVED":          "approved.png",
		"CHANGES_REQUESTED": "rejected.png",
		"COMMENTED":         "comment.png",
	}
	for state, wantBase := range cases {
		t.Run(state, func(t *testing.T) {
			t.Parallel()

			d := notify.NewDetector(nil)
			prevPR := makeBasePR(1, "Feature A")
			currPR := prevPR
			currPR.LatestReviews = []model.Review{
				{
					Author:      "reviewer",
					State:       state,
					CommitOID:   "commit1",
					SubmittedAt: time.Now(),
				},
			}

			notes, err := d.DetectMineChanges(
				context.Background(),
				[]model.PullRequest{prevPR},
				[]model.PullRequest{currPR},
			)
			require.NoError(t, err)
			require.Len(t, notes, 1)
			assert.Equal(t, wantBase, filepath.Base(notes[0].ImagePath))
		})
	}
}

func TestDetector_DefaultIcon_Fallback(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prevPR := makeBasePR(1, "Feature A")
	prevPR.Checks = model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}

	currPR := prevPR
	currPR.Checks = model.ChecksSummary{
		Total:             2,
		Failed:            1,
		Done:              2,
		ReqTotal:          2,
		ReqFailed:         1,
		ReqDone:           2,
		HasRequiredChecks: true,
	}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(
		t,
		notify.DefaultTriggerImage(notify.Notification{Trigger: notify.TriggerCIFailed}),
		notes[0].ImagePath,
	)
}

func TestDetector_UserImageOverridesDefaultIcon(t *testing.T) {
	t.Parallel()

	images := map[notify.Trigger]string{notify.TriggerCIFailed: "/custom/fail.png"}
	d := notify.NewDetector(nil, notify.WithAssets(notify.Assets{Images: images}))

	prevPR := makeBasePR(1, "Feature A")
	prevPR.Checks = model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}

	currPR := prevPR
	currPR.Checks = model.ChecksSummary{
		Total:             2,
		Failed:            1,
		Done:              2,
		ReqTotal:          2,
		ReqFailed:         1,
		ReqDone:           2,
		HasRequiredChecks: true,
	}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, "/custom/fail.png", notes[0].ImagePath)
}

func TestDetector_UserImagePartial_KeepsDefaultsForOthers(t *testing.T) {
	t.Parallel()

	images := map[notify.Trigger]string{notify.TriggerCIPassed: "/custom/pass.png"}
	d := notify.NewDetector(nil, notify.WithAssets(notify.Assets{Images: images}))

	prevPR := makeBasePR(1, "Feature A")
	prevPR.Checks = model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}

	currPR := prevPR
	currPR.Checks = model.ChecksSummary{
		Total:             2,
		Failed:            1,
		Done:              2,
		ReqTotal:          2,
		ReqFailed:         1,
		ReqDone:           2,
		HasRequiredChecks: true,
	}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(
		t,
		notify.DefaultTriggerImage(notify.Notification{Trigger: notify.TriggerCIFailed}),
		notes[0].ImagePath,
	)
}

func TestDefaultTriggerImage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		note     notify.Notification
		wantBase string
	}{
		{
			name:     "ci passed",
			note:     notify.Notification{Trigger: notify.TriggerCIPassed},
			wantBase: "ci-passed.png",
		},
		{
			name:     "ci failed",
			note:     notify.Notification{Trigger: notify.TriggerCIFailed},
			wantBase: "ci-failed.png",
		},
		{
			name:     "conflict",
			note:     notify.Notification{Trigger: notify.TriggerConflict},
			wantBase: "conflict.png",
		},
		{
			name:     "merged",
			note:     notify.Notification{Trigger: notify.TriggerPRMerged},
			wantBase: "merged.png",
		},
		{
			name:     "review approved",
			note:     notify.Notification{Trigger: notify.TriggerReviewReceived, ReviewState: "APPROVED"},
			wantBase: "approved.png",
		},
		{
			name:     "review changes requested",
			note:     notify.Notification{Trigger: notify.TriggerReviewReceived, ReviewState: "CHANGES_REQUESTED"},
			wantBase: "rejected.png",
		},
		{
			name:     "review commented",
			note:     notify.Notification{Trigger: notify.TriggerReviewReceived, ReviewState: "COMMENTED"},
			wantBase: "comment.png",
		},
		{
			name:     "review fallback",
			note:     notify.Notification{Trigger: notify.TriggerReviewReceived},
			wantBase: "comment.png",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := notify.DefaultTriggerImage(tc.note)
			require.NotEmpty(t, got)
			assert.Equal(t, tc.wantBase, filepath.Base(got))
			info, err := os.Stat(got)
			require.NoError(t, err)
			assert.Positive(t, info.Size())
		})
	}
}
