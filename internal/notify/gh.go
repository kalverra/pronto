package notify

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/cli/go-gh/v2"
)

// DefaultPRStatusChecker reports the lifecycle state of a PR, via the gh CLI.
func DefaultPRStatusChecker(ctx context.Context, repo string, number int) (PRState, error) {
	numStr := strconv.Itoa(number)
	var args []string
	if repo != "" {
		args = []string{"pr", "view", numStr, "-R", repo, "--json", "state"}
	} else {
		args = []string{"pr", "view", numStr, "--json", "state"}
	}
	stdout, _, err := gh.ExecContext(ctx, args...)
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
