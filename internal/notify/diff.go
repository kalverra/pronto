package notify

import (
	"cmp"
	"slices"
	"strings"

	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
)

// diffEvents reports PRs entering or leaving the queue between two fetches,
// covering both authored and inbox lists. It omits pr_removed for PRs that were merged.
// Output is sorted deterministically by (Repo, PR, Type).
func diffEvents(prev, curr model.Queue, merged map[model.PRKey]bool) []events.Event {
	prevAll := make(map[model.PRKey]model.PullRequest, len(prev.Authored)+len(prev.Inbox))
	for _, pr := range prev.Authored {
		prevAll[pr.Key()] = pr
	}
	for _, pr := range prev.Inbox {
		prevAll[pr.Key()] = pr
	}
	currAll := make(map[model.PRKey]model.PullRequest, len(curr.Authored)+len(curr.Inbox))
	for _, pr := range curr.Authored {
		currAll[pr.Key()] = pr
	}
	for _, pr := range curr.Inbox {
		currAll[pr.Key()] = pr
	}

	var out []events.Event
	for key, pr := range currAll {
		if _, ok := prevAll[key]; !ok {
			out = append(out, events.Event{
				Type:  events.TypePRAdded,
				Repo:  pr.RepoNameWithOwner,
				PR:    pr.Number,
				Title: pr.Title,
			})
		}
	}
	for key, pr := range prevAll {
		if _, ok := currAll[key]; !ok {
			if merged != nil && merged[key] {
				continue
			}
			out = append(out, events.Event{
				Type:  events.TypePRRemoved,
				Repo:  pr.RepoNameWithOwner,
				PR:    pr.Number,
				Title: pr.Title,
			})
		}
	}

	slices.SortFunc(out, func(a, b events.Event) int {
		if c := strings.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		if c := cmp.Compare(a.PR, b.PR); c != 0 {
			return c
		}
		return strings.Compare(string(a.Type), string(b.Type))
	})
	return out
}
