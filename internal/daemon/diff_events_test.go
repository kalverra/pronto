package daemon_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
)

func TestDiffEvents_DeterministicSortedOrder(t *testing.T) {
	t.Parallel()

	prev := model.Queue{
		Authored: []model.PullRequest{
			{RepoNameWithOwner: "repo/b", Number: 20},
			{RepoNameWithOwner: "repo/a", Number: 50},
			{RepoNameWithOwner: "repo/a", Number: 10},
		},
	}
	curr := model.Queue{
		Authored: []model.PullRequest{
			{RepoNameWithOwner: "repo/b", Number: 15},
			{RepoNameWithOwner: "repo/a", Number: 30},
			{RepoNameWithOwner: "repo/b", Number: 5},
		},
	}

	evs := daemon.DiffEvents(prev, curr, nil)
	want := []events.Event{
		{Type: events.TypePRRemoved, Repo: "repo/a", PR: 10},
		{Type: events.TypePRAdded, Repo: "repo/a", PR: 30},
		{Type: events.TypePRRemoved, Repo: "repo/a", PR: 50},
		{Type: events.TypePRAdded, Repo: "repo/b", PR: 5},
		{Type: events.TypePRAdded, Repo: "repo/b", PR: 15},
		{Type: events.TypePRRemoved, Repo: "repo/b", PR: 20},
	}
	assert.Equal(t, want, evs)
}
