package prview_test

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/prview"
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

	t.Run("diff names rejected as view", func(t *testing.T) {
		t.Parallel()
		cfg := config.Config{PRView: "difftastic"}
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
