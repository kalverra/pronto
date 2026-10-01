package notify_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

func obsIn(pr model.PullRequest, scopes ...notify.Scope) []notify.Observed {
	return []notify.Observed{{PR: pr, Scopes: scopes}}
}

func detect(t *testing.T, d *notify.Detector, prev, curr []notify.Observed) []notify.Notification {
	t.Helper()
	notes, err := d.DetectChanges(context.Background(), prev, curr)
	require.NoError(t, err)
	return notes
}

func triggersOf(notes []notify.Notification) []notify.Trigger {
	out := make([]notify.Trigger, len(notes))
	for i, n := range notes {
		out[i] = n.Trigger
	}
	return out
}

func TestDetector_MergeQueueTransitions(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	mine := scope(notify.TabMine, "")
	base := makeBasePR(1, "Queued")
	queued := base
	queued.IsInMergeQueue = true

	notes := detect(t, d, obsIn(base, mine), obsIn(queued, mine))
	require.Len(t, notes, 1)
	assert.Equal(t, notify.TriggerMergeQueueEntered, notes[0].Trigger)
	assert.Contains(t, notes[0].Title, "Merge Queue")
	assert.Equal(t, []notify.Scope{mine}, notes[0].Scopes)

	assert.Empty(t, detect(t, d, obsIn(queued, mine), obsIn(queued, mine)), "no repeat on identical poll")

	notes = detect(t, d, obsIn(queued, mine), obsIn(base, mine))
	require.Len(t, notes, 1)
	assert.Equal(t, notify.TriggerMergeQueueLeft, notes[0].Trigger)
	assert.Contains(t, notes[0].Title, "Kicked out of Merge Queue")
	assert.Contains(t, notes[0].Message, "Kicked out of merge queue")
}

func TestDetector_MergeQueueLeft_NotFiredWhenVanishes(t *testing.T) {
	t.Parallel()

	checker := func(context.Context, string, int) (notify.PRState, error) { return notify.PRStateOpen, nil }
	d := notify.NewDetector(perPR(checker))
	queued := makeBasePR(1, "Queued")
	queued.IsInMergeQueue = true

	assert.Empty(t, detect(t, d, obsIn(queued), nil))
}

func TestDetector_Vanished_ByState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state notify.PRState
		want  []notify.Trigger
	}{
		{notify.PRStateMerged, []notify.Trigger{notify.TriggerPRMerged}},
		{notify.PRStateClosed, []notify.Trigger{notify.TriggerPRClosed}},
		{notify.PRStateOpen, []notify.Trigger{}},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			t.Parallel()
			checker := func(context.Context, string, int) (notify.PRState, error) { return tc.state, nil }
			d := notify.NewDetector(perPR(checker))
			mine := scope(notify.TabMine, model.SectionInReview)

			notes := detect(t, d, obsIn(makeBasePR(1, "Gone"), mine), nil)
			assert.Equal(t, tc.want, triggersOf(notes))
			for _, n := range notes {
				assert.Equal(t, []notify.Scope{mine}, n.Scopes, "vanished PRs use prev scopes")
			}
			assert.Empty(t, detect(t, d, nil, nil), "not repeated")
		})
	}
}

func TestDetector_NewCommits(t *testing.T) {
	t.Parallel()

	prev := makeBasePR(1, "Feature")
	prev.Author = "someone"
	curr := prev
	curr.HeadRefOID = "commit2"

	t.Run("fires once on new head", func(t *testing.T) {
		t.Parallel()
		d := notify.NewDetector(nil, notify.WithViewer("me"))
		notes := detect(t, d, obs(prev), obs(curr))
		require.Len(t, notes, 1)
		assert.Equal(t, notify.TriggerNewCommits, notes[0].Trigger)
		assert.Equal(t, "https://github.com/kalverra/pronto/pull/1/commits", notes[0].URL)
		assert.Empty(t, detect(t, d, obs(curr), obs(curr)))
	})

	t.Run("suppressed for viewer's own pushes", func(t *testing.T) {
		t.Parallel()
		d := notify.NewDetector(nil, notify.WithViewer("someone"))
		assert.Empty(t, detect(t, d, obs(prev), obs(curr)))
	})

	t.Run("not fired when prev head unknown", func(t *testing.T) {
		t.Parallel()
		d := notify.NewDetector(nil)
		blank := prev
		blank.HeadRefOID = ""
		assert.Empty(t, detect(t, d, obs(blank), obs(curr)))
	})
}

func TestDetector_Entered(t *testing.T) {
	t.Parallel()

	prio := scope(notify.TabPriority, "")
	attn := scope(notify.TabPriority, model.SectionAttention)
	blocked := scope(notify.TabPriority, model.SectionBlocked)
	pr := makeBasePR(1, "Review me")

	t.Run("fires on scope gain and carries the scope", func(t *testing.T) {
		t.Parallel()
		d := notify.NewDetector(nil)
		notes := detect(t, d, obsIn(pr, prio, blocked), obsIn(pr, prio, attn))
		require.Len(t, notes, 1)
		assert.Equal(t, notify.TriggerEntered, notes[0].Trigger)
		require.NotNil(t, notes[0].Entered)
		assert.Equal(t, attn, *notes[0].Entered)
		assert.Equal(t, []notify.Scope{attn}, notes[0].Scopes)
		assert.Equal(t, "👀 Ready for Review (#1)", notes[0].Title)
	})

	t.Run("new PR enters all its scopes", func(t *testing.T) {
		t.Parallel()
		d := notify.NewDetector(nil)
		notes := detect(t, d, nil, obsIn(pr, prio, attn))
		require.Len(t, notes, 2)
		assert.Equal(t, "🔥 Priority (#1)", notes[0].Title)
	})

	t.Run("not on seeded baseline", func(t *testing.T) {
		t.Parallel()
		d := notify.NewDetector(nil)
		base := obsIn(pr, prio, attn)
		d.Seed(base)
		assert.Empty(t, detect(t, d, nil, base))
	})

	t.Run("no refire when flapping on same commit; refires after push", func(t *testing.T) {
		t.Parallel()
		d := notify.NewDetector(nil)
		assert.Len(t, detect(t, d, obsIn(pr, prio, blocked), obsIn(pr, prio, attn)), 1)
		// First visit to blocked on this commit fires; going back to attention does not repeat.
		assert.Len(t, detect(t, d, obsIn(pr, prio, attn), obsIn(pr, prio, blocked)), 1)
		assert.Empty(t, detect(t, d, obsIn(pr, prio, blocked), obsIn(pr, prio, attn)))
		assert.Empty(t, detect(t, d, obsIn(pr, prio, attn), obsIn(pr, prio, attn)))

		pushed := pr
		pushed.HeadRefOID = "commit2"
		assert.Equal(t,
			[]notify.Trigger{notify.TriggerEntered, notify.TriggerNewCommits},
			triggersOf(detect(t, d, obsIn(pr, prio, attn), obsIn(pushed, prio, blocked))))
		assert.Len(t, detect(t, d, obsIn(pushed, prio, blocked), obsIn(pushed, prio, attn)), 1)
	})

	t.Run("text for other scopes", func(t *testing.T) {
		t.Parallel()
		d := notify.NewDetector(nil)
		rtm := scope(notify.TabMine, model.SectionReadyToMerge)
		notes := detect(t, d, obsIn(pr), obsIn(pr, rtm))
		require.Len(t, notes, 1)
		assert.Equal(t, "🚀 Ready to Merge (#1)", notes[0].Title)

		other := scope(notify.TabFocus, model.SectionActionRequired)
		notes = detect(t, d, obsIn(pr), obsIn(pr, other))
		require.Len(t, notes, 1)
		assert.Equal(t, "⚡ Action Required (#1)", notes[0].Title)
		assert.Contains(t, notes[0].Message, "Now in Focus › Action Required")
	})
}

func TestDetector_PROpened_RespectsClock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	d := notify.NewDetector(nil, notify.WithClock(func() time.Time { return now }))
	d.Seed(nil)

	fresh := makeBasePR(1, "Fresh")
	fresh.CreatedAt = now.Add(time.Minute)
	old := makeBasePR(2, "Old")
	old.CreatedAt = now.Add(-time.Hour)
	mine := scope(notify.TabMine, "")

	notes := detect(t, d, nil, obsIn(fresh, mine))
	got := triggersOf(notes)
	assert.Contains(t, got, notify.TriggerPROpened)
	assert.Contains(t, got, notify.TriggerEntered)

	// Old PR newly requested: entered only, never opened.
	notes = detect(t, d, nil, obsIn(old, scope(notify.TabPriority, "")))
	assert.Equal(t, []notify.Trigger{notify.TriggerEntered}, triggersOf(notes))
}

func TestDetector_ScopesUnionPrevAndCurr(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	a := scope(notify.TabMine, model.SectionInReview)
	b := scope(notify.TabMine, model.SectionActionRequired)
	prev := makeBasePR(1, "Feature")
	prev.Checks = model.ChecksSummary{Total: 1, Running: 1, ReqTotal: 1, ReqRunning: 1, HasRequiredChecks: true}
	curr := prev
	curr.Checks = model.ChecksSummary{Total: 1, Failed: 1, ReqTotal: 1, ReqFailed: 1, HasRequiredChecks: true}

	notes := detect(t, d, obsIn(prev, a), obsIn(curr, b))
	var ci notify.Notification
	for _, n := range notes {
		if n.Trigger == notify.TriggerCIFailed {
			ci = n
		}
	}
	assert.ElementsMatch(t, []notify.Scope{a, b}, ci.Scopes)
}

func TestDetector_MergeQueueKickedOut_FailingChecks(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	mine := scope(notify.TabMine, "")
	queued := makeBasePR(1, "Queued PR")
	queued.IsInMergeQueue = true

	kicked := makeBasePR(1, "Queued PR")
	kicked.Checks = model.ChecksSummary{
		Total:     2,
		Failed:    1,
		FailedURL: "https://github.com/kalverra/pronto/actions/runs/123",
	}

	notes := detect(t, d, obsIn(queued, mine), obsIn(kicked, mine))
	var mqNote *notify.Notification
	for _, n := range notes {
		if n.Trigger == notify.TriggerMergeQueueLeft {
			mqNote = &n
		}
	}
	require.NotNil(t, mqNote)
	assert.Equal(t, "🚨 Kicked out of Merge Queue (#1)", mqNote.Title)
	assert.Contains(t, mqNote.Message, "Kicked out of merge queue (checks failed)")
	assert.Equal(t, "https://github.com/kalverra/pronto/actions/runs/123", mqNote.URL)
}

func TestDetector_MergeQueueKickedOut_Conflict(t *testing.T) {
	t.Parallel()

	d := notify.NewDetector(nil)
	mine := scope(notify.TabMine, "")
	queued := makeBasePR(1, "Queued PR")
	queued.IsInMergeQueue = true

	kicked := makeBasePR(1, "Queued PR")
	kicked.MergeStatus = model.MergeStatus{Mergeable: "CONFLICTING"}

	notes := detect(t, d, obsIn(queued, mine), obsIn(kicked, mine))
	var mqNote *notify.Notification
	for _, n := range notes {
		if n.Trigger == notify.TriggerMergeQueueLeft {
			mqNote = &n
		}
	}
	require.NotNil(t, mqNote)
	assert.Equal(t, "🚨 Kicked out of Merge Queue (#1)", mqNote.Title)
	assert.Contains(t, mqNote.Message, "Kicked out of merge queue (conflicts)")
}

func TestDetector_Vanished_DefaultBranchFiltering(t *testing.T) {
	t.Parallel()

	checker := func(context.Context, string, int) (notify.PRState, error) {
		return notify.PRStateMerged, nil
	}
	d := notify.NewDetector(perPR(checker))
	mine := scope(notify.TabMine, model.SectionReadyToMerge)

	// Merged into non-default feature branch: must be skipped.
	prFeature := makeBasePR(10, "Stack Child")
	prFeature.BaseRefName = "feature-parent"
	prFeature.DefaultBranch = "develop"
	notes := detect(t, d, obsIn(prFeature, mine), nil)
	assert.Empty(t, notes, "merging into non-default branch must not trigger pr_merged")

	// Merged into default branch: must notify with branch name.
	prDefault := makeBasePR(11, "Trunk Feature")
	prDefault.BaseRefName = "develop"
	prDefault.DefaultBranch = "develop"
	notes = detect(t, d, obsIn(prDefault, mine), nil)
	require.Len(t, notes, 1)
	assert.Equal(t, notify.TriggerPRMerged, notes[0].Trigger)
	assert.Equal(t, "🟣 PR Merged (#11)", notes[0].Title)
	assert.Contains(t, notes[0].Message, "Merged to develop")
}
