// Package daemon runs the pronto background poll loop and serves the socket
// API from a single fetcher.
package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/profiling"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/internal/source"
)

// DefaultInterval is the background refresh cadence when Options.Interval is
// unset. A source.Pacer source paces itself instead; for one, Interval only
// bounds each fetch.
const DefaultInterval = source.DefaultDiscoveryInterval

// minPacedWait floors the wait a Pacer asks for, so a deadline already in
// the past cannot spin the loop.
const minPacedWait = time.Second

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

	NotificationConfig config.NotificationConfig
	FocusConfig        config.RuleSet
	PriorityConfig     config.PriorityConfig
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
	boosted      map[model.PRKey]bool // focused/priority PRs, from the last classify
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
	detectorOpts := []notify.DetectorOption{
		notify.WithBotFilter(true),
		notify.WithPolicy(opts.NotificationConfig.Policy()),
		notify.WithPriority(opts.PriorityConfig),
		notify.WithFocus(opts.FocusConfig),
	}
	if opts.Checker != nil {
		detectorOpts = append(detectorOpts, notify.WithStatusChecker(opts.Checker))
	}
	if len(opts.NotificationConfig.Images) > 0 || len(opts.NotificationConfig.Sounds) > 0 {
		assets := notify.Assets{}
		if len(opts.NotificationConfig.Images) > 0 {
			assets.Images = make(map[notify.Trigger]string, len(opts.NotificationConfig.Images))
			for k, v := range opts.NotificationConfig.Images {
				assets.Images[notify.Trigger(k)] = config.ExpandPath(v)
			}
		}
		if len(opts.NotificationConfig.Sounds) > 0 {
			assets.Sounds = make(map[notify.Trigger]string, len(opts.NotificationConfig.Sounds))
			for k, v := range opts.NotificationConfig.Sounds {
				assets.Sounds[notify.Trigger(k)] = v
			}
		}
		detectorOpts = append(detectorOpts, notify.WithAssets(assets))
	}

	return &Daemon{
		opts:     opts,
		bus:      opts.Bus,
		detector: notify.NewDetector(detectorOpts...),
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

	timer := time.NewTimer(d.nextWait())
	defer timer.Stop()

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
		case <-timer.C:
			d.refresh(ctx)
			timer.Reset(d.nextWait())
		case <-leakC:
			d.checkLeaks()
		case <-d.kickCh:
			d.refresh(source.WithForceRefresh(ctx))
			timer.Reset(d.nextWait())
		}
	}
}

// nextWait is how long to sleep before the next poll: until the source's
// NextFetch when it paces itself, otherwise the fixed interval.
func (d *Daemon) nextWait() time.Duration {
	if p, ok := d.opts.Source.(source.Pacer); ok {
		return max(time.Until(p.NextFetch()), minPacedWait)
	}
	return d.opts.Interval
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
		// Boost from the warm snapshot so the first fetch already refreshes
		// Focus/Priority PRs a tier sooner, instead of waiting a poll.
		boosted := d.detector.Seed(q, notify.WithFocusOverride(d.focusOverride(ctx)))
		d.mu.Lock()
		d.boosted = boosted
		d.mu.Unlock()
	}
}

// focusOverride loads manual focus decisions from the cache store.
func (d *Daemon) focusOverride(ctx context.Context) notify.FocusOverride {
	if d.opts.Store == nil {
		return nil
	}
	if keys, _, ok := d.opts.Store.Focus(ctx); ok {
		cached := make(map[model.PRKey]bool, len(keys))
		for _, k := range keys {
			cached[k] = true
		}
		return func(k model.PRKey) (bool, bool) { return true, cached[k] }
	}
	return nil
}

func (d *Daemon) refresh(ctx context.Context) {
	d.mu.Lock()
	d.refreshing = true
	isCold := !d.hadWarmCache && d.fetchedAt.IsZero()
	boosted := d.boosted
	d.mu.Unlock()

	timeout := d.opts.Interval
	if isCold {
		timeout = source.ColdFetchTimeout
	}

	fetchCtx, cancel := context.WithTimeout(source.WithBoosted(ctx, boosted), timeout)
	progressCtx := source.WithProgress(fetchCtx, func(loaded, total int) {
		d.bus.Emit(events.Event{
			Type:    events.TypeFetchProgress,
			Payload: events.FetchProgressPayload{Loaded: loaded, Total: total},
		})
	})
	q, err := d.opts.Source.Fetch(progressCtx)
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

	override := notify.WithFocusOverride(d.focusOverride(ctx))
	var nextBoosted map[model.PRKey]bool
	if !hadBaseline {
		// First observed state is the baseline: seed the detector and
		// emit no per-PR events.
		nextBoosted = d.detector.Seed(q, override)
	} else {
		delta := d.detector.Detect(ctx, prevQueue, q, override)
		for _, ev := range delta.Events {
			d.bus.Emit(ev)
		}
		nextBoosted = delta.Boosted
	}
	d.mu.Lock()
	d.boosted = nextBoosted
	d.mu.Unlock()

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
