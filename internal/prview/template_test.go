package prview_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/prview"
)

func TestBuildCustomArgs_Substitution(t *testing.T) {
	t.Parallel()

	pr := model.PullRequest{
		Number:            42,
		Title:             "Fix auth bug in handler",
		URL:               "https://github.com/org/repo/pull/42",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		HeadRefName:       "fix-auth",
		BaseRefName:       "main",
	}

	tmpl := `echo "{title}" {number} {owner} {repo} {repo_with_owner} {head_ref} {base_ref} {url}`
	args, err := prview.BuildCustomArgs(tmpl, pr)
	require.NoError(t, err)

	expected := []string{
		"echo",
		"Fix auth bug in handler",
		"42",
		"org",
		"repo",
		"org/repo",
		"fix-auth",
		"main",
		"https://github.com/org/repo/pull/42",
	}
	assert.Equal(t, expected, args)
}

func TestBuildCustomArgs_EdgeCases(t *testing.T) {
	t.Parallel()

	pr := model.PullRequest{
		Number:            42,
		Title:             "Fix bug",
		URL:               "https://github.com/org/repo/pull/42",
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
	}

	t.Run("empty template returns error", func(t *testing.T) {
		t.Parallel()
		_, err := prview.BuildCustomArgs("", pr)
		require.Error(t, err)
	})

	t.Run("unmatched quotes return error", func(t *testing.T) {
		t.Parallel()
		_, err := prview.BuildCustomArgs(`my-tool "unmatched quote`, pr)
		require.Error(t, err)
	})

	t.Run("number-only PR substitutes number", func(t *testing.T) {
		t.Parallel()
		numberOnly := model.PullRequest{Number: 1}
		args, err := prview.BuildCustomArgs(`gh pr view {number}`, numberOnly)
		require.NoError(t, err)
		assert.Equal(t, []string{"gh", "pr", "view", "1"}, args)
	})
}
