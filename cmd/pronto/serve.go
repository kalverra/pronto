package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/profiling"
	"github.com/kalverra/pronto/internal/source"
)

// resolveServeSource returns the fetch source for the serve command: an
// injected source when present (tests), otherwise a direct GraphQL source.
// serve never proxies another daemon — a second serve must fail the bind.
func resolveServeSource(src source.Source, debug bool, logger zerolog.Logger) (source.Source, cache.Store, error) {
	if src != nil {
		return src, nil, nil
	}
	return defaultSource(debug, logger)
}

func newServeCmd(src source.Source) *cobra.Command {
	var interval string
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the background poller and socket API daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			debug, _ := cmd.Flags().GetBool("debug")
			cpuProfilePath, _ := cmd.Flags().GetString("cpu-profile")
			memProfilePath, _ := cmd.Flags().GetString("mem-profile")

			logger, closer := resolveLogger(debug)
			if closer != nil {
				defer func() { _ = closer.Close() }()
			}

			s, store, err := resolveServeSource(src, debug, logger)
			if err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			profSession, err := startProfiling(cmd, cfg, debug, cpuProfilePath, memProfilePath, logger)
			if err != nil {
				return err
			}
			if profSession != nil {
				defer profSession.Stop(logger)
			}

			pollInterval := daemon.DefaultInterval
			if cfg.Server.PollInterval != "" {
				parsed, err := time.ParseDuration(cfg.Server.PollInterval)
				if err != nil {
					return fmt.Errorf("parse server.poll_interval: %w", err)
				}
				pollInterval = parsed
			}
			if interval != "" {
				parsed, err := time.ParseDuration(interval)
				if err != nil {
					return fmt.Errorf("parse --interval: %w", err)
				}
				pollInterval = parsed
			}
			if pollInterval < daemon.MinInterval {
				return fmt.Errorf(
					"interval %s is below the %s minimum (GitHub API rate limits)",
					pollInterval,
					daemon.MinInterval,
				)
			}

			leakInterval := daemon.DefaultLeakCheckInterval
			if cfg.Server.LeakCheckInterval != "" {
				parsed, err := time.ParseDuration(cfg.Server.LeakCheckInterval)
				if err != nil {
					return fmt.Errorf("parse server.leak_check_interval: %w", err)
				}
				leakInterval = parsed
			}
			if debug && leakInterval == 0 {
				leakInterval = 1 * time.Minute
			}

			socketPath := resolveSocketPath(cmd)

			leakDumpDir := ""
			if base, err := cache.Dir(); err == nil {
				leakDumpDir = filepath.Join(base, "leaks")
			} else {
				logger.Warn().Err(err).Msg("resolving cache dir for leak dumps failed; dumps disabled")
			}

			d := daemon.New(daemon.Options{
				Source:            s,
				Store:             store,
				Checker:           notify.DefaultPRStatusChecker,
				Interval:          pollInterval,
				LeakCheckInterval: leakInterval,
				LeakChecker:       profiling.RuntimeLeakChecker{},
				LeakDumpDir:       leakDumpDir,
				SocketPath:        socketPath,
				Version:           version,
				Logger:            logger,
			})
			return d.Run(cmd.Context())
		},
	}
	serveCmd.Flags().StringVar(&interval, "interval", "", "Poll interval, e.g. 30s (overrides server.poll_interval)")
	return serveCmd
}
