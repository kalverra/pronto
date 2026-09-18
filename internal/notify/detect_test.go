package notify_test

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

func makeBasePR(num int, title string) model.PullRequest {
	return model.PullRequest{
		Number:            num,
		Title:             title,
		RepoNameWithOwner: "kalverra/pronto",
		URL:               "https://github.com/kalverra/pronto/pull/1",
		Author:            "kalverra",
		HeadRefOID:        "commit1",
	}
}

func TestDetector_Seed_NoNotificationsOnInitialLoad(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	pr := makeBasePR(1, "Feature A")
	pr.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	d.Seed([]model.PullRequest{pr})

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{pr}, []model.PullRequest{pr})
	require.NoError(t, err)
	assert.Empty(t, notes, "seeded baseline must produce zero notifications")
}

func TestDetector_CIPassed_FromRunning(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prevPR := makeBasePR(1, "Feature A")
	prevPR.Checks = model.ChecksSummary{
		Total:             2,
		Running:           1,
		Done:              1,
		ReqTotal:          2,
		ReqRunning:        1,
		ReqDone:           1,
		HasRequiredChecks: true,
	}

	currPR := prevPR
	currPR.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)

	n := notes[0]
	assert.Equal(t, notify.TriggerCIPassed, n.Trigger)
	assert.Equal(t, 1, n.PRNumber)
	assert.Equal(t, "Feature A", n.PRTitle)
	assert.Equal(t, "kalverra/pronto", n.Repo)
	assert.Contains(t, n.Title, "CI Passed")
	assert.Contains(t, n.Message, "Feature A")
}

func TestDetector_CIPassed_FromFailed_Rerun(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prevPR := makeBasePR(1, "Feature A")
	prevPR.Checks = model.ChecksSummary{
		Total:             2,
		Failed:            1,
		Done:              2,
		ReqTotal:          2,
		ReqFailed:         1,
		ReqDone:           2,
		HasRequiredChecks: true,
	}

	currPR := prevPR
	currPR.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, notify.TriggerCIPassed, notes[0].Trigger)
}

func TestDetector_CIFailed_FromRunning(t *testing.T) {
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

	n := notes[0]
	assert.Equal(t, notify.TriggerCIFailed, n.Trigger)
	assert.Equal(t, 1, n.PRNumber)
	assert.Contains(t, n.Title, "CI Failed")
	assert.Contains(t, n.Message, "Feature A")
}

func TestDetector_CIFailed_FromPassed(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prevPR := makeBasePR(1, "Feature A")
	prevPR.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

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
	assert.Equal(t, notify.TriggerCIFailed, notes[0].Trigger)
}

func TestDetector_CIUnchanged_NoNotification(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)

	t.Run("passing remains passing", func(t *testing.T) {
		t.Parallel()
		pr := makeBasePR(1, "Feature A")
		pr.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

		notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{pr}, []model.PullRequest{pr})
		require.NoError(t, err)
		assert.Empty(t, notes)
	})

	t.Run("running remains running", func(t *testing.T) {
		t.Parallel()
		pr := makeBasePR(1, "Feature A")
		pr.Checks = model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}

		notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{pr}, []model.PullRequest{pr})
		require.NoError(t, err)
		assert.Empty(t, notes)
	})

	t.Run("failed remains failed", func(t *testing.T) {
		t.Parallel()
		pr := makeBasePR(1, "Feature A")
		pr.Checks = model.ChecksSummary{
			Total:             2,
			Failed:            1,
			Done:              2,
			ReqTotal:          2,
			ReqFailed:         1,
			ReqDone:           2,
			HasRequiredChecks: true,
		}

		notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{pr}, []model.PullRequest{pr})
		require.NoError(t, err)
		assert.Empty(t, notes)
	})
}

func TestDetector_ReviewReceived_Approved(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prevPR := makeBasePR(1, "Feature A")
	currPR := prevPR

	reviewTime := time.Now().Add(-5 * time.Minute)
	currPR.LatestReviews = []model.Review{
		{
			Author:      "alice",
			State:       "APPROVED",
			CommitOID:   "commit1",
			SubmittedAt: reviewTime,
		},
	}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)

	n := notes[0]
	assert.Equal(t, notify.TriggerReviewReceived, n.Trigger)
	assert.Equal(t, 1, n.PRNumber)
	assert.Equal(t, "alice", n.Author)
	assert.Equal(t, "APPROVED", n.ReviewState)
	assert.Contains(t, n.Title, "Review")
	assert.Contains(t, n.Message, "alice")
	assert.Contains(t, n.Message, "Approved")
}

func TestDetector_ReviewReceived_ChangesRequested(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prevPR := makeBasePR(1, "Feature A")
	currPR := prevPR

	reviewTime := time.Now().Add(-5 * time.Minute)
	currPR.LatestReviews = []model.Review{
		{
			Author:      "bob",
			State:       "CHANGES_REQUESTED",
			CommitOID:   "commit1",
			SubmittedAt: reviewTime,
		},
	}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)

	n := notes[0]
	assert.Equal(t, notify.TriggerReviewReceived, n.Trigger)
	assert.Equal(t, "bob", n.Author)
	assert.Equal(t, "CHANGES_REQUESTED", n.ReviewState)
	assert.Contains(t, n.Message, "bob")
	assert.Contains(t, n.Message, "Changes requested")
}

func TestDetector_ReviewReceived_Commented(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prevPR := makeBasePR(1, "Feature A")
	currPR := prevPR

	reviewTime := time.Now().Add(-5 * time.Minute)
	currPR.LatestReviews = []model.Review{
		{
			Author:      "charlie",
			State:       "COMMENTED",
			CommitOID:   "commit1",
			SubmittedAt: reviewTime,
		},
	}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)

	n := notes[0]
	assert.Equal(t, notify.TriggerReviewReceived, n.Trigger)
	assert.Equal(t, "charlie", n.Author)
	assert.Equal(t, "COMMENTED", n.ReviewState)
	assert.Contains(t, n.Message, "charlie")
	assert.Contains(t, n.Message, "Commented")
}

func TestDetector_Review_AuthorSelfReviewIgnored(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prevPR := makeBasePR(1, "Feature A") // author is "kalverra"
	currPR := prevPR

	currPR.LatestReviews = []model.Review{
		{
			Author:      "kalverra", // self
			State:       "COMMENTED",
			CommitOID:   "commit1",
			SubmittedAt: time.Now(),
		},
	}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	assert.Empty(t, notes, "author's own review/comment should not notify")
}

func TestDetector_Review_BotFiltered(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil, notify.WithBotFilter(true))
	prevPR := makeBasePR(1, "Feature A")
	currPR := prevPR

	currPR.LatestReviews = []model.Review{
		{
			Author:      "codecov[bot]",
			State:       "COMMENTED",
			CommitOID:   "commit1",
			SubmittedAt: time.Now(),
		},
		{
			Author:      "github-actions[bot]",
			State:       "COMMENTED",
			CommitOID:   "commit1",
			SubmittedAt: time.Now(),
		},
	}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	assert.Empty(t, notes, "bot reviews should be filtered out")
}

func TestDetector_PRMerged(t *testing.T) {
	t.Parallel()

	checkerCalled := false
	checker := func(_ context.Context, repo string, number int) (bool, error) {
		checkerCalled = true
		assert.Equal(t, "kalverra/pronto", repo)
		assert.Equal(t, 42, number)
		return true, nil
	}

	d := notify.NewDetector(checker)
	prevPR := makeBasePR(42, "Refactor core engine")
	// PR 42 is absent from currPRs (merged and closed)
	currPRs := []model.PullRequest{}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, currPRs)
	require.NoError(t, err)
	assert.True(t, checkerCalled, "checker should be called for vanished authored PR")
	require.Len(t, notes, 1)

	n := notes[0]
	assert.Equal(t, notify.TriggerPRMerged, n.Trigger)
	assert.Equal(t, 42, n.PRNumber)
	assert.Contains(t, n.Title, "PR Merged")
	assert.Contains(t, n.Message, "Refactor core engine")
}

func TestDetector_PRClosedWithoutMerge_NoNotification(t *testing.T) {
	t.Parallel()

	checkerCalled := false
	checker := func(_ context.Context, _ string, _ int) (bool, error) {
		checkerCalled = true
		return false, nil // closed, NOT merged
	}

	d := notify.NewDetector(checker)
	prevPR := makeBasePR(42, "Abandoned experiment")
	currPRs := []model.PullRequest{}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, currPRs)
	require.NoError(t, err)
	assert.True(t, checkerCalled)
	assert.Empty(t, notes, "unmerged closed PR must produce zero notifications")
}

func TestDetector_Deduplication(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prevPR := makeBasePR(1, "Feature A")
	prevPR.Checks = model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}

	currPR := prevPR
	currPR.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	// First detection fires notification
	notes1, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes1, 1)

	// Second detection with identical state should NOT re-notify
	notes2, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	assert.Empty(t, notes2, "duplicate event must be suppressed")
}

func TestDetector_SubsequentCommit_CIFailure_Notifies(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prCommit1 := makeBasePR(1, "Feature A")
	prCommit1.HeadRefOID = "commit1"
	prCommit1.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	// Commit 1 passed CI
	prev := makeBasePR(1, "Feature A")
	prev.HeadRefOID = "commit1"
	prev.Checks = model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}
	notes1, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prev}, []model.PullRequest{prCommit1})
	require.NoError(t, err)
	require.Len(t, notes1, 1)
	assert.Equal(t, notify.TriggerCIPassed, notes1[0].Trigger)

	// User pushes Commit 2, and CI on Commit 2 fails
	prCommit2Running := makeBasePR(1, "Feature A")
	prCommit2Running.HeadRefOID = "commit2"
	prCommit2Running.Checks = model.ChecksSummary{
		Total:             2,
		Running:           2,
		ReqTotal:          2,
		ReqRunning:        2,
		HasRequiredChecks: true,
	}

	prCommit2Failed := prCommit2Running
	prCommit2Failed.Checks = model.ChecksSummary{
		Total:             2,
		Failed:            1,
		Done:              2,
		ReqTotal:          2,
		ReqFailed:         1,
		ReqDone:           2,
		HasRequiredChecks: true,
	}

	notes2, err := d.DetectMineChanges(
		context.Background(),
		[]model.PullRequest{prCommit2Running},
		[]model.PullRequest{prCommit2Failed},
	)
	require.NoError(t, err)
	require.Len(t, notes2, 1, "failing CI on a subsequent commit must notify")
	assert.Equal(t, notify.TriggerCIFailed, notes2[0].Trigger)
	assert.Equal(t, "commit2", notes2[0].CommitOID)
}

func TestDetector_SubsequentReview_SameAuthor_Notifies(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	t1 := time.Now().Add(-10 * time.Minute)
	t2 := time.Now().Add(-1 * time.Minute)

	prInitial := makeBasePR(1, "Feature A")
	prReview1 := prInitial
	prReview1.LatestReviews = []model.Review{
		{Author: "alice", State: "COMMENTED", SubmittedAt: t1},
	}

	notes1, err := d.DetectMineChanges(
		context.Background(),
		[]model.PullRequest{prInitial},
		[]model.PullRequest{prReview1},
	)
	require.NoError(t, err)
	require.Len(t, notes1, 1)
	assert.Equal(t, notify.TriggerReviewReceived, notes1[0].Trigger)

	// Alice submits another review later
	prReview2 := prInitial
	prReview2.LatestReviews = []model.Review{
		{Author: "alice", State: "COMMENTED", SubmittedAt: t2},
	}

	notes2, err := d.DetectMineChanges(
		context.Background(),
		[]model.PullRequest{prReview1},
		[]model.PullRequest{prReview2},
	)
	require.NoError(t, err)
	require.Len(t, notes2, 1, "new review submission from same author must notify")
	assert.Equal(t, notify.TriggerReviewReceived, notes2[0].Trigger)
	assert.Equal(t, t2, notes2[0].SubmittedAt)
}

func TestDetector_NewPR_AlreadyPassed_Notifies(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	newPR := makeBasePR(99, "Newly Created Feature")
	newPR.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	// PR was not in prev (newly added), and not in seed
	notes, err := d.DetectMineChanges(context.Background(), nil, []model.PullRequest{newPR})
	require.NoError(t, err)
	require.Len(t, notes, 1, "unseeded newly arrived PR with passing checks must notify")
	assert.Equal(t, notify.TriggerCIPassed, notes[0].Trigger)
	assert.Equal(t, 99, notes[0].PRNumber)
}

func TestDetector_DisappearedPR_RetryOnCheckerError(t *testing.T) {
	t.Parallel()

	checkerCalls := 0
	checker := func(_ context.Context, _ string, _ int) (bool, error) {
		checkerCalls++
		if checkerCalls == 1 {
			return false, assert.AnError // transient failure
		}
		return true, nil // merged on retry
	}

	d := notify.NewDetector(checker)
	prevPR := makeBasePR(42, "Refactor core engine")

	// First tick: checker errors, no notification yet
	notes1, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, nil)
	require.NoError(t, err)
	assert.Empty(t, notes1)
	assert.Equal(t, 1, checkerCalls)

	// Second tick: prev no longer has PR 42, but detector should retry pending vanished check
	notes2, err := d.DetectMineChanges(context.Background(), nil, nil)
	require.NoError(t, err)
	require.Len(t, notes2, 1, "retry on recovered network must deliver merge notification")
	assert.Equal(t, notify.TriggerPRMerged, notes2[0].Trigger)
	assert.Equal(t, 42, notes2[0].PRNumber)
}

func TestDetector_TriggerImages(t *testing.T) {
	t.Parallel()

	images := map[notify.Trigger]string{
		notify.TriggerCIPassed: "/icons/pass.png",
	}
	d := notify.NewDetector(nil, notify.WithTriggerImages(images))

	prevPR := makeBasePR(1, "Feature A")
	prevPR.Checks = model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}

	currPR := prevPR
	currPR.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, "/icons/pass.png", notes[0].ImagePath)
}

func TestDetector_TriggerSounds(t *testing.T) {
	t.Parallel()

	sounds := map[notify.Trigger]string{
		notify.TriggerCIPassed: "/sounds/pass.wav",
	}
	d := notify.NewDetector(nil, notify.WithTriggerSounds(sounds))

	prevPR := makeBasePR(1, "Feature A")
	prevPR.Checks = model.ChecksSummary{Total: 2, Running: 2, ReqTotal: 2, ReqRunning: 2, HasRequiredChecks: true}

	currPR := prevPR
	currPR.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prevPR}, []model.PullRequest{currPR})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, "/sounds/pass.wav", notes[0].SoundPath)
}

func TestDetector_Titles_OmitPRontoPrefix(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(func(_ context.Context, _ string, _ int) (bool, error) {
		return true, nil
	})

	// CI Passed
	prev := makeBasePR(1, "Feature A")
	prev.Checks = model.ChecksSummary{Total: 1, Running: 1, ReqTotal: 1, ReqRunning: 1, HasRequiredChecks: true}
	curr := prev
	curr.Checks = model.ChecksSummary{Total: 1, Done: 1, ReqTotal: 1, ReqDone: 1, HasRequiredChecks: true}
	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prev}, []model.PullRequest{curr})
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, "CI Passed (#1)", notes[0].Title)
	assert.NotContains(t, notes[0].Title, "PRonto")

	// CI Failed
	d2 := notify.NewDetector(nil)
	currFailed := prev
	currFailed.Checks = model.ChecksSummary{Total: 1, Failed: 1, ReqTotal: 1, ReqFailed: 1, HasRequiredChecks: true}
	notesFailed, err := d2.DetectMineChanges(
		context.Background(),
		[]model.PullRequest{prev},
		[]model.PullRequest{currFailed},
	)
	require.NoError(t, err)
	require.Len(t, notesFailed, 1)
	assert.Equal(t, "CI Failed (#1)", notesFailed[0].Title)
	assert.NotContains(t, notesFailed[0].Title, "PRonto")
}

func TestDetector_Conflict_Notifies(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	prev := makeBasePR(1, "Feature A")
	prev.MergeStatus = model.ComputeMergeStatus("CLEAN", "CLEAN", false)

	curr := prev
	curr.Mergeable = "CONFLICTING"
	curr.MergeStateStatus = "DIRTY"
	curr.MergeStatus = model.ComputeMergeStatus("CONFLICTING", "DIRTY", false)

	notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{prev}, []model.PullRequest{curr})
	require.NoError(t, err)
	require.Len(t, notes, 1, "conflict transition must generate notification")
	assert.Equal(t, notify.TriggerConflict, notes[0].Trigger)
	assert.Equal(t, "Merge Conflict (#1)", notes[0].Title)
	assert.NotContains(t, notes[0].Title, "PRonto")

	// Seed suppresses existing conflict
	dSeeded := notify.NewDetector(nil)
	dSeeded.Seed([]model.PullRequest{curr})
	notesSeeded, err := dSeeded.DetectMineChanges(
		context.Background(),
		[]model.PullRequest{curr},
		[]model.PullRequest{curr},
	)
	require.NoError(t, err)
	assert.Empty(t, notesSeeded, "seed must suppress initial conflict")
}

func TestDetector_SeenKeys_PrunedForVanishedPRs(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	pr1 := makeBasePR(1, "Feature A")
	pr1.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}
	pr2 := makeBasePR(2, "Feature B")
	pr2.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	d.Seed([]model.PullRequest{pr1, pr2})
	require.Equal(t, 2, d.SeenKeysCount(), "initial seed should record 2 keys")

	// PR2 vanishes from curr.
	_, err := d.DetectMineChanges(context.Background(), []model.PullRequest{pr1, pr2}, []model.PullRequest{pr1})
	require.NoError(t, err)

	// Subsequent pass where PR2 is in neither prev nor curr.
	_, err = d.DetectMineChanges(context.Background(), []model.PullRequest{pr1}, []model.PullRequest{pr1})
	require.NoError(t, err)

	assert.Equal(t, 1, d.SeenKeysCount(), "keys for vanished PR2 must be dropped from seenKeys")
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDetector_VanishedPR_CheckerTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		checker := func(ctx context.Context, _ string, _ int) (bool, error) {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(500 * time.Millisecond):
				return true, nil
			}
		}

		d := notify.NewDetector(
			checker,
			notify.WithCheckerTimeout(50*time.Millisecond),
		)

		pr1 := makeBasePR(1, "Vanished PR")
		start := time.Now()
		notes, err := d.DetectMineChanges(context.Background(), []model.PullRequest{pr1}, nil)
		elapsed := time.Since(start)

		require.NoError(t, err)
		assert.Empty(t, notes, "timed-out checker should not produce notification")
		assert.Less(t, elapsed, 300*time.Millisecond, "vanished PR check must not stall beyond checker timeout")
	})
}

//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDetector_VanishedPR_CheckerBoundedParallelism(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			active         atomic.Int32
			maxConcurrency atomic.Int32
		)

		checker := func(_ context.Context, _ string, _ int) (bool, error) {
			cur := active.Add(1)
			for {
				old := maxConcurrency.Load()
				if cur <= old || maxConcurrency.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			active.Add(-1)
			return true, nil
		}

		const parallelism = 2
		d := notify.NewDetector(
			checker,
			notify.WithCheckerParallelism(parallelism),
			notify.WithCheckerTimeout(2*time.Second),
		)

		var prev []model.PullRequest
		for i := 1; i <= 6; i++ {
			prev = append(prev, makeBasePR(i, "PR"))
		}

		notes, err := d.DetectMineChanges(context.Background(), prev, nil)
		require.NoError(t, err)
		assert.Len(t, notes, 6)
		assert.Equal(
			t,
			int32(parallelism),
			maxConcurrency.Load(),
			"concurrency must reach and be bounded by configured parallelism",
		)
	})
}

func TestDetector_NotificationsHaveSubmittedAt(t *testing.T) {
	t.Parallel()

	checker := func(_ context.Context, _ string, _ int) (bool, error) {
		return true, nil
	}
	d := notify.NewDetector(checker)

	tBefore := time.Now().Add(-time.Second)

	// 1. CI passed
	prevPassing := makeBasePR(1, "CI Pass")
	prevPassing.Checks = model.ChecksSummary{Total: 2, Running: 1, ReqTotal: 2, ReqRunning: 1, HasRequiredChecks: true}
	currPassing := prevPassing
	currPassing.Checks = model.ChecksSummary{Total: 2, Done: 2, ReqTotal: 2, ReqDone: 2, HasRequiredChecks: true}

	// 2. CI failed
	prevFailing := makeBasePR(2, "CI Fail")
	prevFailing.Checks = model.ChecksSummary{Total: 2, Running: 1, ReqTotal: 2, ReqRunning: 1, HasRequiredChecks: true}
	currFailing := prevFailing
	currFailing.Checks = model.ChecksSummary{Total: 2, Failed: 1, ReqTotal: 2, ReqFailed: 1, HasRequiredChecks: true}

	// 3. Conflict
	prevConflict := makeBasePR(3, "Conflict")
	currConflict := prevConflict
	currConflict.MergeStatus = model.ComputeMergeStatus("CONFLICTING", "DIRTY", false)

	// 4. Vanished (merged)
	prevMerged := makeBasePR(4, "Merged")

	notes, err := d.DetectMineChanges(
		context.Background(),
		[]model.PullRequest{prevPassing, prevFailing, prevConflict, prevMerged},
		[]model.PullRequest{currPassing, currFailing, currConflict},
	)
	require.NoError(t, err)
	require.Len(t, notes, 4)

	for _, n := range notes {
		assert.False(t, n.SubmittedAt.IsZero(), "notification trigger %v must have non-zero SubmittedAt", n.Trigger)
		assert.True(t, n.SubmittedAt.After(tBefore), "SubmittedAt must be recent time.Now()")
	}
}
