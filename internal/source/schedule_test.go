package source

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	at := func(d time.Duration) *time.Time { t := ago(d); return &t }
	settled := model.ChecksSummary{State: "SUCCESS", Total: 1, Done: 1}
	// base is a settled, mergeable PR whose last activity and head commit
	// were age ago.
	base := func(age time.Duration) model.PullRequest {
		return model.PullRequest{
			RepoNameWithOwner: "org/repo", Number: 1,
			UpdatedAt: ago(age), Commits: []model.Commit{{OID: "a", CommittedDate: ago(age)}},
			Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN",
			Checks: settled,
		}
	}
	with := func(pr model.PullRequest, f func(*model.PullRequest)) model.PullRequest {
		f(&pr)
		return pr
	}

	tests := []struct {
		name    string
		pr      model.PullRequest
		boosted bool
		want    tier
	}{
		{"active", base(30 * time.Minute), false, tierActive},
		{"recent", base(3 * time.Hour), false, tierRecent},
		{"quiet", base(48 * time.Hour), false, tierQuiet},
		{"stale", base(40 * 24 * time.Hour), false, tierStale},
		{"merge queue is hot", with(base(48*time.Hour), func(p *model.PullRequest) {
			p.IsInMergeQueue = true
		}), false, tierHot},
		{"partial is hot", with(base(48*time.Hour), func(p *model.PullRequest) {
			p.Partial = true
		}), false, tierHot},
		{"running checks are hot", with(base(48*time.Hour), func(p *model.PullRequest) {
			p.Checks = model.ChecksSummary{Total: 2, Running: 1, Done: 1, StartedAt: at(5 * time.Minute)}
		}), false, tierHot},
		{"checks running for hours cool off", with(base(48*time.Hour), func(p *model.PullRequest) {
			p.Checks = model.ChecksSummary{Total: 2, Running: 1, Done: 1, StartedAt: at(3 * time.Hour)}
		}), false, tierQuiet},
		{"fresh push without checks is hot", with(base(5*time.Minute), func(p *model.PullRequest) {
			p.Checks = model.ChecksSummary{}
		}), false, tierHot},
		{"no checks ever is not hot", with(base(3*time.Hour), func(p *model.PullRequest) {
			p.Checks = model.ChecksSummary{}
		}), false, tierRecent},
		{"required check never dispatched is not hot", with(base(3*time.Hour), func(p *model.PullRequest) {
			p.Checks = model.ChecksSummary{HasRequiredChecks: true, ReqTotal: 2, ReqDone: 1}
		}), false, tierRecent},
		{"unknown merge state on an active PR is hot", with(base(30*time.Minute), func(p *model.PullRequest) {
			p.Mergeable = "UNKNOWN"
		}), false, tierHot},
		{"unknown merge state caps an old PR at active", with(base(48*time.Hour), func(p *model.PullRequest) {
			p.MergeStateStatus = "UNKNOWN"
		}), false, tierActive},
		{"a recent review is activity", with(base(48*time.Hour), func(p *model.PullRequest) {
			p.LatestReviews = []model.Review{{SubmittedAt: ago(10 * time.Minute)}}
		}), false, tierActive},
		{"boost moves recent to active", base(3 * time.Hour), true, tierActive},
		{"boost moves stale to quiet", base(40 * 24 * time.Hour), true, tierQuiet},
		{"boost never makes a PR hot", base(30 * time.Minute), true, tierActive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classify(tc.pr, now, model.StaleThreshold, tc.boosted)
			assert.Equal(t, tc.want.String(), got.String())
		})
	}
}

func TestFingerprintChanged(t *testing.T) {
	t.Parallel()

	updated := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	pr := model.PullRequest{
		UpdatedAt: updated, HeadRefOID: "a",
		Mergeable: "MERGEABLE", MergeStateStatus: "BLOCKED", ReviewDecision: "REVIEW_REQUIRED",
		Checks: model.ChecksSummary{State: "PENDING"},
	}
	ident := func(f func(*rawIdentity)) rawIdentity {
		id := rawIdentity{
			UpdatedAt: updated, HeadRefOID: "a",
			FPMergeable: "MERGEABLE", FPMergeStateStatus: "BLOCKED", FPReviewDecision: "REVIEW_REQUIRED",
		}
		id.FPLastCommit.Nodes = make([]struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		}, 1)
		id.FPLastCommit.Nodes[0].Commit.StatusCheckRollup = &struct {
			State string `json:"state"`
		}{State: "PENDING"}
		if f != nil {
			f(&id)
		}
		return id
	}

	tests := []struct {
		name string
		id   rawIdentity
		want bool
	}{
		{"unchanged", ident(nil), false},
		{"updatedAt", ident(func(id *rawIdentity) { id.UpdatedAt = updated.Add(time.Second) }), true},
		{"head pushed", ident(func(id *rawIdentity) { id.HeadRefOID = "b" }), true},
		{"entered merge queue", ident(func(id *rawIdentity) { id.IsInMergeQueue = true }), true},
		{"draft toggled", ident(func(id *rawIdentity) { id.IsDraft = true }), true},
		{"CI finished", ident(func(id *rawIdentity) {
			id.FPLastCommit.Nodes[0].Commit.StatusCheckRollup.State = "SUCCESS"
		}), true},
		{"base moved: conflict", ident(func(id *rawIdentity) { id.FPMergeable = "CONFLICTING" }), true},
		{"merge state moved", ident(func(id *rawIdentity) { id.FPMergeStateStatus = "CLEAN" }), true},
		{"review decision moved", ident(func(id *rawIdentity) { id.FPReviewDecision = "APPROVED" }), true},
		{"unknown merge state is not a change", ident(func(id *rawIdentity) {
			id.FPMergeable, id.FPMergeStateStatus = "UNKNOWN", "UNKNOWN"
		}), false},
		{"empty review decision is not a change", ident(func(id *rawIdentity) { id.FPReviewDecision = "" }), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, fingerprintChanged(tc.id, pr))
		})
	}
}

func TestDiscoveryBackoff(t *testing.T) {
	t.Parallel()

	base, idle := time.Minute, 3*time.Minute
	var got []time.Duration
	for quiet := range 7 {
		got = append(got, discoveryBackoff(base, idle, quiet))
	}
	assert.Equal(t, []time.Duration{
		time.Minute, time.Minute, time.Minute, time.Minute, 2 * time.Minute, 3 * time.Minute, 3 * time.Minute,
	}, got)
	assert.Equal(t, time.Minute, discoveryBackoff(base, 0, 10), "idle below base never shortens the base")
}
