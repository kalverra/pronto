// Package cachetest provides an in-memory cache.Store fake for tests.
package cachetest

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/model"
)

// Store is a thread-safe, in-memory cache.Store with DiskStore's observable
// semantics: values round-trip through JSON (no aliasing), an identity without
// a login reads as a miss, TouchPR extends retention without advancing
// savedAt, and done contexts miss on read and fail on write.
type Store struct {
	mu   sync.Mutex
	now  func() time.Time
	docs map[string]doc
}

type doc struct {
	data    []byte
	savedAt time.Time
	usedAt  time.Time // savedAt or last TouchPR; drives PrunePRs
}

var _ cache.Store = (*Store)(nil)

// New returns an empty Store stamping entries with time.Now.
func New() *Store {
	return &Store{now: time.Now, docs: map[string]doc{}}
}

// SetClock replaces the clock used to stamp savedAt on later saves and
// touches, e.g. to seed a stale snapshot.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

const (
	identityDoc = "identity"
	queueDoc    = "queue"
	focusDoc    = "focus"
	prsPrefix   = "prs/"
)

func prDoc(repo string, num int) string {
	return prsPrefix + model.PRKey{Repo: repo, Number: num}.String()
}

func get[T any](ctx context.Context, s *Store, name string) (T, time.Time, bool) {
	var zero T
	if ctx.Err() != nil {
		return zero, time.Time{}, false
	}
	s.mu.Lock()
	d, ok := s.docs[name]
	s.mu.Unlock()
	if !ok {
		return zero, time.Time{}, false
	}
	var v T
	if err := json.Unmarshal(d.data, &v); err != nil {
		return zero, time.Time{}, false
	}
	return v, d.savedAt, true
}

func (s *Store) set(ctx context.Context, name string, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.docs[name] = doc{data: data, savedAt: now, usedAt: now}
	return nil
}

// Identity returns the saved identity; one saved without a login misses.
func (s *Store) Identity(ctx context.Context) (cache.Identity, time.Time, bool) {
	id, savedAt, ok := get[cache.Identity](ctx, s, identityDoc)
	if !ok || id.Login == "" {
		return cache.Identity{}, time.Time{}, false
	}
	return id, savedAt, true
}

// SaveIdentity stores id.
func (s *Store) SaveIdentity(ctx context.Context, id cache.Identity) error {
	return s.set(ctx, identityDoc, id)
}

// PR returns the saved pull request for (repo, num).
func (s *Store) PR(ctx context.Context, repo string, num int) (model.PullRequest, time.Time, bool) {
	return get[model.PullRequest](ctx, s, prDoc(repo, num))
}

// SavePR stores pr for (repo, num).
func (s *Store) SavePR(ctx context.Context, repo string, num int, pr model.PullRequest) error {
	return s.set(ctx, prDoc(repo, num), pr)
}

// TouchPR extends retention of (repo, num) without advancing savedAt. Absent
// entries are a no-op.
func (s *Store) TouchPR(ctx context.Context, repo string, num int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := prDoc(repo, num)
	if d, ok := s.docs[name]; ok {
		d.usedAt = s.now()
		s.docs[name] = d
	}
	return nil
}

// PrunePRs removes PRs not saved or touched within olderThan.
func (s *Store) PrunePRs(ctx context.Context, olderThan time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	removed := 0
	for name, d := range s.docs {
		if strings.HasPrefix(name, prsPrefix) && now.Sub(d.usedAt) > olderThan {
			delete(s.docs, name)
			removed++
		}
	}
	return removed, nil
}

// Queue returns the saved queue snapshot.
func (s *Store) Queue(ctx context.Context) (model.Queue, time.Time, bool) {
	return get[model.Queue](ctx, s, queueDoc)
}

// SaveQueue stores q.
func (s *Store) SaveQueue(ctx context.Context, q model.Queue) error {
	return s.set(ctx, queueDoc, q)
}

// Focus returns the saved focus keys.
func (s *Store) Focus(ctx context.Context) ([]model.PRKey, time.Time, bool) {
	return get[[]model.PRKey](ctx, s, focusDoc)
}

// SaveFocus stores keys.
func (s *Store) SaveFocus(ctx context.Context, keys []model.PRKey) error {
	return s.set(ctx, focusDoc, keys)
}
