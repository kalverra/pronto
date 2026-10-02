package notify_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

type mockPriorityPartitioner struct {
	priority []model.PullRequest
}

func (m mockPriorityPartitioner) Partition(prs []model.PullRequest) ([]model.PullRequest, []model.PullRequest) {
	return m.priority, prs
}

type mockFocusMatcher struct {
	focused map[model.PRKey]bool
}

func (m mockFocusMatcher) Matches(pr model.PullRequest) bool {
	return m.focused[pr.Key()]
}

func TestDetector_DeepInterface_SeedAndDetect(t *testing.T) {
	t.Parallel()

	pr1 := model.PullRequest{
		RepoNameWithOwner: "org/repo",
		Number:            101,
		Title:             "PR 101",
		HeadRefOID:        "oid1",
		Checks: model.ChecksSummary{
			Total:             2,
			Running:           2,
			ReqTotal:          2,
			ReqRunning:        2,
			HasRequiredChecks: true,
		},
	}
	initialQueue := model.Queue{
		Authored: []model.PullRequest{pr1},
	}

	policy := notify.Policy{
		notify.Scope{Tab: notify.TabMine}: notify.TriggerSet{
			notify.TriggerCIPassed: true,
		},
	}

	d := notify.NewDetector(
		notify.WithPolicy(policy),
	)

	// Baseline seed
	d.Seed(initialQueue)

	// Second poll: CI finishes and passes
	pr1Passed := pr1
	pr1Passed.Checks = model.ChecksSummary{
		Total:             2,
		Done:              2,
		ReqTotal:          2,
		ReqDone:           2,
		HasRequiredChecks: true,
	}

	// Add a new PR 102
	pr2 := model.PullRequest{
		RepoNameWithOwner: "org/repo",
		Number:            102,
		Title:             "PR 102",
	}

	currQueue := model.Queue{
		Authored: []model.PullRequest{pr1Passed, pr2},
	}

	delta := d.Detect(context.Background(), initialQueue, currQueue)

	// Check policy-selected notifications
	require.Len(t, delta.Notifications, 1)
	assert.Equal(t, notify.TriggerCIPassed, delta.Notifications[0].Trigger)
	assert.Equal(t, 101, delta.Notifications[0].PRNumber)

	// Check events list (includes both trigger event and pr_added)
	var eventTypes []events.Type
	var notifiedEvents []events.Type
	for _, ev := range delta.Events {
		eventTypes = append(eventTypes, ev.Type)
		if ev.Notify != nil {
			notifiedEvents = append(notifiedEvents, ev.Type)
		}
	}

	assert.Contains(t, eventTypes, events.TypeCIPassed)
	assert.Contains(t, eventTypes, events.TypePRAdded)
	assert.Contains(t, notifiedEvents, events.TypeCIPassed)
}

func TestDetector_Seed_ReturnsBoosted(t *testing.T) {
	t.Parallel()

	prPrio := model.PullRequest{
		RepoNameWithOwner: "org/repo",
		Number:            201,
	}
	prFocus := model.PullRequest{
		RepoNameWithOwner: "org/repo",
		Number:            202,
	}
	prOther := model.PullRequest{
		RepoNameWithOwner: "org/repo",
		Number:            203,
	}

	q := model.Queue{
		Inbox: []model.PullRequest{prPrio, prFocus, prOther},
	}

	d := notify.NewDetector(
		notify.WithPriority(mockPriorityPartitioner{priority: []model.PullRequest{prPrio}}),
		notify.WithFocus(mockFocusMatcher{focused: map[model.PRKey]bool{prFocus.Key(): true}}),
	)

	boosted := d.Seed(q)
	assert.True(t, boosted[prPrio.Key()], "priority PR must be boosted")
	assert.True(t, boosted[prFocus.Key()], "focused PR must be boosted")
	assert.False(t, boosted[prOther.Key()], "other PR must not be boosted")
}

func TestDetector_Detect_NilPolicyNotifiesNothing(t *testing.T) {
	t.Parallel()

	running := model.PullRequest{
		RepoNameWithOwner: "org/repo",
		Number:            1,
		HeadRefOID:        "oid1",
		Checks: model.ChecksSummary{
			Total:             1,
			Running:           1,
			ReqTotal:          1,
			ReqRunning:        1,
			HasRequiredChecks: true,
		},
	}
	passed := running
	passed.Checks = model.ChecksSummary{Total: 1, Done: 1, ReqTotal: 1, ReqDone: 1, HasRequiredChecks: true}
	fresh := model.PullRequest{RepoNameWithOwner: "org/repo", Number: 2}

	d := notify.NewDetector()
	prev := model.Queue{Authored: []model.PullRequest{running}}
	d.Seed(prev)
	delta := d.Detect(context.Background(), prev, model.Queue{Authored: []model.PullRequest{passed, fresh}})

	assert.Empty(t, delta.Notifications, "no policy subscribes no scope")
	var types []events.Type
	for _, ev := range delta.Events {
		assert.Nil(t, ev.Notify, "%s must not carry a banner without a policy", ev.Type)
		types = append(types, ev.Type)
	}
	assert.Contains(t, types, events.TypeCIPassed, "unselected triggers still reach the bus")
	assert.Contains(t, types, events.TypePRAdded)
}

func TestDetector_Detect_ReportsBoostedForCurrentQueue(t *testing.T) {
	t.Parallel()

	prPrio := model.PullRequest{RepoNameWithOwner: "org/repo", Number: 1}
	prOther := model.PullRequest{RepoNameWithOwner: "org/repo", Number: 2}

	d := notify.NewDetector(
		notify.WithPriority(mockPriorityPartitioner{priority: []model.PullRequest{prPrio}}),
	)
	prev := model.Queue{Inbox: []model.PullRequest{prOther}}
	d.Seed(prev)
	delta := d.Detect(context.Background(), prev, model.Queue{Inbox: []model.PullRequest{prPrio, prOther}})

	assert.Equal(t, map[model.PRKey]bool{prPrio.Key(): true}, delta.Boosted)
}
