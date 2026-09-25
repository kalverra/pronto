package config

import "github.com/kalverra/pronto/internal/model"

// PriorityConfig selects incoming pull requests for the Priority tab: those
// matching the embedded rules, plus (when DirectRequests is set) those whose
// review is requested from the viewer personally or that are assigned to them.
type PriorityConfig struct {
	RuleSet `mapstructure:",squash"`

	DirectRequests bool `json:"direct_requests" mapstructure:"direct_requests" toml:"direct_requests"`
}

// DefaultPriorityConfig returns the priority config used when none is loaded:
// direct review requests and assignments only, excluding bot authors.
func DefaultPriorityConfig() PriorityConfig {
	return PriorityConfig{DirectRequests: true, ExcludeBots: true}
}

// Matches reports whether a pull request belongs in the Priority tab.
func (c PriorityConfig) Matches(pr model.PullRequest) bool {
	if c.ExcludeBots && pr.IsAuthorBot() {
		return false
	}
	if c.DirectRequests && (pr.DirectRequest || pr.Assigned) {
		return true
	}
	return c.RuleSet.Matches(pr)
}

// Partition splits incoming PRs into Priority and the rest. A stack moves
// whole: if any member matches, every member is priority, so a stack never
// splits across tabs.
func (c PriorityConfig) Partition(prs []model.PullRequest) (priority, rest []model.PullRequest) {
	stacks := make(map[string]bool)
	for _, pr := range prs {
		if pr.IsPartOfStack() && c.Matches(pr) {
			stacks[pr.StackKey()] = true
		}
	}
	for _, pr := range prs {
		if c.Matches(pr) || (pr.IsPartOfStack() && stacks[pr.StackKey()]) {
			priority = append(priority, pr)
		} else {
			rest = append(rest, pr)
		}
	}
	return priority, rest
}
