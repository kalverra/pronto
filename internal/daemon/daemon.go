// Package daemon runs the pronto background poll loop and serves the socket
// API from a single fetcher.
package daemon

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/profiling"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/internal/source"
)

// DefaultInterval is the background refresh cadence when Options.Interval is
// unset.
const DefaultInterval = 60 * time.Second

// MinInterval is the lowest poll interval the serve command accepts. Below
// roughly 10s a poller exhausts GitHub's 5,000 points/hour GraphQL budget.
const MinInterval = 10 * time.Second

// DefaultLeakCheckInterval is the goroutine leak check cadence when
// Options.LeakCheckInterval is unset. Zero disables leak checks entirely.
const DefaultLeakCheckInterval = time.Hour

// leakDumpRetention bounds how long goroutine leak profile dumps are kept.
const leakDumpRetention = 7 * 24 * time.Hour

// LeakChecker detects and dumps goroutine leaks.
type LeakChecker interface {
	// Leaked reports the number of leaked goroutines.
	Leaked() (int, error)
	// Dump writes the goroutine leak profile to path in pprof format.
	Dump(path string) error
}

// Options configures a Daemon.
type Options struct {
	Source     source.Source
	Store      cache.Store
	Bus        *events.Bus
	Checker    notify.PRStatusChecker
	Interval   time.Duration
	SocketPath string // optional; when empty, daemon runs without socket server
	Version    string
	Logger     zerolog.Logger

	// LeakCheckInterval is the cadence between goroutine leak checks.
	// Zero (the default) disables periodic checks.
	LeakCheckInterval time.Duration
	// LeakChecker detects and dumps leaks. Defaults to the Go 1.27 runtime
	// goroutine leak profiler.
	LeakChecker LeakChecker
	// LeakDumpDir receives goroutine leak profile dumps; empty disables dumps.
	LeakDumpDir string
}

// Daemon owns the poll loop, change detection, and event bus, and serves the
// socket API. It implements server.State.
type Daemon struct {
	opts     Options
	bus      *events.Bus
	detector *notify.Detector
	kickCh   chan struct{}
	ready    chan struct{}

	mu           sync.Mutex
	queue        model.Queue
	fetchedAt    time.Time
	refreshing   bool
	lastErr      string
	seeded       bool
	hadWarmCache bool
}

// New creates a Daemon. Call Run to start polling and serving.
func New(opts Options) *Daemon {
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Bus == nil {
		opts.Bus = events.NewBus()
	}
	if opts.LeakChecker == nil {
		opts.LeakChecker = profiling.RuntimeLeakChecker{}
	}
	// A zero zerolog.Logger discards all output, so no default is needed.
	return &Daemon{
		opts:     opts,
		bus:      opts.Bus,
		detector: notify.NewDetector(opts.Checker, notify.WithBotFilter(true)),
		kickCh:   make(chan struct{}, 1),
		ready:    make(chan struct{}),
	}
}

// Bus returns the event bus owned by the daemon.
func (d *Daemon) Bus() *events.Bus {
	return d.bus
}

// Ready returns a channel that is closed when the daemon is ready (socket bound or no socket).
func (d *Daemon) Ready() <-chan struct{} {
	return d.ready
}

// Run warm-starts from the cache, optionally serves the socket API (if SocketPath
// is non-empty), and polls until ctx is canceled. It blocks.
func (d *Daemon) Run(ctx context.Context) error {
	d.warmStart(ctx)

	var srv *server.Server
	var listenErr chan error
	if d.opts.SocketPath != "" {
		srvReady := make(chan struct{})
		srv = server.New(d.bus, d, server.Options{
			SocketPath: d.opts.SocketPath,
			Version:    d.opts.Version,
			Ready:      srvReady,
		})
		listenErr = make(chan error, 1)
		go func() { listenErr <- srv.Listen(ctx) }()
		select {
		case <-srvReady:
			close(d.ready)
		case err := <-listenErr:
			return err
		case <-ctx.Done():
			return nil
		}
	} else {
		close(d.ready)
	}
	defer func() {
		if srv != nil {
			srv.Close()
		}
		d.bus.Close()
	}()

	d.refresh(ctx)

	ticker := time.NewTicker(d.opts.Interval)
	defer ticker.Stop()

	// A nil leakC (when leak checks are disabled) blocks forever, which is
	// exactly the desired behavior in the select below.
	var leakC <-chan time.Time
	if d.opts.LeakCheckInterval > 0 {
		leakTicker := time.NewTicker(d.opts.LeakCheckInterval)
		defer leakTicker.Stop()
		leakC = leakTicker.C
	}
	for {
		select {
		case <-ctx.Done():
			d.saveFinalSnapshot(ctx)
			return nil
		case err := <-listenErr:
			d.saveFinalSnapshot(ctx)
			if ctx.Err() != nil || err == nil {
				return nil
			}
			return err
		case <-ticker.C:
			d.refresh(ctx)
		case <-leakC:
			d.checkLeaks()
		case <-d.kickCh:
			ticker.Reset(d.opts.Interval)
			d.refresh(ctx)
		}
	}
}

func (d *Daemon) saveFinalSnapshot(ctx context.Context) {
	if d.opts.Store == nil {
		return
	}
	d.mu.Lock()
	q := d.queue
	d.mu.Unlock()
	if err := d.opts.Store.SaveQueue(context.WithoutCancel(ctx), q); err != nil {
		d.opts.Logger.Warn().Err(err).Msg("saving final queue snapshot failed")
	}
}

// checkLeaks runs one goroutine leak check: on a nonzero count it warns and,
// when a dump dir is configured, writes a profile there and prunes old dumps.
func (d *Daemon) checkLeaks() {
	n, err := d.opts.LeakChecker.Leaked()
	if err != nil {
		d.opts.Logger.Warn().Err(err).Msg("goroutine leak check failed")
		return
	}
	if n == 0 {
		return
	}
	evt := d.opts.Logger.Warn().Int("count", n)
	if d.opts.LeakDumpDir != "" {
		path, dumpErr := d.dumpLeaks()
		switch {
		case dumpErr != nil:
			d.opts.Logger.Warn().Err(dumpErr).Msg("writing goroutine leak profile failed")
		default:
			evt = evt.Str("profile", path)
			d.pruneLeakDumps()
		}
	}
	evt.Msg("goroutine leak detected")
}

// dumpLeaks writes one goroutine leak profile into LeakDumpDir and returns its
// path.
func (d *Daemon) dumpLeaks() (string, error) {
	if err := os.MkdirAll(d.opts.LeakDumpDir, 0o700); err != nil {
		return "", fmt.Errorf("create leak dump dir: %w", err)
	}
	path := filepath.Join(d.opts.LeakDumpDir,
		fmt.Sprintf("goroutineleak-%s.pprof", time.Now().UTC().Format("20060102-150405")))
	if err := d.opts.LeakChecker.Dump(path); err != nil {
		return "", err
	}
	return path, nil
}

// pruneLeakDumps removes goroutine leak dumps older than leakDumpRetention.
// Only files matching the goroutineleak-*.pprof naming pattern are touched.
func (d *Daemon) pruneLeakDumps() {
	entries, err := os.ReadDir(d.opts.LeakDumpDir)
	if err != nil {
		d.opts.Logger.Warn().Err(err).Msg("reading leak dump dir failed")
		return
	}
	cutoff := time.Now().Add(-leakDumpRetention)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "goroutineleak-") || !strings.HasSuffix(name, ".pprof") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(d.opts.LeakDumpDir, name)); err != nil {
			d.opts.Logger.Warn().Err(err).Str("file", name).Msg("pruning old leak dump failed")
		}
	}
}

// Snapshot returns the current queue state for the queue.snapshot method.
func (d *Daemon) Snapshot() server.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked()
}

func (d *Daemon) snapshotLocked() server.Snapshot {
	var age float64
	if !d.fetchedAt.IsZero() {
		age = time.Since(d.fetchedAt).Seconds()
	}
	var dropped uint64
	if d.bus != nil {
		dropped = d.bus.Dropped()
	}
	return server.Snapshot{
		Queue:         d.queue,
		FetchedAt:     d.fetchedAt,
		AgeSeconds:    age,
		Refreshing:    d.refreshing,
		LastError:     d.lastErr,
		DroppedEvents: dropped,
	}
}

// FindPR resolves a PR reference against the current queue.
func (d *Daemon) FindPR(ref string) (*model.PullRequest, error) {
	d.mu.Lock()
	q := d.queue
	d.mu.Unlock()
	return q.Find(ref)
}

// Refresh triggers an immediate queue poll.
func (d *Daemon) Refresh() server.Snapshot {
	d.mu.Lock()
	d.refreshing = true
	snap := d.snapshotLocked()
	d.mu.Unlock()
	select {
	case d.kickCh <- struct{}{}:
	default:
	}
	return snap
}

// warmStart loads the cached queue snapshot, if any, so the socket serves
// useful state before the first fetch completes.
func (d *Daemon) warmStart(ctx context.Context) {
	if d.opts.Store == nil {
		return
	}
	q, savedAt, ok := d.opts.Store.Queue(ctx)
	if !ok {
		return
	}

	interval := d.opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	maxAge := 2 * interval
	fresh := time.Since(savedAt) <= maxAge

	d.mu.Lock()
	d.queue = q
	d.fetchedAt = savedAt
	d.hadWarmCache = true
	if fresh {
		d.seeded = true
	}
	d.mu.Unlock()

	if fresh {
		d.detector.Seed(q.Authored)
	}
}

func (d *Daemon) refresh(ctx context.Context) {
	d.mu.Lock()
	d.refreshing = true
	isCold := !d.hadWarmCache && d.fetchedAt.IsZero()
	d.mu.Unlock()

	timeout := d.opts.Interval
	if isCold {
		timeout = source.ColdFetchTimeout
	}

	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	q, err := d.opts.Source.Fetch(fetchCtx)
	cancel()

	d.mu.Lock()
	d.refreshing = false
	if err != nil {
		d.lastErr = err.Error()
		prev := d.queue
		d.mu.Unlock()
		d.opts.Logger.Error().Err(err).Msg("queue fetch failed")
		d.bus.Emit(events.Event{
			Type: events.TypeQueueRefreshed,
			Payload: events.QueueRefreshedPayload{
				OK:       false,
				Error:    err.Error(),
				Authored: len(prev.Authored),
				Inbox:    len(prev.Inbox),
			},
		})
		return
	}
	prevQueue := d.queue
	hadBaseline := d.seeded
	d.queue = q
	d.fetchedAt = time.Now().UTC()
	d.lastErr = ""
	d.seeded = true
	d.mu.Unlock()

	if !hadBaseline {
		// First observed state is the baseline: seed the detector and
		// emit no per-PR events.
		d.detector.Seed(q.Authored)
	} else {
		merged := make(map[model.PRKey]bool)
		if notes, derr := d.detector.DetectMineChanges(ctx, prevQueue.Authored, q.Authored); derr != nil {
			d.opts.Logger.Error().Err(derr).Msg("change detection failed")
		} else {
			for _, n := range notes {
				if n.Trigger == notify.TriggerPRMerged {
					merged[model.PRKey{Repo: n.Repo, Number: n.PRNumber}] = true
				}
				d.bus.Emit(notificationEvent(n))
			}
		}
		for _, ev := range DiffEvents(prevQueue, q, merged) {
			d.bus.Emit(ev)
		}
	}

	if d.opts.Store != nil {
		if err := d.opts.Store.SaveQueue(ctx, q); err != nil {
			d.opts.Logger.Warn().Err(err).Msg("saving queue snapshot failed")
		}
	}

	d.bus.Emit(events.Event{
		Type: events.TypeQueueRefreshed,
		Payload: events.QueueRefreshedPayload{
			OK:       true,
			Authored: len(q.Authored),
			Inbox:    len(q.Inbox),
		},
	})
}

// notificationEvent converts a detector notification into an event.
func notificationEvent(n notify.Notification) events.Event {
	ev := events.Event{
		Type:  events.Type(string(n.Trigger)),
		Repo:  n.Repo,
		PR:    n.PRNumber,
		Title: n.PRTitle,
	}
	if n.Trigger == notify.TriggerReviewReceived {
		ev.Payload = events.ReviewPayload{
			Author:      n.Author,
			State:       n.ReviewState,
			SubmittedAt: n.SubmittedAt,
		}
	}
	return ev
}

// DiffEvents reports PRs entering or leaving the queue between two fetches,
// covering both authored and inbox lists. It omits pr_removed for PRs that were merged.
// Output is sorted deterministically by (Repo, PR, Type).
func DiffEvents(prev, curr model.Queue, merged map[model.PRKey]bool) []events.Event {
	prevAll := make(map[model.PRKey]model.PullRequest, len(prev.Authored)+len(prev.Inbox))
	for _, pr := range prev.Authored {
		prevAll[pr.Key()] = pr
	}
	for _, pr := range prev.Inbox {
		prevAll[pr.Key()] = pr
	}
	currAll := make(map[model.PRKey]model.PullRequest, len(curr.Authored)+len(curr.Inbox))
	for _, pr := range curr.Authored {
		currAll[pr.Key()] = pr
	}
	for _, pr := range curr.Inbox {
		currAll[pr.Key()] = pr
	}

	var out []events.Event
	for key, pr := range currAll {
		if _, ok := prevAll[key]; !ok {
			out = append(out, events.Event{
				Type:  events.TypePRAdded,
				Repo:  pr.RepoNameWithOwner,
				PR:    pr.Number,
				Title: pr.Title,
			})
		}
	}
	for key, pr := range prevAll {
		if _, ok := currAll[key]; !ok {
			if merged != nil && merged[key] {
				continue
			}
			out = append(out, events.Event{
				Type:  events.TypePRRemoved,
				Repo:  pr.RepoNameWithOwner,
				PR:    pr.Number,
				Title: pr.Title,
			})
		}
	}

	slices.SortFunc(out, func(a, b events.Event) int {
		if c := strings.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		if c := cmp.Compare(a.PR, b.PR); c != 0 {
			return c
		}
		return strings.Compare(string(a.Type), string(b.Type))
	})
	return out
}
