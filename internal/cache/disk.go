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

// stampedFile is the on-disk envelope. Key identifies the document so a
// filename collision (e.g. "a/b" and "a_b" sanitize to the same name) reads as
// a miss instead of returning the wrong document.
type stampedFile struct {
	Schema  int             `json:"schema"`
	Key     string          `json:"key"`
	SavedAt time.Time       `json:"saved_at"`
	Data    json.RawMessage `json:"data"`
}

var errCacheMiss = errors.New("cache miss")

// read decodes the envelope at path without touching Data. Absent, corrupt,
// or schema-mismatched files return an error.
func (d *DiskStore) read(ctx context.Context, path string) (stampedFile, error) {
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
	return stamped, nil
}

// load reads the document at path stamped with key. Any miss (absent, corrupt,
// key mismatch, ctx done) returns the zero T and ok=false.
func load[T any](ctx context.Context, d *DiskStore, path, key string) (T, time.Time, bool) {
	stamped, err := d.read(ctx, path)
	if err != nil {
		var zero T
		return zero, time.Time{}, false
	}
	return decode[T](ctx, stamped, key)
}

// decode unmarshals stamped.Data once its key matches; mismatches and decode
// failures return the zero T so callers never see a wrong or partial value.
func decode[T any](ctx context.Context, stamped stampedFile, key string) (T, time.Time, bool) {
	var zero T
	if stamped.Key != key {
		return zero, time.Time{}, false
	}
	var v T
	if err := json.Unmarshal(stamped.Data, &v); err != nil {
		return zero, time.Time{}, false
	}
	if ctx.Err() != nil {
		return zero, time.Time{}, false
	}
	return v, stamped.SavedAt, true
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

// rootPath is the path of a singleton document (identity, queue, focus).
func (d *DiskStore) rootPath(name string) string {
	return filepath.Join(d.dir, name+".json")
}

func (d *DiskStore) prsDir() string {
	return filepath.Join(d.dir, "prs")
}

func (d *DiskStore) prPath(repo string, num int) string {
	safe := strings.NewReplacer("/", "_", "\\", "_").Replace(repo)
	return filepath.Join(d.prsDir(), fmt.Sprintf("%s_%d.json", safe, num))
}

func prDocKey(repo string, num int) string {
	return model.PRKey{Repo: repo, Number: num}.String()
}

// Identity returns the cached viewer identity, or ok=false when absent,
// corrupt, schema/key mismatched, saved without a login, or ctx is done.
func (d *DiskStore) Identity(ctx context.Context) (Identity, time.Time, bool) {
	// Identity is keyed by its own login, which is unknown until decoded.
	stamped, err := d.read(ctx, d.rootPath("identity"))
	if err != nil || stamped.Key == "" {
		return Identity{}, time.Time{}, false
	}
	id, savedAt, ok := decode[Identity](ctx, stamped, stamped.Key)
	if !ok || id.Login != stamped.Key {
		return Identity{}, time.Time{}, false
	}
	return id, savedAt, true
}

// SaveIdentity persists the viewer identity. An identity without a login is
// written but always reads back as a miss.
func (d *DiskStore) SaveIdentity(ctx context.Context, id Identity) error {
	return d.write(ctx, d.rootPath("identity"), id.Login, id)
}

// PR returns cached pull request for (repo, num), or ok=false when absent.
func (d *DiskStore) PR(ctx context.Context, repo string, num int) (model.PullRequest, time.Time, bool) {
	return load[model.PullRequest](ctx, d, d.prPath(repo, num), prDocKey(repo, num))
}

// SavePR persists full pull request for (repo, num).
func (d *DiskStore) SavePR(ctx context.Context, repo string, num int, pr model.PullRequest) error {
	return d.write(ctx, d.prPath(repo, num), prDocKey(repo, num), pr)
}

// TouchPR marks the PR cache file as recently used without advancing its
// savedAt, so PrunePRs keeps it. Touching an absent entry is a no-op.
func (d *DiskStore) TouchPR(ctx context.Context, repo string, num int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	if err := os.Chtimes(d.prPath(repo, num), now, now); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

const tempFilePruneAge = 1 * time.Hour

// PrunePRs removes PR cache files not saved or touched within olderThan, plus
// abandoned temp files.
func (d *DiskStore) PrunePRs(ctx context.Context, olderThan time.Duration) (int, error) {
	dir := d.prsDir()
	entries, err := os.ReadDir(dir)
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
		fullPath := filepath.Join(dir, name)

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

// Queue returns the cached queue snapshot, or ok=false when absent.
func (d *DiskStore) Queue(ctx context.Context) (model.Queue, time.Time, bool) {
	return load[model.Queue](ctx, d, d.rootPath("queue"), "queue")
}

// SaveQueue persists a queue snapshot.
func (d *DiskStore) SaveQueue(ctx context.Context, q model.Queue) error {
	return d.write(ctx, d.rootPath("queue"), "queue", q)
}

// Focus returns the cached focused PR keys, or ok=false when absent.
func (d *DiskStore) Focus(ctx context.Context) ([]model.PRKey, time.Time, bool) {
	return load[[]model.PRKey](ctx, d, d.rootPath("focus"), "focus")
}

// SaveFocus persists focused PR keys.
func (d *DiskStore) SaveFocus(ctx context.Context, keys []model.PRKey) error {
	return d.write(ctx, d.rootPath("focus"), "focus", keys)
}
