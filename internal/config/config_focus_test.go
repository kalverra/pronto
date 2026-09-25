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

func TestLoad_FocusDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.Focus.Authors)
	assert.Empty(t, cfg.Focus.Repos)
	assert.Empty(t, cfg.Focus.Rules)
}

func TestLoad_FocusFull(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)

	tomlContent := `
[focus]
authors = ["alice", "bob"]
repos = ["kalverra/pronto", "org/repo-b"]

[[focus.rules]]
repo = "kalverra/pronto"
keywords = ["urgent", "security"]
files = ["go.mod", "schema.sql"]
directories = ["cmd/pronto", "internal/notify"]
authors = ["charlie"]
paths = ["docs/architecture.md"]
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(tomlContent), 0o600)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"alice", "bob"}, cfg.Focus.Authors)
	assert.Equal(t, []string{"kalverra/pronto", "org/repo-b"}, cfg.Focus.Repos)
	require.Len(t, cfg.Focus.Rules, 1)

	r := cfg.Focus.Rules[0]
	assert.Equal(t, "kalverra/pronto", r.Repo)
	assert.Equal(t, []string{"urgent", "security"}, r.Keywords)
	assert.Equal(t, []string{"go.mod", "schema.sql"}, r.Files)
	assert.Equal(t, []string{"cmd/pronto", "internal/notify"}, r.Directories)
	assert.Equal(t, []string{"charlie"}, r.Authors)
	assert.Equal(t, []string{"docs/architecture.md"}, r.Paths)
}

func TestLoad_AutoFocusAlias(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PRONTO_CONFIG_DIR", tmpDir)

	tomlContent := `
[auto_focus]
authors = ["alice"]
repos = ["kalverra/pronto"]

[[auto_focus.rules]]
repo = "kalverra/pronto"
keywords = ["urgent"]
`
	err := os.WriteFile(filepath.Join(tmpDir, "pronto.toml"), []byte(tomlContent), 0o600)
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"alice"}, cfg.Focus.Authors)
	assert.Equal(t, []string{"kalverra/pronto"}, cfg.Focus.Repos)
	require.Len(t, cfg.Focus.Rules, 1)
	assert.Equal(t, []string{"urgent"}, cfg.Focus.Rules[0].Keywords)
}

func TestFocus_Matches(t *testing.T) {
	t.Parallel()

	cfg := config.FocusConfig{
		Authors: []string{"alice", "@bob"},
		Repos:   []string{"kalverra/pronto"},
		Rules: []config.FocusRule{
			{
				Repo:        "org/backend",
				Keywords:    []string{"auth", "security"},
				Files:       []string{"schema.sql", "*.proto"},
				Directories: []string{"internal/auth", "api/v1/"},
				Authors:     []string{"dan"},
				Paths:       []string{"config/secrets.toml"},
			},
			{
				Repo: "org/all-prs",
			},
		},
	}

	// 1. Matches top-level author
	assert.True(t, cfg.Matches(model.PullRequest{
		Author:            "alice",
		RepoNameWithOwner: "other/repo",
	}))
	assert.True(t, cfg.Matches(model.PullRequest{
		Author:            "bob", // strips @
		RepoNameWithOwner: "other/repo",
	}))

	// 2. Matches top-level repo
	assert.True(t, cfg.Matches(model.PullRequest{
		Author:            "someone",
		RepoNameWithOwner: "kalverra/pronto",
	}))
	assert.True(t, cfg.Matches(model.PullRequest{
		Author:   "someone",
		RepoName: "pronto", // matches short name
	}))

	// 3. Matches rule repo-only
	assert.True(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "org/all-prs",
	}))

	// 4. Matches rule in org/backend by keyword
	assert.True(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "org/backend",
		Title:             "Fix [Security] flaw in token parser",
	}))

	// 5. Matches rule in org/backend by file exact or glob
	assert.True(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "org/backend",
		Title:             "Update database",
		Files:             []string{"migrations/schema.sql"},
	}))
	assert.True(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "org/backend",
		Title:             "Update service",
		Files:             []string{"proto/service.proto"},
	}))

	// 6. Matches rule in org/backend by directory
	assert.True(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "org/backend",
		Title:             "Clean up handlers",
		Files:             []string{"internal/auth/handler.go"},
	}))
	assert.True(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "org/backend",
		Title:             "Endpoint change",
		Files:             []string{"api/v1/user.go"},
	}))

	// 7. Matches rule in org/backend by path
	assert.True(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "org/backend",
		Title:             "Update secrets",
		Files:             []string{"config/secrets.toml"},
	}))

	// 8. Matches rule in org/backend by author
	assert.True(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "org/backend",
		Author:            "dan",
		Title:             "Regular PR",
	}))

	// 9. Does not match when wrong repo
	assert.False(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "other/repo",
		Title:             "auth update",
		Files:             []string{"internal/auth/handler.go"},
	}))

	// 10. Does not match when in org/backend but no criteria matched
	assert.False(t, cfg.Matches(model.PullRequest{
		RepoNameWithOwner: "org/backend",
		Author:            "eve",
		Title:             "Routine chore",
		Files:             []string{"cmd/server/main.go"},
	}))
}
