package daemon_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/source"
)

// pacedSource is a source.Pacer that always asks to be fetched again after
// wait, recording what each Fetch was told.
type pacedSource struct {
	wait  time.Duration
	queue model.Queue

	mu      sync.Mutex
	at      []time.Time
	forced  []bool
	boosted []map[model.PRKey]bool
}

func (s *pacedSource) Fetch(ctx context.Context) (model.Queue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at = append(s.at, time.Now())
	s.forced = append(s.forced, source.ForceRefreshFromContext(ctx))
	s.boosted = append(s.boosted, source.BoostedFromContext(ctx))
	return s.queue, nil
}

func (s *pacedSource) NextFetch() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.at[len(s.at)-1].Add(s.wait)
}

func (s *pacedSource) snapshot() ([]time.Time, []bool, []map[model.PRKey]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.at...),
		append([]bool(nil), s.forced...),
		append([]map[model.PRKey]bool(nil), s.boosted...)
}

// runPaced runs a daemon over src inside a synctest bubble until stop is
// called.
func runPaced(t *testing.T, opts daemon.Options) (*daemon.Daemon, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	d := daemon.New(opts)
	done := make(chan struct{})
	go func() {
		_ = d.Run(ctx)
		close(done)
	}()
	return d, func() {
		cancel()
		<-done
	}
}

// A source that paces itself sets the poll cadence: the fixed interval is
// ignored.
//
//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemon_PacerSetsPollCadence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &pacedSource{wait: 20 * time.Second}
		_, stop := runPaced(t, daemon.Options{Source: src, Interval: time.Hour})

		time.Sleep(65 * time.Second)
		synctest.Wait()
		stop()

		at, forced, _ := src.snapshot()
		require.Len(t, at, 4, "polls at 0s, 20s, 40s, 60s")
		for i := 1; i < len(at); i++ {
			assert.Equal(t, 20*time.Second, at[i].Sub(at[i-1]))
		}
		assert.NotContains(t, forced, true, "scheduled polls are not forced")
	})
}

// A deadline already in the past cannot spin the loop.
//
//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemon_PacerPastDeadlineIsFloored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &pacedSource{wait: -time.Minute}
		_, stop := runPaced(t, daemon.Options{Source: src, Interval: time.Hour})

		time.Sleep(3500 * time.Millisecond)
		synctest.Wait()
		stop()

		at, _, _ := src.snapshot()
		assert.Len(t, at, 4, "one poll per second at most")
	})
}

// A manual refresh (the TUI's r key) polls at once and marks the fetch
// forced, so a pacing source runs discovery immediately.
//
//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemon_RefreshForcesFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &pacedSource{wait: time.Hour}
		d, stop := runPaced(t, daemon.Options{Source: src, Interval: time.Hour})

		time.Sleep(time.Second)
		synctest.Wait()
		d.Refresh()
		time.Sleep(time.Second)
		synctest.Wait()
		stop()

		_, forced, _ := src.snapshot()
		assert.Equal(t, []bool{false, true}, forced)
	})
}

// PRs in the Focus or Priority tab reach the source as boosted keys on the
// next poll.
//
//nolint:paralleltest // synctest bubbles cannot run in parallel
func TestDaemon_PassesBoostedKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		direct := pr(1, "Asked of me", "org/repo")
		direct.Author = "alice"
		direct.DirectRequest = true
		other := pr(2, "Team request", "org/repo")
		other.Author = "bob"
		src := &pacedSource{
			wait:  20 * time.Second,
			queue: model.Queue{Viewer: "kalverra", Inbox: []model.PullRequest{direct, other}},
		}
		_, stop := runPaced(t, daemon.Options{
			Source:         src,
			Interval:       time.Hour,
			PriorityConfig: config.DefaultPriorityConfig(),
		})

		time.Sleep(25 * time.Second)
		synctest.Wait()
		stop()

		_, _, boosted := src.snapshot()
		require.Len(t, boosted, 2)
		assert.Empty(t, boosted[0], "nothing is classified before the first fetch")
		assert.Equal(t, map[model.PRKey]bool{direct.Key(): true}, boosted[1])
	})
}
