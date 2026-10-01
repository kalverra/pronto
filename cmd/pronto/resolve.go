package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/client"
	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/logging"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/profiling"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/internal/source"
)

func resolveLogger(debug bool) (zerolog.Logger, io.Closer) {
	level := zerolog.InfoLevel
	if debug {
		level = zerolog.DebugLevel
	}
	l, closer, _ := logging.New(level)
	return l, closer
}

func defaultLogger() (zerolog.Logger, io.Closer) {
	return resolveLogger(false)
}

func defaultStore() cache.Store {
	store, err := cache.Open()
	if err != nil {
		return nil
	}
	return store
}

// defaultSource builds the GitHub GraphQL source; extra options (pacing)
// apply after the defaults.
func defaultSource(
	debug bool,
	extra []source.GraphQLSourceOption,
	loggers ...zerolog.Logger,
) (source.Source, cache.Store, error) {
	client, err := api.NewGraphQLClient(api.ClientOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("create default graphql client: %w", err)
	}
	var logger zerolog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	} else {
		logger, _ = resolveLogger(debug)
	}

	opts := []source.GraphQLSourceOption{source.WithLogger(logger)}
	store := defaultStore()
	if store == nil {
		logger.Warn().Msg("opening cache failed; fetching without cache")
	} else {
		opts = append(opts, source.WithCache(store))
	}
	opts = append(opts, extra...)
	retrying := source.NewRetryingGraphQLClient(client)
	return source.NewGraphQLSource(retrying, opts...), store, nil
}

// pacingOptions configures how a polling source paces itself: poll is the
// base discovery interval (already parsed and floored by the caller); the hot
// and idle intervals come from config (validated by config.Validate), floored
// at daemon.MinInterval and poll respectively.
func pacingOptions(server config.ServerConfig, poll time.Duration) []source.GraphQLSourceOption {
	parse := func(v string, def time.Duration) time.Duration {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		return def
	}
	hot := max(parse(server.HotInterval, source.DefaultHotInterval), daemon.MinInterval)
	idle := max(parse(server.IdleInterval, source.DefaultIdleInterval), poll)
	return []source.GraphQLSourceOption{
		source.WithDiscoveryInterval(poll),
		source.WithHotInterval(hot),
		source.WithIdleInterval(idle),
	}
}

// stateChecker adapts a source's batched PR state lookup for the change
// detector; sources without one (fixtures, tests) get no checker.
func stateChecker(src source.Source) notify.PRStatusChecker {
	lookup, ok := src.(interface {
		PRStates(ctx context.Context, keys []model.PRKey) (map[model.PRKey]string, error)
	})
	if !ok {
		return nil
	}
	return func(ctx context.Context, keys []model.PRKey) (map[model.PRKey]notify.PRState, error) {
		states, err := lookup.PRStates(ctx, keys)
		if err != nil {
			return nil, err
		}
		out := make(map[model.PRKey]notify.PRState, len(states))
		for k, st := range states {
			out[k] = notify.PRState(st)
		}
		return out, nil
	}
}

func isAddressInUse(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, server.ErrAlreadyListening) ||
		errors.Is(err, syscall.EADDRINUSE) ||
		strings.Contains(err.Error(), "already listening") ||
		strings.Contains(err.Error(), "address already in use")
}

// tryDaemonClient dials the socket and returns a ping-verified client, or nil
// when no daemon answers within timeout.
func tryDaemonClient(ctx context.Context, socketPath string, timeout time.Duration) *client.Client {
	c, err := dialDaemon(socketPath)
	if err != nil {
		return nil
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	_, pingErr := c.Ping(pingCtx)
	cancel()
	if pingErr != nil {
		c.Close()
		return nil
	}
	return c
}

// clampPollInterval lifts sub-floor poll intervals to daemon.MinInterval for
// the embedded TUI daemon, where a fatal error would block the whole app.
func clampPollInterval(d time.Duration) (time.Duration, bool) {
	if d >= daemon.MinInterval {
		return d, false
	}
	return daemon.MinInterval, true
}

func resolveTUISource(
	ctx context.Context,
	cmd *cobra.Command,
	injected source.Source,
	cfg config.Config,
	injectedStore cache.Store,
	loggers ...zerolog.Logger,
) (source.Source, cache.Store, func(), error) {
	debug := false
	if cmd != nil {
		debug, _ = cmd.Flags().GetBool("debug")
	}
	var logger zerolog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	} else {
		logger, _ = resolveLogger(debug)
	}
	store := injectedStore
	if store == nil {
		store = defaultStore()
	}
	if injected != nil {
		if ds, ok := injected.(interface{ IsDaemon() bool }); ok && ds.IsDaemon() {
			return injected, store, nil, nil
		}
	}

	socketPath := resolveSocketPath(cmd)

	// 1. Check if an external daemon is already running and answers ping.
	if c := tryDaemonClient(ctx, socketPath, 500*time.Millisecond); c != nil {
		return client.NewQueueSource(c), store, func() { c.Close() }, nil
	}

	// 2. No daemon is running: start an embedded daemon in-process for ctx.
	pollInterval := daemon.DefaultInterval
	if cfg.Server.PollInterval != "" {
		if parsed, err := time.ParseDuration(cfg.Server.PollInterval); err == nil {
			pollInterval = parsed
		}
	}
	if clamped, didClamp := clampPollInterval(pollInterval); didClamp {
		logger.Warn().
			Dur("requested", pollInterval).
			Dur("clamped_to", clamped).
			Msg("poll interval below minimum; clamping (GitHub API rate limits)")
		pollInterval = clamped
	}

	var underlyingSrc source.Source
	if injected != nil {
		underlyingSrc = injected
	} else {
		var srcErr error
		var defaultStoreRef cache.Store
		underlyingSrc, defaultStoreRef, srcErr = defaultSource(debug, pacingOptions(cfg.Server, pollInterval))
		if srcErr != nil {
			return nil, nil, nil, srcErr
		}
		if injectedStore == nil {
			store = defaultStoreRef
		}
	}

	leakDumpDir := ""
	if base, err := cache.Dir(); err == nil {
		leakDumpDir = filepath.Join(base, "leaks")
	}
	leakInterval := time.Duration(0)
	var leakChecker daemon.LeakChecker
	if debug {
		leakChecker = profiling.RuntimeLeakChecker{}
		leakInterval = 1 * time.Minute
		if cfg.Server.LeakCheckInterval != "" {
			if parsed, err := time.ParseDuration(cfg.Server.LeakCheckInterval); err == nil {
				leakInterval = parsed
			}
		}
	}

	daemonOpts := daemon.Options{
		Source:             underlyingSrc,
		Store:              store,
		Checker:            stateChecker(underlyingSrc),
		Interval:           pollInterval,
		LeakCheckInterval:  leakInterval,
		LeakChecker:        leakChecker,
		LeakDumpDir:        leakDumpDir,
		SocketPath:         socketPath,
		Version:            version,
		Logger:             logger,
		NotificationConfig: cfg.Notifications,
		FocusConfig:        cfg.Focus,
		PriorityConfig:     cfg.Priority,
	}

	d := daemon.New(daemonOpts)

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- d.Run(ctx)
	}()

	select {
	case <-d.Ready():
		return daemon.NewSource(d), store, nil, nil
	case err := <-runErrCh:
		if isAddressInUse(err) {
			// Lost the bind race: another server bound the socket. Fall back to client mode.
			if c := tryDaemonClient(ctx, socketPath, 1*time.Second); c != nil {
				return client.NewQueueSource(c), store, func() { c.Close() }, nil
			}
		} else if err != nil {
			logger.Warn().Err(err).Msg("starting embedded daemon with socket failed; continuing without socket server")
		}
		// If client fallback failed or socket unusable, run embedded without socket server.
		daemonOpts.SocketPath = ""
		dNoSock := daemon.New(daemonOpts)
		go func() { _ = dNoSock.Run(ctx) }()
		select {
		case <-dNoSock.Ready():
		case <-ctx.Done():
		}
		return daemon.NewSource(dNoSock), store, nil, nil
	}
}

func resolveQueueSource(
	ctx context.Context,
	cmd *cobra.Command,
	injected source.Source,
) (source.Source, error) {
	if injected != nil {
		return injected, nil
	}
	if c := tryDaemonClient(ctx, resolveSocketPath(cmd), 500*time.Millisecond); c != nil {
		return client.NewQueueSource(c), nil
	}
	debug := false
	if cmd != nil {
		debug, _ = cmd.Flags().GetBool("debug")
	}
	src, _, err := defaultSource(debug, nil)
	return src, err
}

func resolveSocketPath(cmd *cobra.Command) string {
	if cmd != nil {
		// Flags() only contains persistent flags after Execute merges them,
		// so direct callers (tests) need the explicit PersistentFlags lookup.
		if s, err := cmd.Flags().GetString("socket"); err == nil && s != "" {
			return s
		}
		if s, err := cmd.PersistentFlags().GetString("socket"); err == nil && s != "" {
			return s
		}
	}
	return client.SocketPath()
}

func dialDaemon(socketPath string) (*client.Client, error) {
	if socketPath == "" {
		socketPath = client.SocketPath()
	}
	c, err := client.Dial(socketPath)
	if err != nil {
		return nil, err
	}
	return c, nil
}
