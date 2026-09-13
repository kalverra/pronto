// Package cache provides on-disk caching of GitHub fetch artifacts:
// viewer identity, head-OID-stable pull request fields, and queue snapshots.
package cache

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// Identity is the viewer login and their org-qualified teams ("org/slug").
type Identity struct {
	Login string   `json:"login"`
	Teams []string `json:"teams"`
}

// SchemaVersion is the current cache serialization version. Mismatches invalidate cache entries.
const SchemaVersion = 1

// Store persists fetch artifacts across invocations. All reads are
// miss-tolerant: corrupt or absent data returns ok=false, never an error.
type Store interface {
	Identity(ctx context.Context) (id Identity, savedAt time.Time, ok bool)
	SaveIdentity(ctx context.Context, id Identity) error
	PR(ctx context.Context, repo string, num int) (pr model.PullRequest, savedAt time.Time, ok bool)
	SavePR(ctx context.Context, repo string, num int, pr model.PullRequest) error
	TouchPR(ctx context.Context, repo string, num int) error
	PrunePRs(ctx context.Context, olderThan time.Duration) (removed int, err error)
	Queue(ctx context.Context) (q model.Queue, savedAt time.Time, ok bool)
	SaveQueue(ctx context.Context, q model.Queue) error
}

// Dir returns the default cache directory: PRONTO_CACHE_DIR when set,
// otherwise os.UserCacheDir()/pronto.
func Dir() (string, error) {
	if dir := os.Getenv("PRONTO_CACHE_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "pronto"), nil
}

// Open returns the default disk store. Location is PRONTO_CACHE_DIR when set,
// otherwise os.UserCacheDir()/pronto.
func Open() (*DiskStore, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return NewDiskStore(dir), nil
}
