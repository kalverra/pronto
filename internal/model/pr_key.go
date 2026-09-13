package model

import "fmt"

// PRKey uniquely identifies a pull request by repository and number.
type PRKey struct {
	Repo   string
	Number int
}

// String returns canonical "repo#num" representation.
func (k PRKey) String() string {
	return fmt.Sprintf("%s#%d", k.Repo, k.Number)
}

// Key returns the PRKey for this pull request.
func (pr PullRequest) Key() PRKey {
	return PRKey{
		Repo:   pr.RepoNameWithOwner,
		Number: pr.Number,
	}
}
