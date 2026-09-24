package tui

import "github.com/kalverra/pronto/internal/model"

// SortStrategy defines how pull requests are grouped and ordered in the TUI.
type SortStrategy int

const (
	// SortAction groups PRs by action/urgency status category (default).
	SortAction SortStrategy = iota
	// SortRepo groups PRs alphabetically by repository name.
	SortRepo
	// SortUpdated orders PRs chronologically by last updated timestamp descending.
	SortUpdated
	// SortScore orders PRs purely by computed priority score descending.
	SortScore
)

var allSortStrategies = []SortStrategy{
	SortAction,
	SortRepo,
	SortUpdated,
	SortScore,
}

// String returns the short identifier for the sort strategy.
func (s SortStrategy) String() string {
	switch s {
	case SortAction:
		return "action"
	case SortRepo:
		return "repo"
	case SortUpdated:
		return "updated"
	case SortScore:
		return "score"
	default:
		return "action"
	}
}

// Label returns the human-readable display label for the sort strategy.
func (s SortStrategy) Label() string {
	switch s {
	case SortAction:
		return "Action"
	case SortRepo:
		return "Repo"
	case SortUpdated:
		return "Updated"
	case SortScore:
		return "Score"
	default:
		return "Action"
	}
}

// Next returns the next SortStrategy cycling by delta (+1 forward, -1 backward).
func (s SortStrategy) Next(delta int) SortStrategy {
	n := len(allSortStrategies)
	cur := 0
	for i, start := range allSortStrategies {
		if start == s {
			cur = i
			break
		}
	}
	next := (cur + delta) % n
	if next < 0 {
		next += n
	}
	return allSortStrategies[next]
}

// prRepoKey returns the normalized repository identifier for a pull request.
func prRepoKey(pr model.PullRequest) string {
	if pr.RepoNameWithOwner != "" {
		return pr.RepoNameWithOwner
	}
	return pr.RepoName
}
