package notify

import (
	"context"
	"encoding/json"

	"github.com/cli/go-gh/v2"

	"github.com/kalverra/pronto/internal/model"
)

// DefaultPRStatusChecker reports whether a PR is merged, via the gh CLI.
func DefaultPRStatusChecker(ctx context.Context, repo string, number int) (bool, error) {
	target := model.PRKey{Repo: repo, Number: number}.String()
	stdout, _, err := gh.ExecContext(ctx, "pr", "view", target, "--json", "state")
	if err != nil {
		return false, err
	}
	var resp struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return false, err
	}
	return resp.State == "MERGED", nil
}
