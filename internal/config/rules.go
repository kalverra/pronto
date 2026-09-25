package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/kalverra/pronto/internal/model"
)

// RuleSet specifies criteria and rules for matching pull requests. It backs
// both the [focus] (auto-focus) and [priority] (Priority tab) sections.
type RuleSet struct {
	Authors []string `json:"authors,omitempty" mapstructure:"authors" toml:"authors"`
	Repos   []string `json:"repos,omitempty"   mapstructure:"repos"   toml:"repos"`
	Rules   []Rule   `json:"rules,omitempty"   mapstructure:"rules"   toml:"rules"`
	// ExcludeBots keeps bot-authored PRs from matching (see
	// model.PullRequest.IsAuthorBot). Enabled by default for [focus] and
	// [priority].
	ExcludeBots bool `json:"exclude_bots" mapstructure:"exclude_bots" toml:"exclude_bots"`
}

// IsEmpty reports whether any criteria or rules are configured.
func (c RuleSet) IsEmpty() bool {
	return len(c.Authors) == 0 && len(c.Repos) == 0 && len(c.Rules) == 0
}

// Validate reports the first rule regex that fails to compile.
func (c RuleSet) Validate() error {
	for _, rule := range c.Rules {
		for _, pattern := range rule.Regex {
			if _, err := compileRegex(pattern); err != nil {
				return fmt.Errorf("invalid rule regex %q: %w", pattern, err)
			}
		}
	}
	return nil
}

// Rule specifies matching criteria for pull requests within a repository or globally.
type Rule struct {
	Repo        string   `json:"repo,omitempty"        mapstructure:"repo"        toml:"repo"`
	Authors     []string `json:"authors,omitempty"     mapstructure:"authors"     toml:"authors"`
	Keywords    []string `json:"keywords,omitempty"    mapstructure:"keywords"    toml:"keywords"`
	Files       []string `json:"files,omitempty"       mapstructure:"files"       toml:"files"`
	Directories []string `json:"directories,omitempty" mapstructure:"directories" toml:"directories"`
	Paths       []string `json:"paths,omitempty"       mapstructure:"paths"       toml:"paths"`
	// Regex holds RE2 patterns matched against each changed file path.
	Regex []string `json:"regex,omitempty" mapstructure:"regex" toml:"regex"`
}

// Matches reports whether a pull request matches any configured authors, repos, or rules.
func (c RuleSet) Matches(pr model.PullRequest) bool {
	if c.ExcludeBots && pr.IsAuthorBot() {
		return false
	}
	for _, a := range c.Authors {
		if matchAuthor(a, pr.Author) {
			return true
		}
	}
	for _, r := range c.Repos {
		if matchRepo(r, pr) {
			return true
		}
	}
	for _, rule := range c.Rules {
		if rule.Matches(pr) {
			return true
		}
	}
	return false
}

// Matches reports whether a pull request matches this rule.
func (r Rule) Matches(pr model.PullRequest) bool {
	if r.Repo != "" && !matchRepo(r.Repo, pr) {
		return false
	}
	hasCriteria := len(r.Authors) > 0 || len(r.Keywords) > 0 || len(r.Files) > 0 || len(r.Directories) > 0 ||
		len(r.Paths) > 0 || len(r.Regex) > 0
	if !hasCriteria {
		return r.Repo != ""
	}
	for _, a := range r.Authors {
		if matchAuthor(a, pr.Author) {
			return true
		}
	}
	if len(r.Keywords) > 0 {
		titleLower := strings.ToLower(pr.Title)
		for _, kw := range r.Keywords {
			if kw != "" && strings.Contains(titleLower, strings.ToLower(kw)) {
				return true
			}
		}
	}
	for _, f := range r.Files {
		if matchFile(f, pr.Files) {
			return true
		}
	}
	for _, d := range r.Directories {
		if matchDirectory(d, pr.Files) {
			return true
		}
	}
	for _, p := range r.Paths {
		if matchPath(p, pr.Files) {
			return true
		}
	}
	for _, p := range r.Regex {
		if matchRegex(p, pr.Files) {
			return true
		}
	}
	return false
}

func matchAuthor(expected, actual string) bool {
	expected = strings.TrimPrefix(strings.TrimSpace(expected), "@")
	actual = strings.TrimPrefix(strings.TrimSpace(actual), "@")
	return strings.EqualFold(expected, actual)
}

func matchRepo(pattern string, pr model.PullRequest) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if strings.EqualFold(pattern, pr.RepoNameWithOwner) || strings.EqualFold(pattern, pr.RepoName) {
		return true
	}
	if !strings.Contains(pattern, "/") {
		if strings.EqualFold(pattern, pr.RepoName) {
			return true
		}
		if _, after, ok := strings.Cut(pr.RepoNameWithOwner, "/"); ok && strings.EqualFold(pattern, after) {
			return true
		}
	} else if pr.RepoNameWithOwner == "" && pr.RepoName != "" {
		if _, after, ok := strings.Cut(pattern, "/"); ok && strings.EqualFold(after, pr.RepoName) {
			return true
		}
	}
	return false
}

func matchFile(pattern string, files []string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	for _, f := range files {
		if strings.EqualFold(f, pattern) || strings.EqualFold(filepath.Base(f), pattern) {
			return true
		}
		if matched, err := filepath.Match(strings.ToLower(pattern), strings.ToLower(f)); err == nil && matched {
			return true
		}
		if matched, err := filepath.Match(
			strings.ToLower(pattern),
			strings.ToLower(filepath.Base(f)),
		); err == nil &&
			matched {
			return true
		}
	}
	return false
}

func matchDirectory(dir string, files []string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	clean := filepath.Clean(dir)
	prefix := clean + string(filepath.Separator)
	for _, f := range files {
		fClean := filepath.Clean(f)
		if strings.EqualFold(fClean, clean) || strings.HasPrefix(strings.ToLower(fClean), strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

func matchPath(path string, files []string) bool {
	return matchFile(path, files) || matchDirectory(path, files)
}

// regexCache memoizes compiled rule patterns; rules are matched on every
// render, so compiling per call would dominate.
var regexCache sync.Map // pattern -> *regexp.Regexp

func compileRegex(pattern string) (*regexp.Regexp, error) {
	if re, ok := regexCache.Load(pattern); ok {
		return re.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// matchRegex reports whether any file path matches pattern. Invalid patterns
// never match; Validate rejects them at load time.
func matchRegex(pattern string, files []string) bool {
	if pattern == "" {
		return false
	}
	re, err := compileRegex(pattern)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(files, re.MatchString)
}
