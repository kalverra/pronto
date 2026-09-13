package prview

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/shlex"

	"github.com/kalverra/pronto/internal/model"
)

// ExpandTemplate expands PR placeholders in a custom command template string.
// Supported placeholders:
// - {url}
// - {number}
// - {owner}
// - {repo}
// - {repo_with_owner}
// - {head_ref}
// - {base_ref}
// - {title}
func ExpandTemplate(tmpl string, pr model.PullRequest) (string, error) {
	if strings.TrimSpace(tmpl) == "" {
		return "", errors.New("custom pr_view_command is empty")
	}

	owner := pr.RepoOwner
	repo := pr.RepoName
	repoWithOwner := pr.RepoNameWithOwner
	if repoWithOwner == "" && owner != "" && repo != "" {
		repoWithOwner = owner + "/" + repo
	} else if repoWithOwner != "" && (owner == "" || repo == "") {
		parts := strings.SplitN(repoWithOwner, "/", 2)
		if len(parts) == 2 {
			if owner == "" {
				owner = parts[0]
			}
			if repo == "" {
				repo = parts[1]
			}
		}
	}

	numStr := ""
	if pr.Number > 0 {
		numStr = strconv.Itoa(pr.Number)
	}

	r := strings.NewReplacer(
		"{url}", pr.URL,
		"{number}", numStr,
		"{owner}", owner,
		"{repo}", repo,
		"{repo_with_owner}", repoWithOwner,
		"{head_ref}", pr.HeadRefName,
		"{base_ref}", pr.BaseRefName,
		"{title}", pr.Title,
	)

	return r.Replace(tmpl), nil
}

// BuildCustomArgs expands a template and splits it into shell arguments.
func BuildCustomArgs(tmpl string, pr model.PullRequest) ([]string, error) {
	expanded, err := ExpandTemplate(tmpl, pr)
	if err != nil {
		return nil, err
	}

	args, err := shlex.Split(expanded)
	if err != nil {
		return nil, fmt.Errorf("parse custom command: %w", err)
	}
	if len(args) == 0 {
		return nil, errors.New("custom pr_view_command produced empty command")
	}

	return args, nil
}
