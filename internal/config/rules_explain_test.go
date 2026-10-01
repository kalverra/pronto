package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/model"
)

func TestRule_Explain(t *testing.T) {
	t.Parallel()

	pr := model.PullRequest{
		Number:            101,
		RepoNameWithOwner: "org/repo",
		RepoName:          "repo",
		Author:            "alice",
		Title:             "Fix security vulnerability in auth",
		Files:             []string{"internal/auth/token.go", "docs/auth.md"},
	}

	t.Run("matches author", func(t *testing.T) {
		t.Parallel()
		r := config.Rule{Authors: []string{"alice"}}
		assert.Contains(t, r.Explain(pr), "author @alice")
	})

	t.Run("matches keyword", func(t *testing.T) {
		t.Parallel()
		r := config.Rule{Keywords: []string{"security"}}
		assert.Contains(t, r.Explain(pr), `keyword "security" in title`)
	})

	t.Run("matches file", func(t *testing.T) {
		t.Parallel()
		r := config.Rule{Files: []string{"token.go"}}
		assert.Contains(t, r.Explain(pr), `file "token.go"`)
	})

	t.Run("matches directory", func(t *testing.T) {
		t.Parallel()
		r := config.Rule{Directories: []string{"internal/auth"}}
		assert.Contains(t, r.Explain(pr), `directory "internal/auth"`)
	})

	t.Run("matches regex", func(t *testing.T) {
		t.Parallel()
		r := config.Rule{Regex: []string{`^docs/.*\.md$`}}
		assert.Contains(t, r.Explain(pr), `matched regex`)
		assert.Contains(t, r.Explain(pr), `docs/.*`)
	})

	t.Run("matches repo only", func(t *testing.T) {
		t.Parallel()
		r := config.Rule{Repo: "org/repo"}
		assert.Contains(t, r.Explain(pr), `repo "org/repo"`)
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		r := config.Rule{Keywords: []string{"unrelated"}}
		assert.Empty(t, r.Explain(pr))
	})
}

func TestRuleSet_Explain(t *testing.T) {
	t.Parallel()

	pr := model.PullRequest{
		Number:            101,
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Title:             "Refactor api",
	}

	t.Run("matches top-level author", func(t *testing.T) {
		t.Parallel()
		rs := config.RuleSet{Authors: []string{"alice"}}
		assert.Contains(t, rs.Explain(pr), "author @alice")
	})

	t.Run("matches top-level repo", func(t *testing.T) {
		t.Parallel()
		rs := config.RuleSet{Repos: []string{"org/repo"}}
		assert.Contains(t, rs.Explain(pr), "repo org/repo")
	})

	t.Run("matches nested rule", func(t *testing.T) {
		t.Parallel()
		rs := config.RuleSet{
			Rules: []config.Rule{{Keywords: []string{"api"}}},
		}
		assert.Contains(t, rs.Explain(pr), `keyword "api"`)
	})

	t.Run("exclude bots", func(t *testing.T) {
		t.Parallel()
		botPR := model.PullRequest{
			Number:      102,
			Author:      "dependabot[bot]",
			AuthorIsBot: true,
			Title:       "Bump lodash",
		}
		rs := config.RuleSet{
			ExcludeBots: true,
			Authors:     []string{"dependabot[bot]"},
		}
		assert.Empty(t, rs.Explain(botPR))
	})
}

func TestPriorityConfig_Explain(t *testing.T) {
	t.Parallel()

	stack := &model.PRStack{ID: "S1", Size: 2}

	prDirect := model.PullRequest{
		Number:            1,
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		DirectRequest:     true,
	}

	prAssigned := model.PullRequest{
		Number:            2,
		RepoNameWithOwner: "org/repo",
		Author:            "alice",
		Assigned:          true,
	}

	prStackMember1 := model.PullRequest{
		Number:            10,
		RepoNameWithOwner: "org/repo",
		Author:            "bob",
		DirectRequest:     true,
		Stack:             stack,
	}

	prStackMember2 := model.PullRequest{
		Number:            11,
		RepoNameWithOwner: "org/repo",
		Author:            "bob",
		DirectRequest:     false,
		Stack:             stack,
	}

	cfg := config.DefaultPriorityConfig()

	t.Run("direct request", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, cfg.Explain(prDirect, nil), "Direct review requested")
	})

	t.Run("assigned", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, cfg.Explain(prAssigned, nil), "Assigned to you")
	})

	t.Run("stack promotion", func(t *testing.T) {
		t.Parallel()
		allInbox := []model.PullRequest{prStackMember1, prStackMember2}
		assert.Contains(t, cfg.Explain(prStackMember2, allInbox), "Promoted by stack entry #10")
	})

	t.Run("rule match", func(t *testing.T) {
		t.Parallel()
		prioRule := config.PriorityConfig{
			Rules: []config.Rule{{Keywords: []string{"critical"}}},
		}
		prCrit := model.PullRequest{
			Number:            3,
			RepoNameWithOwner: "org/repo",
			Title:             "A critical bugfix",
		}
		assert.Contains(t, prioRule.Explain(prCrit, nil), `Matched priority rule: matched keyword "critical"`)
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		prOrdinary := model.PullRequest{
			Number:            4,
			RepoNameWithOwner: "org/repo",
			Title:             "Unrelated docs change",
		}
		assert.Empty(t, cfg.Explain(prOrdinary, nil))
	})
}
