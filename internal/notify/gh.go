package notify

import (
	"context"
	"encoding/json"

	"github.com/cli/go-gh/v2"

	"github.com/kalverra/pronto/internal/model"
)

// DefaultPRStatusChecker reports the lifecycle state of a PR, via the gh CLI.
func DefaultPRStatusChecker(ctx context.Context, repo string, number int) (PRState, error) {
	target := model.PRKey{Repo: repo, Number: number}.String()
	stdout, _, err := gh.ExecContext(ctx, "pr", "view", target, "--json", "state")
	if err != nil {
		return "", err
	}
	var resp struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return "", err
	}
	return PRState(resp.State), nil
}
