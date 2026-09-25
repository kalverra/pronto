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

func defaultSource(debug bool, loggers ...zerolog.Logger) (source.Source, cache.Store, error) {
	client, err := api.NewGraphQLClient(api.ClientOptions{Timeout: 30 * time.Second})
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
	retrying := source.NewRetryingGraphQLClient(client)
	return source.NewGraphQLSource(retrying, opts...), store, nil
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
	var underlyingSrc source.Source
	if injected != nil {
		underlyingSrc = injected
	} else {
		var srcErr error
		var defaultStoreRef cache.Store
		underlyingSrc, defaultStoreRef, srcErr = defaultSource(debug)
		if srcErr != nil {
			return nil, nil, nil, srcErr
		}
		if injectedStore == nil {
			store = defaultStoreRef
		}
	}

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
		Checker:            notify.DefaultPRStatusChecker,
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
	src, _, err := defaultSource(debug)
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
