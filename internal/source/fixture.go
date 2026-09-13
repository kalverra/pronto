package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// FixtureSource loads pull requests from a captured JSON fixture file.
type FixtureSource struct {
	Path               string
	StaleActivityAfter time.Duration
}

// FixtureOption configures a FixtureSource.
type FixtureOption func(*FixtureSource)

// WithFixtureStaleActivityAfter overrides the inactivity duration after which a PR is marked stale.
func WithFixtureStaleActivityAfter(d time.Duration) FixtureOption {
	return func(f *FixtureSource) {
		f.StaleActivityAfter = d
	}
}

// NewFixtureSource creates a new FixtureSource with the given file path.
func NewFixtureSource(path string, opts ...FixtureOption) *FixtureSource {
	f := &FixtureSource{Path: path}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

type fixtureRoot struct {
	Viewer          fixtureViewer `json:"viewer"`
	Authored        []rawPR       `json:"authored"`
	ReviewRequested []rawPR       `json:"reviewRequested"`
}

type fixtureViewer struct {
	Login string   `json:"login"`
	Teams []string `json:"teams"`
}

// Fetch satisfies the Source interface.
func (f *FixtureSource) Fetch(ctx context.Context) (model.Queue, error) {
	_ = ctx

	data, err := os.ReadFile(f.Path)
	if err != nil {
		return model.Queue{}, fmt.Errorf("read fixture file: %w", err)
	}

	var root fixtureRoot
	if err := json.Unmarshal(data, &root); err != nil {
		return model.Queue{}, fmt.Errorf("unmarshal fixture json: %w", err)
	}

	opts := convertOpts{staleActivityAfter: f.StaleActivityAfter}
	authored := make([]model.PullRequest, 0, len(root.Authored))
	for _, raw := range root.Authored {
		authored = append(authored, convertPR(
			raw.rawIdentity,
			stableFromWire(raw.rawStable),
			raw.rawFresh,
			false,
			opts,
		))
	}

	inbox := make([]model.PullRequest, 0, len(root.ReviewRequested))
	for _, raw := range root.ReviewRequested {
		inbox = append(inbox, convertPR(
			raw.rawIdentity,
			stableFromWire(raw.rawStable),
			raw.rawFresh,
			false,
			opts,
		))
	}

	queue := model.MergeQueue(authored, inbox)
	queue.Viewer = root.Viewer.Login
	queue.Teams = root.Viewer.Teams

	if progress := ProgressFromContext(ctx); progress != nil {
		total := len(authored) + len(inbox)
		progress(total, total)
	}

	return queue, nil
}
