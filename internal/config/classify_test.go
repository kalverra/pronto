package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Hour)
	mk := func(n int, author string, mut func(*model.PullRequest)) model.PullRequest {
		pr := model.PullRequest{
			Number: n, RepoNameWithOwner: "o/r", Author: author, UpdatedAt: fresh,
			ReviewDecision: "REVIEW_REQUIRED",
		}
		if mut != nil {
			mut(&pr)
		}
		return pr
	}
	scopesOf := func(obs []notify.Observed, n int) []notify.Scope {
		for _, o := range obs {
			if o.PR.Number == n {
				return o.Scopes
			}
		}
		t.Fatalf("PR %d not classified", n)
		return nil
	}
	tab := func(t notify.Tab) notify.Scope { return notify.Scope{Tab: t} }
	sec := func(t notify.Tab, s model.Section) notify.Scope { return notify.Scope{Tab: t, Section: s} }

	prio := config.DefaultPriorityConfig()
	stack := &model.PRStack{ID: "S", Size: 2}

	t.Run("authored lands in mine with section", func(t *testing.T) {
		t.Parallel()
		q := model.Queue{Authored: []model.PullRequest{mk(1, "me", nil)}, Viewer: "me"}
		got := config.Classify(q, prio, config.RuleSet{}, nil, now)
		require.Len(t, got, 1)
		assert.Equal(t, []notify.Scope{tab(notify.TabMine), sec(notify.TabMine, model.SectionInReview)}, got[0].Scopes)
	})

	t.Run("direct request is priority, plain request is inbox", func(t *testing.T) {
		t.Parallel()
		q := model.Queue{
			Inbox: []model.PullRequest{
				mk(2, "a", func(p *model.PullRequest) { p.DirectRequest = true }),
				mk(3, "b", nil),
			},
			Viewer: "me",
		}
		got := config.Classify(q, prio, config.RuleSet{}, nil, now)
		assert.Equal(t,
			[]notify.Scope{tab(notify.TabPriority), sec(notify.TabPriority, model.SectionAttention)}, scopesOf(got, 2))
		assert.Equal(t,
			[]notify.Scope{tab(notify.TabInbox), sec(notify.TabInbox, model.SectionAttention)}, scopesOf(got, 3))
	})

	t.Run("focus via manual set, rule, and manual unfocus", func(t *testing.T) {
		t.Parallel()
		q := model.Queue{
			Authored: []model.PullRequest{mk(1, "me", nil)},
			Inbox:    []model.PullRequest{mk(2, "a", nil), mk(3, "ruled", nil), mk(4, "ruled", nil)},
			Viewer:   "me",
		}
		override := func(k model.PRKey) (bool, bool) {
			switch k.Number {
			case 2:
				return true, true
			case 4:
				return false, true
			}
			return false, false
		}
		got := config.Classify(q, prio, config.RuleSet{Authors: []string{"ruled"}}, override, now)
		assert.Contains(t, scopesOf(got, 2), tab(notify.TabFocus))
		assert.Contains(t, scopesOf(got, 3), tab(notify.TabFocus))
		assert.Contains(t, scopesOf(got, 3), sec(notify.TabFocus, model.SectionAttention))
		assert.NotContains(t, scopesOf(got, 4), tab(notify.TabFocus), "manual unfocus beats rules")
		assert.NotContains(t, scopesOf(got, 1), tab(notify.TabFocus))
	})

	t.Run("stacked priority PR moves whole and shares section", func(t *testing.T) {
		t.Parallel()
		q := model.Queue{
			Inbox: []model.PullRequest{
				mk(5, "a", func(p *model.PullRequest) { p.DirectRequest = true; p.Stack = stack }),
				mk(6, "a", func(p *model.PullRequest) {
					p.Stack = stack
					p.Checks = model.ChecksSummary{HasRequiredChecks: true, ReqTotal: 1, ReqFailed: 1}
				}),
			},
			Viewer: "me",
		}
		got := config.Classify(q, prio, config.RuleSet{}, nil, now)
		want := []notify.Scope{tab(notify.TabPriority), sec(notify.TabPriority, model.SectionAttention)}
		assert.Equal(t, want, scopesOf(got, 5))
		assert.Equal(t, want, scopesOf(got, 6))
	})

	t.Run("sections agree with EffectiveSections", func(t *testing.T) {
		t.Parallel()
		q := model.Queue{
			Authored: []model.PullRequest{mk(1, "me", func(p *model.PullRequest) { p.IsDraft = true })},
			Inbox: []model.PullRequest{mk(2, "a", func(p *model.PullRequest) {
				p.Checks = model.ChecksSummary{HasRequiredChecks: true, ReqTotal: 1, ReqFailed: 1}
			})},
			Viewer: "me",
		}
		want := model.EffectiveSections(append(q.Authored, q.Inbox...), q.IsAuthored, now)
		for _, o := range config.Classify(q, prio, config.RuleSet{}, nil, now) {
			assert.Contains(t, o.Scopes, notify.Scope{Tab: o.Scopes[0].Tab, Section: want[o.PR.Key()]})
		}
	})

	t.Run("bot excluded from priority and focus rules", func(t *testing.T) {
		t.Parallel()
		bot := mk(7, "dependabot[bot]", func(p *model.PullRequest) { p.DirectRequest = true; p.AuthorIsBot = true })
		q := model.Queue{Inbox: []model.PullRequest{bot}, Viewer: "me"}
		focus := config.RuleSet{Authors: []string{"dependabot[bot]"}, ExcludeBots: true}
		got := config.Classify(q, prio, focus, nil, now)
		assert.Equal(t, tab(notify.TabInbox), scopesOf(got, 7)[0])
		assert.NotContains(t, scopesOf(got, 7), tab(notify.TabFocus))
	})

	t.Run("output order is authored then inbox", func(t *testing.T) {
		t.Parallel()
		q := model.Queue{
			Authored: []model.PullRequest{mk(9, "me", nil), mk(8, "me", nil)},
			Inbox:    []model.PullRequest{mk(7, "a", nil)},
			Viewer:   "me",
		}
		var nums []int
		for _, o := range config.Classify(q, prio, config.RuleSet{}, nil, now) {
			nums = append(nums, o.PR.Number)
		}
		assert.Equal(t, []int{9, 8, 7}, nums)
	})
}
