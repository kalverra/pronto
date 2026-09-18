package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kalverra/pronto/internal/model"
)

// DiskStore is a file-based Store. Writes are atomic (temp file + rename) so
// concurrent readers never observe partial files.
type DiskStore struct {
	dir string
}

var _ Store = (*DiskStore)(nil)

// NewDiskStore creates a DiskStore rooted at dir.
func NewDiskStore(dir string) *DiskStore {
	return &DiskStore{dir: dir}
}

// Dir returns the store's root directory.
func (d *DiskStore) Dir() string {
	return d.dir
}

type stampedFile struct {
	Schema  int             `json:"schema"`
	Key     string          `json:"key"`
	SavedAt time.Time       `json:"saved_at"`
	Data    json.RawMessage `json:"data"`
}

var errCacheMiss = errors.New("cache miss")

func (d *DiskStore) read(ctx context.Context, path string, dest any) (stampedFile, error) {
	if err := ctx.Err(); err != nil {
		return stampedFile{}, err
	}
	// #nosec G304 — path is constructed internally from the store root.
	raw, err := os.ReadFile(path)
	if err != nil {
		return stampedFile{}, err
	}
	var stamped stampedFile
	if err := json.Unmarshal(raw, &stamped); err != nil {
		return stampedFile{}, err
	}
	if stamped.Schema != SchemaVersion {
		return stampedFile{}, errCacheMiss
	}
	if err := json.Unmarshal(stamped.Data, dest); err != nil {
		return stampedFile{}, err
	}
	return stamped, nil
}

func (d *DiskStore) write(ctx context.Context, path, key string, data any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	stamped := stampedFile{
		Schema:  SchemaVersion,
		Key:     key,
		SavedAt: time.Now(),
		Data:    payload,
	}
	encoded, err := json.Marshal(stamped)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// Identity returns the cached viewer identity, or ok=false when absent, stale
// files are corrupt, schema/key mismatches, or ctx is done.
func (d *DiskStore) Identity(ctx context.Context) (Identity, time.Time, bool) {
	var id Identity
	stamped, err := d.read(ctx, filepath.Join(d.dir, "identity.json"), &id)
	if err != nil {
		return Identity{}, time.Time{}, false
	}
	if stamped.Key == "" || stamped.Key != id.Login {
		return Identity{}, time.Time{}, false
	}
	if ctx.Err() != nil {
		return Identity{}, time.Time{}, false
	}
	return id, stamped.SavedAt, true
}

// SaveIdentity persists the viewer identity.
func (d *DiskStore) SaveIdentity(ctx context.Context, id Identity) error {
	return d.write(ctx, filepath.Join(d.dir, "identity.json"), id.Login, id)
}

// PR returns cached pull request for (repo, num), or ok=false when absent.
func (d *DiskStore) PR(ctx context.Context, repo string, num int) (model.PullRequest, time.Time, bool) {
	var pr model.PullRequest
	stamped, err := d.read(ctx, d.prPath(repo, num), &pr)
	if err != nil {
		return model.PullRequest{}, time.Time{}, false
	}
	expectedKey := model.PRKey{Repo: repo, Number: num}.String()
	if stamped.Key != expectedKey {
		return model.PullRequest{}, time.Time{}, false
	}
	return pr, stamped.SavedAt, true
}

// SavePR persists full pull request for (repo, num).
func (d *DiskStore) SavePR(ctx context.Context, repo string, num int, pr model.PullRequest) error {
	expectedKey := model.PRKey{Repo: repo, Number: num}.String()
	return d.write(ctx, d.prPath(repo, num), expectedKey, pr)
}

// TouchPR updates the access and modification times of the PR cache file.
func (d *DiskStore) TouchPR(ctx context.Context, repo string, num int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := d.prPath(repo, num)
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

const tempFilePruneAge = 1 * time.Hour

// PrunePRs removes stale PR cache files and temp files.
func (d *DiskStore) PrunePRs(ctx context.Context, olderThan time.Duration) (int, error) {
	prsDir := filepath.Join(d.dir, "prs")
	entries, err := os.ReadDir(prsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	removed := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		fullPath := filepath.Join(prsDir, name)

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if strings.HasPrefix(name, ".tmp-") {
			if time.Since(info.ModTime()) > tempFilePruneAge {
				_ = os.Remove(fullPath)
			}
			continue
		}

		if !strings.HasSuffix(name, ".json") {
			continue
		}

		if time.Since(info.ModTime()) > olderThan {
			if err := os.Remove(fullPath); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

func (d *DiskStore) prPath(repo string, num int) string {
	safe := strings.NewReplacer("/", "_", "\\", "_").Replace(repo)
	name := fmt.Sprintf("%s_%d.json", safe, num)
	return filepath.Join(d.dir, "prs", name)
}

// Queue returns the cached queue snapshot, or ok=false when absent.
func (d *DiskStore) Queue(ctx context.Context) (model.Queue, time.Time, bool) {
	var q model.Queue
	stamped, err := d.read(ctx, filepath.Join(d.dir, "queue.json"), &q)
	if err != nil {
		return model.Queue{}, time.Time{}, false
	}
	if stamped.Key != "queue" {
		return model.Queue{}, time.Time{}, false
	}
	if ctx.Err() != nil {
		return model.Queue{}, time.Time{}, false
	}
	return q, stamped.SavedAt, true
}

// SaveQueue persists a queue snapshot.
func (d *DiskStore) SaveQueue(ctx context.Context, q model.Queue) error {
	return d.write(ctx, filepath.Join(d.dir, "queue.json"), "queue", q)
}

// Focus returns the cached focused PR keys, or ok=false when absent.
func (d *DiskStore) Focus(ctx context.Context) ([]model.PRKey, time.Time, bool) {
	var keys []model.PRKey
	stamped, err := d.read(ctx, filepath.Join(d.dir, "focus.json"), &keys)
	if err != nil {
		return nil, time.Time{}, false
	}
	if stamped.Key != "focus" {
		return nil, time.Time{}, false
	}
	if ctx.Err() != nil {
		return nil, time.Time{}, false
	}
	return keys, stamped.SavedAt, true
}

// SaveFocus persists focused PR keys.
func (d *DiskStore) SaveFocus(ctx context.Context, keys []model.PRKey) error {
	return d.write(ctx, filepath.Join(d.dir, "focus.json"), "focus", keys)
}
