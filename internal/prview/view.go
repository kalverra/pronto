// Package prview provides pull request viewer implementations.
package prview

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cli/go-gh/v2/pkg/browser"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
)

// BuildTerminalArgs builds argv for the terminal gh pr view command.
func BuildTerminalArgs(pr model.PullRequest) ([]string, error) {
	target, err := prTarget(pr)
	if err != nil {
		return nil, err
	}
	return []string{"gh", "pr", "view", target}, nil
}

// prTarget resolves the gh CLI target reference for a PR: its URL, or
// repo#number, or bare number.
func prTarget(pr model.PullRequest) (string, error) {
	target := pr.URL
	if target == "" {
		if pr.RepoNameWithOwner != "" && pr.Number > 0 {
			target = pr.Key().String()
		} else if pr.Number > 0 {
			target = strconv.Itoa(pr.Number)
		}
	}
	if target == "" {
		return "", errors.New("cannot view PR: missing URL and repo info")
	}
	return target, nil
}

// BuildVSCodeArgs builds argv for opening the PR overview panel in VSCode via
// URL handler.
func BuildVSCodeArgs(pr model.PullRequest) ([]string, error) {
	if pr.URL == "" {
		return nil, errors.New("cannot view PR in vscode: missing URL")
	}
	uri := "vscode://github.vscode-pull-request-github/open-pull-request-webview?uri=" + url.QueryEscape(pr.URL)
	return []string{"code", "--open-url", uri}, nil
}

// Options configures viewer resolution.
type Options struct {
	Opener func(string) error
}

// Option configures viewer construction.
type Option func(*Options)

// WithOpener sets a custom URL opener for web view.
func WithOpener(opener func(string) error) Option {
	return func(o *Options) {
		o.Opener = opener
	}
}

// ResolveViewer constructs a tui.ViewPRFunc for inspecting PR details.
func ResolveViewer(cfg config.Config, opts ...Option) (tui.ViewPRFunc, error) {
	var o Options
	for _, opt := range opts {
		opt(&o)
	}

	view := cfg.PRView
	if view == "" {
		view = config.ViewCondensed
	}

	switch view {
	case config.ViewCondensed:
		return nil, nil
	case config.ViewTerminal:
		return TerminalViewer, nil
	case config.ViewVSCode:
		return VSCodeViewer, nil
	case config.ViewWeb:
		return WebViewer(o.Opener), nil
	case config.ViewCustom:
		if strings.TrimSpace(cfg.PRViewCommand) == "" {
			return nil, errors.New("pr_view 'custom' requires pr_view_command")
		}
		return CustomViewer(cfg.PRViewCommand), nil
	default:
		return nil, fmt.Errorf("unknown pr_view: %q", view)
	}
}

// TerminalViewer opens a PR in the terminal using gh pr view.
func TerminalViewer(pr model.PullRequest) tea.Cmd {
	args, err := BuildTerminalArgs(pr)
	if err != nil {
		return func() tea.Msg {
			return tui.ViewPRMsg{PR: pr, Err: err}
		}
	}
	// #nosec G204 -- arguments constructed internally.
	//nolint:noctx // Interactive terminal pager process managed by Bubbletea ExecProcess.
	c := exec.Command(args[0], args[1:]...)
	c.Env = tui.ViewPREnv()
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return tui.ViewPRMsg{PR: pr, Err: err}
	})
}

// VSCodeViewer opens a PR overview panel in VSCode.
func VSCodeViewer(pr model.PullRequest) tea.Cmd {
	args, err := BuildVSCodeArgs(pr)
	if err != nil {
		return func() tea.Msg {
			return tui.ViewPRMsg{PR: pr, Err: err}
		}
	}
	return func() tea.Msg {
		// #nosec G204 -- arguments constructed internally.
		//nolint:noctx // CLI command fires off to launch desktop editor.
		c := exec.Command(args[0], args[1:]...)
		return tui.ViewPRMsg{PR: pr, Err: c.Run()}
	}
}

// WebViewer opens a PR in the web browser.
func WebViewer(opener func(string) error) tui.ViewPRFunc {
	if opener == nil {
		opener = func(rawURL string) error {
			return browser.New("", nil, nil).Browse(rawURL)
		}
	}
	return func(pr model.PullRequest) tea.Cmd {
		if pr.URL == "" {
			return func() tea.Msg {
				return tui.ViewPRMsg{PR: pr, Err: errors.New("cannot view PR: missing URL")}
			}
		}
		return func() tea.Msg {
			return tui.ViewPRMsg{PR: pr, Err: opener(pr.URL)}
		}
	}
}

// CustomViewer opens a PR using a user-specified command template.
func CustomViewer(tmpl string) tui.ViewPRFunc {
	return func(pr model.PullRequest) tea.Cmd {
		args, err := BuildCustomArgs(tmpl, pr)
		if err != nil {
			return func() tea.Msg {
				return tui.ViewPRMsg{PR: pr, Err: err}
			}
		}
		// #nosec G204 -- user-configured command template.
		//nolint:noctx // Interactive process managed by Bubbletea ExecProcess.
		c := exec.Command(args[0], args[1:]...)
		c.Env = tui.ViewPREnv()
		return tea.ExecProcess(c, func(err error) tea.Msg {
			return tui.ViewPRMsg{PR: pr, Err: err}
		})
	}
}
