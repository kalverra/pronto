// Package update checks GitHub for a pronto release newer than the running binary.
package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"golang.org/x/mod/semver"
)

// Repo is the GitHub repository releases are checked against.
const Repo = "kalverra/pronto"

// checkTimeout bounds the one-shot release lookup so a slow or unreachable
// GitHub never delays TUI startup.
const checkTimeout = 5 * time.Second

// RESTClient is the subset of *api.RESTClient used to query GitHub releases.
type RESTClient interface {
	DoWithContext(ctx context.Context, method, path string, body io.Reader, response any) error
}

// Info describes the outcome of a release check.
type Info struct {
	// Available reports whether Latest is newer than the version passed to Check.
	Available bool
	// Latest is the newest release's tag name (e.g. "v0.2.0").
	Latest string
	// URL links to the release on GitHub.
	URL string
}

type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// NewClient builds a REST client using go-gh's standard auth resolution
// (GH_TOKEN, gh CLI config, or unauthenticated for public repos).
func NewClient() (*api.RESTClient, error) {
	return api.NewRESTClient(api.ClientOptions{Timeout: checkTimeout})
}

// Check queries the latest release of Repo and reports whether it is newer
// than current. current may be a semver string with or without a leading
// "v"; any non-semver value (e.g. a "dev" build) skips the network call
// entirely and returns a zero Info with no error.
func Check(ctx context.Context, client RESTClient, current string) (Info, error) {
	curV := ensureV(current)
	if !semver.IsValid(curV) {
		return Info{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	var rel release
	if err := client.DoWithContext(ctx, http.MethodGet, "repos/"+Repo+"/releases/latest", nil, &rel); err != nil {
		return Info{}, fmt.Errorf("fetch latest %s release: %w", Repo, err)
	}

	latestV := ensureV(rel.TagName)
	if !semver.IsValid(latestV) {
		return Info{}, nil
	}

	return Info{
		Available: semver.Compare(latestV, curV) > 0,
		Latest:    rel.TagName,
		URL:       rel.HTMLURL,
	}, nil
}

// ensureV prefixes v with "v" when missing, as required by golang.org/x/mod/semver.
func ensureV(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
