package prview_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/prview"
	"github.com/kalverra/pronto/internal/tui"
)

func TestBuildTerminalArgs(t *testing.T) {
	t.Parallel()

	t.Run("with URL", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			Number: 101,
			URL:    "https://github.com/org/repo/pull/101",
		}
		args, err := prview.BuildTerminalArgs(pr)
		require.NoError(t, err)
		assert.Equal(t, []string{"gh", "pr", "view", "https://github.com/org/repo/pull/101"}, args)
	})

	t.Run("without URL but with repo and number", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			Number:            101,
			RepoNameWithOwner: "org/repo",
		}
		args, err := prview.BuildTerminalArgs(pr)
		require.NoError(t, err)
		assert.Equal(t, []string{"gh", "pr", "view", "org/repo#101"}, args)
	})

	t.Run("missing target", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{}
		_, err := prview.BuildTerminalArgs(pr)
		require.Error(t, err)
	})
}

func TestBuildVSCodeArgs(t *testing.T) {
	t.Parallel()

	t.Run("with URL", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{
			Number: 101,
			URL:    "https://github.com/org/repo/pull/101",
		}
		args, err := prview.BuildVSCodeArgs(pr)
		require.NoError(t, err)
		expectedURI := "vscode://github.vscode-pull-request-github/open-pull-request-webview?uri=" + url.QueryEscape(
			pr.URL,
		)
		assert.Equal(t, []string{"code", "--open-url", expectedURI}, args)
	})

	t.Run("missing URL", func(t *testing.T) {
		t.Parallel()
		pr := model.PullRequest{Number: 101}
		_, err := prview.BuildVSCodeArgs(pr)
		require.Error(t, err)
	})
}

func TestBuildDifftasticArgs(t *testing.T) {
	t.Parallel()

	args := prview.BuildDifftasticArgs("/tmp/base", "/tmp/head")
	assert.Equal(t, []string{"difft", "--skip-unchanged", "/tmp/base", "/tmp/head"}, args)
}

func TestZedViewer_MultiDiffDirs(t *testing.T) {
	t.Parallel()

	fake := &fakeMaterializer{
		diff: &prview.MaterializedDiff{
			BaseDir: "/cache/diffs/org-repo-42/base",
			HeadDir: "/cache/diffs/org-repo-42/head",
			Files:   []string{"main.go", "internal/auth.go"},
		},
	}
	viewer := prview.ZedViewer(fake)

	pr := model.PullRequest{
		Number:            42,
		URL:               "https://github.com/org/repo/pull/42",
		RepoNameWithOwner: "org/repo",
	}
	msg := viewer(pr)()

	ready, ok := msg.(tui.DiffReadyMsg)
	require.True(t, ok)
	require.NoError(t, ready.Err)
	assert.Equal(t, []string{
		"zed", "--diff",
		"/cache/diffs/org-repo-42/base", "/cache/diffs/org-repo-42/head",
	}, ready.Args)
}

func TestZedViewer_MaterializeError(t *testing.T) {
	t.Parallel()

	fake := &fakeMaterializer{err: errors.New("compare API returned 500")}
	viewer := prview.ZedViewer(fake)

	pr := model.PullRequest{
		Number:            42,
		RepoNameWithOwner: "org/repo",
	}
	msg := viewer(pr)()

	ready, ok := msg.(tui.DiffReadyMsg)
	require.True(t, ok)
	require.Error(t, ready.Err)
	assert.Contains(t, ready.Err.Error(), "compare API returned 500")
}

type fakeMaterializer struct {
	diff *prview.MaterializedDiff
	err  error
}

func (f *fakeMaterializer) Materialize(_ context.Context, _ model.PullRequest) (*prview.MaterializedDiff, error) {
	return f.diff, f.err
}

func TestResolveViewer(t *testing.T) {
	t.Parallel()

	views := []string{
		config.ViewTerminal,
		config.ViewVSCode,
		config.ViewWeb,
		config.ViewCustom,
	}

	for _, v := range views {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{
				PRView:        v,
				PRViewCommand: "echo {url}",
			}
			viewer, err := prview.ResolveViewer(cfg)
			require.NoError(t, err)
			assert.NotNil(t, viewer)
		})
	}

	t.Run("condensed returns nil", func(t *testing.T) {
		t.Parallel()
		cfg := config.Config{PRView: config.ViewCondensed}
		viewer, err := prview.ResolveViewer(cfg)
		require.NoError(t, err)
		assert.Nil(t, viewer)
	})

	t.Run("difftastic rejected as view", func(t *testing.T) {
		t.Parallel()
		cfg := config.Config{PRView: config.DiffDifftastic}
		_, err := prview.ResolveViewer(cfg)
		require.Error(t, err)
	})

	t.Run("unknown view", func(t *testing.T) {
		t.Parallel()
		cfg := config.Config{PRView: "unknown"}
		_, err := prview.ResolveViewer(cfg)
		require.Error(t, err)
	})
}

func TestResolveDiffViewer(t *testing.T) {
	t.Parallel()

	diffs := []string{
		config.DiffDifftastic,
		config.DiffZed,
		config.DiffTerminal,
		config.DiffVSCode,
		config.DiffWeb,
		config.DiffCustom,
	}

	for _, d := range diffs {
		t.Run(d, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{
				PRDiff:        d,
				PRDiffCommand: "echo {url}",
			}
			differ, err := prview.ResolveDiffViewer(cfg)
			require.NoError(t, err)
			assert.NotNil(t, differ)
		})
	}

	t.Run("unknown diff", func(t *testing.T) {
		t.Parallel()
		cfg := config.Config{PRDiff: "unknown"}
		_, err := prview.ResolveDiffViewer(cfg)
		require.Error(t, err)
	})
}
