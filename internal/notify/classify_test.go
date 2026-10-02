package notify_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

type dummyPrio struct {
	prio []model.PullRequest
}

func (d dummyPrio) Partition(prs []model.PullRequest) ([]model.PullRequest, []model.PullRequest) {
	return d.prio, prs
}

type dummyFocus struct {
	authors map[string]bool
}

func (d dummyFocus) Matches(pr model.PullRequest) bool {
	return d.authors[pr.Author]
}

func TestClassify(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	authoredPR := model.PullRequest{
		RepoNameWithOwner: "org/repo",
		Number:            1,
		Author:            "me",
		HeadRefOID:        "oid1",
	}
	inboxPR := model.PullRequest{
		RepoNameWithOwner: "org/repo",
		Number:            2,
		Author:            "alice",
		HeadRefOID:        "oid2",
	}
	prioPR := model.PullRequest{
		RepoNameWithOwner: "org/repo",
		Number:            3,
		Author:            "bob",
		HeadRefOID:        "oid3",
	}

	q := model.Queue{
		Authored: []model.PullRequest{authoredPR},
		Inbox:    []model.PullRequest{inboxPR, prioPR},
	}

	prio := dummyPrio{prio: []model.PullRequest{prioPR}}
	focus := dummyFocus{authors: map[string]bool{"alice": true}}

	obs := notify.Classify(q, prio, focus, nil, now)
	require.Len(t, obs, 3)

	scopesByNum := make(map[int][]notify.Scope)
	for _, o := range obs {
		scopesByNum[o.PR.Number] = o.Scopes
	}

	// PR 1: Authored -> Mine tab
	assert.Contains(t, scopesByNum[1], notify.Scope{Tab: notify.TabMine})

	// PR 2: Inbox, matching focus -> Inbox tab AND Focus tab
	assert.Contains(t, scopesByNum[2], notify.Scope{Tab: notify.TabInbox})
	assert.Contains(t, scopesByNum[2], notify.Scope{Tab: notify.TabFocus})

	// PR 3: Priority -> Priority tab
	assert.Contains(t, scopesByNum[3], notify.Scope{Tab: notify.TabPriority})
}
