package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/kalverra/pronto/internal/model"
)

// prStatesBatchSize bounds the aliases in one PullRequestStates request.
// Each alias is a single object lookup (no connections), so a full batch
// still costs one point.
const prStatesBatchSize = 50

// PRStates reports the lifecycle state (OPEN, CLOSED, or MERGED) of each PR
// in keys, one aliased request per prStatesBatchSize keys. PRs GitHub no
// longer returns (deleted, or access lost) are absent from the result. It
// shares the source's retrying client and primary rate limit backoff, so
// lookups never run while the budget is exhausted.
func (s *GraphQLSource) PRStates(ctx context.Context, keys []model.PRKey) (map[model.PRKey]string, error) {
	if until, limited := s.rateLimitedUntil(); limited {
		return nil, fmt.Errorf("%w: waiting until %s", ErrPrimaryRateLimit, until.UTC().Format(time.RFC3339))
	}
	states := make(map[model.PRKey]string, len(keys))
	for start := 0; start < len(keys); start += prStatesBatchSize {
		batch := keys[start:min(start+prStatesBatchSize, len(keys))]
		if err := s.prStatesBatch(ctx, batch, states); err != nil {
			return states, err
		}
	}
	return states, nil
}

func (s *GraphQLSource) prStatesBatch(ctx context.Context, keys []model.PRKey, into map[model.PRKey]string) error {
	vars := make(map[string]any, 3*len(keys))
	for i, k := range keys {
		owner, name, _ := strings.Cut(k.Repo, "/")
		vars[fmt.Sprintf("o%d", i)] = owner
		vars[fmt.Sprintf("n%d", i)] = name
		vars[fmt.Sprintf("p%d", i)] = k.Number
	}

	var resp map[string]json.RawMessage
	err := s.client.DoWithContext(ctx, prStatesQuery(len(keys)), vars, &resp)
	var gqlErr *api.GraphQLError
	switch {
	case err == nil:
	case errors.As(err, &gqlErr):
		// NOT_FOUND for some aliases: the rest decoded normally.
		s.logger.Debug().Err(err).Msg("some PR state lookups failed")
	case isPrimaryRateLimitErr(err):
		s.notePrimaryRateLimit()
		return fmt.Errorf("%w: %w", ErrPrimaryRateLimit, err)
	default:
		return fmt.Errorf("look up PR states: %w", err)
	}

	if raw, ok := resp["rateLimit"]; ok {
		var rl rawRateLimit
		if json.Unmarshal(raw, &rl) == nil {
			s.observeRateLimit(rl)
		}
	}
	for i, k := range keys {
		var repo *struct {
			PullRequest *struct {
				State string `json:"state"`
			} `json:"pullRequest"`
		}
		if err := json.Unmarshal(
			resp[fmt.Sprintf("r%d", i)],
			&repo,
		); err != nil || repo == nil ||
			repo.PullRequest == nil {
			continue
		}
		into[k] = repo.PullRequest.State
	}
	return nil
}

// prStatesQuery builds a PullRequestStates query with n aliased lookups
// r0..r(n-1), each taking variables $oN (owner), $nN (name), $pN (number).
func prStatesQuery(n int) string {
	var b strings.Builder
	b.WriteString("query PullRequestStates(")
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "$o%d: String!, $n%d: String!, $p%d: Int!", i, i, i)
	}
	b.WriteString(") {\n  rateLimit {\n    cost\n    limit\n    remaining\n    resetAt\n  }\n")
	for i := range n {
		fmt.Fprintf(&b,
			"  r%d: repository(owner: $o%d, name: $n%d) {\n    pullRequest(number: $p%d) {\n      state\n    }\n  }\n",
			i, i, i, i)
	}
	b.WriteString("}\n")
	return b.String()
}
