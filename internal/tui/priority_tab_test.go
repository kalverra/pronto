package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

var priorityNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

func priorityPR(num int, author string, mut func(*model.PullRequest)) model.PullRequest {
	pr := model.PullRequest{
		Number:            num,
		Title:             "PR " + author,
		Author:            author,
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		Mergeable:         "MERGEABLE",
		MergeStateStatus:  "CLEAN",
		MergeStatus:       model.ComputeMergeStatus("MERGEABLE", "CLEAN", false),
		TimelineItems: []model.TimelineItem{
			{Type: model.TimelineItemReviewRequested, CreatedAt: priorityNow.Add(-time.Hour), ReviewerTeam: "org/core"},
		},
	}
	if mut != nil {
		mut(&pr)
	}
	return pr
}

func itemNumbers(m tui.Model, tab tui.Tab) []int {
	var items []int
	switch tab {
	case tui.TabPriority:
		for _, it := range m.PriorityItems() {
			items = append(items, it.PR.Number)
		}
	case tui.TabInbox:
		for _, it := range m.InboxItems() {
			items = append(items, it.PR.Number)
		}
	case tui.TabFocus:
		for _, it := range m.FocusItems() {
			items = append(items, it.PR.Number)
		}
	}
	return items
}

func TestPriorityTab_DirectRequestsByDefault(t *testing.T) {
	t.Parallel()

	q := model.Queue{Inbox: []model.PullRequest{
		priorityPR(1, "alice", func(pr *model.PullRequest) { pr.DirectRequest = true }),
		priorityPR(2, "bob", func(pr *model.PullRequest) { pr.Assigned = true }),
		priorityPR(3, "carol", nil), // team-only request
	}}
	m := tui.New(q, tui.WithNow(priorityNow), tui.WithViewer("viewer"))

	assert.ElementsMatch(t, []int{1, 2}, itemNumbers(m, tui.TabPriority))
	assert.Equal(t, []int{3}, itemNumbers(m, tui.TabInbox), "priority PRs must not also appear in Inbox")
}

func TestPriorityTab_DirectRequestsDisabled(t *testing.T) {
	t.Parallel()

	q := model.Queue{Inbox: []model.PullRequest{
		priorityPR(1, "alice", func(pr *model.PullRequest) { pr.DirectRequest = true }),
	}}
	m := tui.New(q, tui.WithNow(priorityNow), tui.WithPriorityConfig(config.PriorityConfig{}))

	assert.Empty(t, itemNumbers(m, tui.TabPriority))
	assert.Equal(t, []int{1}, itemNumbers(m, tui.TabInbox))
}

func TestPriorityTab_RulesRoute(t *testing.T) {
	t.Parallel()

	q := model.Queue{Inbox: []model.PullRequest{
		priorityPR(1, "vip", nil),
		priorityPR(2, "bob", func(pr *model.PullRequest) { pr.RepoNameWithOwner = "org/critical" }),
		priorityPR(3, "carol", func(pr *model.PullRequest) { pr.Files = []string{"db/migrations/001.sql"} }),
		priorityPR(4, "dave", func(pr *model.PullRequest) { pr.Files = []string{"main.go"} }),
	}}
	cfg := config.PriorityConfig{
		Authors: []string{"vip"},
		Repos:   []string{"org/critical"},
		Rules:   []config.Rule{{Regex: []string{`migrations/.*\.sql$`}}},
	}
	m := tui.New(q, tui.WithNow(priorityNow), tui.WithPriorityConfig(cfg))

	assert.ElementsMatch(t, []int{1, 2, 3}, itemNumbers(m, tui.TabPriority))
	assert.Equal(t, []int{4}, itemNumbers(m, tui.TabInbox))
}

func TestPriorityTab_StackMovesWhole(t *testing.T) {
	t.Parallel()

	stack := func(pos int) func(*model.PullRequest) {
		return func(pr *model.PullRequest) {
			pr.Stack = &model.PRStack{ID: "S1", Size: 2, Position: pos}
			if pos == 2 {
				pr.DirectRequest = true
			}
		}
	}
	q := model.Queue{Inbox: []model.PullRequest{
		priorityPR(10, "alice", stack(1)),
		priorityPR(11, "alice", stack(2)),
		priorityPR(12, "bob", nil),
	}}
	m := tui.New(q, tui.WithNow(priorityNow))

	assert.ElementsMatch(t, []int{10, 11}, itemNumbers(m, tui.TabPriority), "stack must not split across tabs")
	assert.Equal(t, []int{12}, itemNumbers(m, tui.TabInbox))
}

func TestPriorityTab_FocusIsOnlyDuplicate(t *testing.T) {
	t.Parallel()

	q := model.Queue{Inbox: []model.PullRequest{
		priorityPR(1, "alice", func(pr *model.PullRequest) { pr.DirectRequest = true }),
		priorityPR(2, "bob", nil),
	}}
	m := tui.New(q,
		tui.WithNow(priorityNow),
		tui.WithFocusConfig(config.RuleSet{Authors: []string{"alice", "bob"}}),
	)

	assert.Equal(t, []int{1}, itemNumbers(m, tui.TabPriority))
	assert.Equal(t, []int{2}, itemNumbers(m, tui.TabInbox))
	assert.ElementsMatch(t, []int{1, 2}, itemNumbers(m, tui.TabFocus))
}

func TestPriorityTab_RendersAndSorts(t *testing.T) {
	t.Parallel()

	q := model.Queue{Inbox: []model.PullRequest{
		priorityPR(1, "alice", func(pr *model.PullRequest) { pr.DirectRequest = true }),
		priorityPR(2, "bob", func(pr *model.PullRequest) { pr.DirectRequest = true }),
		priorityPR(3, "carol", nil),
	}}
	m := tui.New(q, tui.WithNow(priorityNow), tui.WithViewer("viewer"), tui.WithDimensions(140, 40))

	view := m.View()
	assert.Contains(t, view, "3: Priority (2)")
	assert.Contains(t, view, "4: Inbox (1)")

	mNav, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = mNav.(tui.Model)
	require.Equal(t, tui.TabPriority, m.ActiveTab())
	require.NotNil(t, m.SelectedPR())

	view = m.View()
	assert.Contains(t, view, "NEEDS YOUR ATTENTION")
	assert.Contains(t, view, "#1")
	assert.Contains(t, view, "#2")
	assert.NotContains(t, view, "#3 ")

	for range 2 { // Action -> Repo -> Author
		mSort, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
		m = mSort.(tui.Model)
	}
	require.Equal(t, tui.SortAuthor, m.SortStrategy())
	view = m.View()
	assert.Less(t, strings.Index(view, "@ALICE"), strings.Index(view, "@BOB"))
}

func TestPriorityTab_BotsStayInInboxByDefault(t *testing.T) {
	t.Parallel()

	q := model.Queue{Inbox: []model.PullRequest{
		priorityPR(1, "renovate[bot]", func(pr *model.PullRequest) { pr.DirectRequest = true }),
		priorityPR(2, "alice", func(pr *model.PullRequest) { pr.DirectRequest = true }),
	}}
	m := tui.New(q, tui.WithNow(priorityNow))

	assert.Equal(t, []int{2}, itemNumbers(m, tui.TabPriority))
	assert.Equal(t, []int{1}, itemNumbers(m, tui.TabInbox))
}

func TestFocus_ExcludeBotsSkipsRulesButNotManualFocus(t *testing.T) {
	t.Parallel()

	bot := priorityPR(1, "dependabot[bot]", nil)
	bot2 := priorityPR(2, "renovate[bot]", nil)
	q := model.Queue{Inbox: []model.PullRequest{bot, bot2}}
	m := tui.New(q,
		tui.WithNow(priorityNow),
		tui.WithFocusConfig(config.RuleSet{ExcludeBots: true, Repos: []string{"org/repo"}}),
		tui.WithFocusedPRs([]model.PRKey{bot2.Key()}),
	)

	assert.Equal(t, []int{2}, itemNumbers(m, tui.TabFocus), "manual focus survives exclude_bots; rule match does not")
}
