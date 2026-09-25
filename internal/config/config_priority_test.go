package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/model"
)

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pronto.toml"), []byte(content), 0o600))
}

func TestLoad_PriorityDefaults(t *testing.T) {
	t.Setenv("PRONTO_CONFIG_DIR", t.TempDir())

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.Priority.DirectRequests, "direct requests are priority by default")
	assert.Empty(t, cfg.Priority.Authors)
	assert.Empty(t, cfg.Priority.Repos)
	assert.Empty(t, cfg.Priority.Rules)
	assert.Equal(t, []string{config.GroupFocus, config.GroupMine, config.GroupPriority}, cfg.Notifications.Groups)
}

func TestLoad_PriorityFull(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	writeConfig(t, tmpDir, `
[priority]
direct_requests = false
authors = ["alice"]
repos = ["org/critical"]

[[priority.rules]]
repo = "org/app"
regex = ['^migrations/.*\.sql$']
directories = ["infra"]
`)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.False(t, cfg.Priority.DirectRequests)
	assert.Equal(t, []string{"alice"}, cfg.Priority.Authors)
	assert.Equal(t, []string{"org/critical"}, cfg.Priority.Repos)
	require.Len(t, cfg.Priority.Rules, 1)
	assert.Equal(t, []string{`^migrations/.*\.sql$`}, cfg.Priority.Rules[0].Regex)
	assert.Equal(t, []string{"infra"}, cfg.Priority.Rules[0].Directories)
}

func TestLoad_PriorityDirectRequestsEnvOverride(t *testing.T) {
	t.Setenv("PRONTO_CONFIG_DIR", t.TempDir())
	t.Setenv("PRONTO_PRIORITY_DIRECT_REQUESTS", "false")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.False(t, cfg.Priority.DirectRequests)
}

func TestLoad_InvalidRegexRejected(t *testing.T) {
	for _, section := range []string{"focus", "priority"} {
		t.Run(section, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
			writeConfig(t, tmpDir, "[["+section+".rules]]\nregex = ['(unclosed']\n")

			_, err := config.Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "(unclosed")
		})
	}
}

func TestRule_RegexMatchesFilePaths(t *testing.T) {
	t.Parallel()

	rule := config.Rule{Regex: []string{`^migrations/.*\.sql$`, `(?i)readme`}}

	assert.True(t, rule.Matches(model.PullRequest{Files: []string{"main.go", "migrations/001_init.sql"}}))
	assert.True(t, rule.Matches(model.PullRequest{Files: []string{"docs/README.md"}}))
	assert.False(t, rule.Matches(model.PullRequest{Files: []string{"db/migrations/001.sql"}}), "anchored regex")
	assert.False(t, rule.Matches(model.PullRequest{Files: []string{"main.go"}}))

	scoped := config.Rule{Repo: "org/app", Regex: []string{`\.proto$`}}
	assert.True(t, scoped.Matches(model.PullRequest{RepoNameWithOwner: "org/app", Files: []string{"api/v1.proto"}}))
	assert.False(t, scoped.Matches(model.PullRequest{RepoNameWithOwner: "org/other", Files: []string{"api/v1.proto"}}))
}

func TestPriorityConfig_Matches(t *testing.T) {
	t.Parallel()

	direct := model.PullRequest{Author: "zed", DirectRequest: true}
	assigned := model.PullRequest{Author: "zed", Assigned: true}
	teamOnly := model.PullRequest{Author: "zed"}
	byAlice := model.PullRequest{Author: "alice"}

	on := config.PriorityConfig{DirectRequests: true, Authors: []string{"alice"}}
	assert.True(t, on.Matches(direct))
	assert.True(t, on.Matches(assigned), "assignee counts as assigned to you")
	assert.False(t, on.Matches(teamOnly), "team-only request is not priority")
	assert.True(t, on.Matches(byAlice))

	off := config.PriorityConfig{Authors: []string{"alice"}}
	assert.False(t, off.Matches(direct))
	assert.False(t, off.Matches(assigned))
	assert.True(t, off.Matches(byAlice))

	assert.True(t, config.DefaultPriorityConfig().Matches(direct))
	assert.False(t, config.DefaultPriorityConfig().Matches(byAlice))
}

func TestLoad_ExcludeBotsDefaultsOn(t *testing.T) {
	t.Setenv("PRONTO_CONFIG_DIR", t.TempDir())

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.Focus.ExcludeBots)
	assert.True(t, cfg.Priority.ExcludeBots)
	assert.True(t, config.DefaultPriorityConfig().ExcludeBots)
}

func TestLoad_ExcludeBotsDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)
	writeConfig(t, tmpDir, `
[focus]
exclude_bots = false
authors = ["renovate"]

[priority]
exclude_bots = false
`)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.False(t, cfg.Focus.ExcludeBots)
	assert.False(t, cfg.Priority.ExcludeBots)
	assert.Equal(t, []string{"renovate"}, cfg.Focus.Authors)
}

func TestLoad_ExcludeBotsEnvOverride(t *testing.T) {
	t.Setenv("PRONTO_CONFIG_DIR", t.TempDir())
	t.Setenv("PRONTO_FOCUS_EXCLUDE_BOTS", "false")
	t.Setenv("PRONTO_PRIORITY_EXCLUDE_BOTS", "false")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.False(t, cfg.Focus.ExcludeBots)
	assert.False(t, cfg.Priority.ExcludeBots)
}

func TestRuleSet_ExcludeBots(t *testing.T) {
	t.Parallel()

	bot := model.PullRequest{Author: "dependabot[bot]", RepoNameWithOwner: "org/app", Files: []string{"go.mod"}}
	flagged := model.PullRequest{Author: "svc-account", AuthorIsBot: true, RepoNameWithOwner: "org/app"}
	human := model.PullRequest{Author: "alice", RepoNameWithOwner: "org/app"}

	rules := config.RuleSet{
		Repos: []string{"org/app"},
		Rules: []config.Rule{{Files: []string{"go.mod"}}},
	}
	assert.True(t, rules.Matches(bot), "bots match when exclude_bots is off")

	rules.ExcludeBots = true
	assert.False(t, rules.Matches(bot))
	assert.False(t, rules.Matches(flagged), "AuthorIsBot flag counts as bot")
	assert.True(t, rules.Matches(human))
}

func TestPriorityConfig_ExcludeBots(t *testing.T) {
	t.Parallel()

	botDirect := model.PullRequest{Author: "renovate", DirectRequest: true}
	botAssigned := model.PullRequest{Author: "github-actions[bot]", Assigned: true}
	humanDirect := model.PullRequest{Author: "alice", DirectRequest: true}

	cfg := config.DefaultPriorityConfig()
	assert.False(t, cfg.Matches(botDirect), "bot PRs never go to Priority by default, even if requested directly")
	assert.False(t, cfg.Matches(botAssigned))
	assert.True(t, cfg.Matches(humanDirect))

	cfg.ExcludeBots = false
	assert.True(t, cfg.Matches(botDirect))
	assert.True(t, cfg.Matches(botAssigned))
}
