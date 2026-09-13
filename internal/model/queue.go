package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	// ErrNotFound is returned when a referenced pull request cannot be located.
	ErrNotFound = errors.New("pull request not found")
	// ErrAmbiguousRef is returned when a bare number matches multiple pull requests across repositories.
	ErrAmbiguousRef = errors.New("ambiguous pull request reference")
)

// Queue holds the user's authored and inbox pull requests.
type Queue struct {
	Authored []PullRequest `json:"authored,omitempty"`
	Inbox    []PullRequest `json:"inbox,omitempty"`

	// Viewer is the authenticated user's login; Teams are their org-qualified
	// teams ("org/slug"). Sources populate both so scoring call sites can
	// attribute review requests to the viewer or their teams.
	Viewer string   `json:"viewer,omitempty"`
	Teams  []string `json:"teams,omitempty"`
}

// Find resolves a PR reference such as "123", "#123", or "org/repo#123" against the queue.
func (q Queue) Find(ref string) (*PullRequest, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrNotFound
	}

	if parts := strings.Split(ref, "#"); len(parts) == 2 && parts[0] != "" {
		repo := parts[0]
		num, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, ErrNotFound
		}

		for i := range q.Authored {
			if q.Authored[i].RepoNameWithOwner == repo && q.Authored[i].Number == num {
				return &q.Authored[i], nil
			}
		}
		for i := range q.Inbox {
			if q.Inbox[i].RepoNameWithOwner == repo && q.Inbox[i].Number == num {
				return &q.Inbox[i], nil
			}
		}
		return nil, ErrNotFound
	}

	rawNum := strings.TrimPrefix(ref, "#")
	num, err := strconv.Atoi(rawNum)
	if err != nil {
		return nil, ErrNotFound
	}

	var matches []*PullRequest
	seenRepos := make(map[string]bool)

	for i := range q.Authored {
		if q.Authored[i].Number == num {
			if !seenRepos[q.Authored[i].RepoNameWithOwner] {
				seenRepos[q.Authored[i].RepoNameWithOwner] = true
				matches = append(matches, &q.Authored[i])
			}
		}
	}
	for i := range q.Inbox {
		if q.Inbox[i].Number == num {
			if !seenRepos[q.Inbox[i].RepoNameWithOwner] {
				seenRepos[q.Inbox[i].RepoNameWithOwner] = true
				matches = append(matches, &q.Inbox[i])
			}
		}
	}

	if len(matches) == 0 {
		return nil, ErrNotFound
	}
	if len(matches) > 1 {
		refs := make([]string, 0, len(matches))
		for _, m := range matches {
			refs = append(refs, m.Key().String())
		}
		return nil, fmt.Errorf("%w: matches %s", ErrAmbiguousRef, strings.Join(refs, ", "))
	}

	return matches[0], nil
}

// IsAuthored reports whether pr is in the authored queue or authored by the queue viewer.
func (q Queue) IsAuthored(pr PullRequest) bool {
	if pr.Author != "" && q.Viewer != "" && pr.Author == q.Viewer {
		return true
	}
	for _, auth := range q.Authored {
		if auth.Key() == pr.Key() {
			return true
		}
	}
	return false
}

// MergeQueue dedupes and merges pull requests from authored and inbox searches according to pronto rules:
// - Dedupe key is (RepoNameWithOwner, Number)
// - Authored takes precedence over Inbox
// - Inbox drafts are excluded
// - Authored drafts are preserved (pinned to bottom)
// - Assigned and ReviewRequested flags are merged
func MergeQueue(authored, inbox []PullRequest) Queue {
	var authoredReady []PullRequest
	var authoredDrafts []PullRequest
	authoredKeys := make(map[PRKey]bool)

	for _, pr := range authored {
		k := pr.Key()
		if authoredKeys[k] {
			continue
		}
		authoredKeys[k] = true
		if pr.IsDraft {
			authoredDrafts = append(authoredDrafts, pr)
		} else {
			authoredReady = append(authoredReady, pr)
		}
	}

	authoredReady = append(authoredReady, authoredDrafts...)

	inboxMap := make(map[PRKey]PullRequest)
	var inboxKeys []PRKey

	for _, pr := range inbox {
		if pr.IsDraft {
			continue // hard filter on inbox drafts
		}

		k := pr.Key()
		if authoredKeys[k] {
			continue // Authored takes precedence
		}

		if existing, exists := inboxMap[k]; exists {
			existing.Assigned = existing.Assigned || pr.Assigned
			inboxMap[k] = existing
		} else {
			inboxMap[k] = pr
			inboxKeys = append(inboxKeys, k)
		}
	}

	mergedInbox := make([]PullRequest, 0, len(inboxKeys))
	for _, k := range inboxKeys {
		mergedInbox = append(mergedInbox, inboxMap[k])
	}

	return Queue{
		Authored: authoredReady,
		Inbox:    mergedInbox,
	}
}
