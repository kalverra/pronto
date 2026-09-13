// Package main provides the entrypoint for the pronto CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"charm.land/fang/v2"
	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/kalverra/pronto"
	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/client"
	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/daemon"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/logging"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/profiling"
	"github.com/kalverra/pronto/internal/prview"
	"github.com/kalverra/pronto/internal/score"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/internal/source"
	"github.com/kalverra/pronto/internal/tui"
)

// ErrUnknownCommand is returned when an unsupported CLI command is requested.
var ErrUnknownCommand = errors.New("unknown command")

// ErrJSONRequired is returned when list is invoked without --json; use the
// bare `pronto` TUI for interactive output.
var ErrJSONRequired = errors.New("list requires --json (run bare `pronto` for the TUI)")

// ErrRefRequired is returned when why is invoked without a pull request reference.
var ErrRefRequired = errors.New("why requires a pull request reference (e.g. #123 or org/repo#123)")

// version is the daemon protocol version reported by ping and CLI version.
var version = "dev"

// ErrWaitTimeout is returned when wait exits before its --until condition
// was met. The CLI exits with code 2 in this case.
var (
	ErrWaitTimeout = errors.New("wait timed out before the requested event occurred")
	runTUI         = tui.Run
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

// NewRootCmd creates the root Cobra command configured with subcommands and I/O streams.
// Invoked with no arguments, it launches the interactive TUI.
func NewRootCmd(src source.Source, stdout, stderr io.Writer) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "pronto",
		Short:   "PR triage dashboard and review queue",
		Version: version,
	}
	rootCmd.PersistentFlags().String("socket", "", "Socket path (default: PRONTO_SOCKET_PATH or config dir)")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug mode (debug logging, HTTP pprof, exit profiling)")
	rootCmd.PersistentFlags().String("cpu-profile", "", "Write a CPU profile to this file on exit")
	rootCmd.PersistentFlags().String("mem-profile", "", "Write a heap profile to this file on exit")

	if stdout != nil {
		rootCmd.SetOut(stdout)
	}
	if stderr != nil {
		rootCmd.SetErr(stderr)
	}

	resolveSource := func(cmd *cobra.Command) (source.Source, cache.Store, error) {
		return resolveQueueSource(cmd.Context(), cmd, src)
	}

	// Bare `pronto` launches the TUI; unknown arguments are rejected.
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("%w: %s", ErrUnknownCommand, args[0])
		}
		debug, _ := cmd.Flags().GetBool("debug")
		cpuPath, _ := cmd.Flags().GetString("cpu-profile")
		memPath, _ := cmd.Flags().GetString("mem-profile")

		logger, closer := resolveLogger(debug)
		if closer != nil {
			defer func() { _ = closer.Close() }()
		}

		cfg, err := config.Load()
		if err != nil {
			logger.Warn().Err(err).Msg("loading config failed; using default terminal view")
			cfg = config.Config{PRView: config.ViewTerminal}
		}

		profSession, err := startProfiling(cmd, cfg, debug, cpuPath, memPath, logger)
		if err != nil {
			return err
		}
		if profSession != nil {
			defer profSession.Stop(logger)
		}

		tuiCtx, cancelTUI := context.WithCancel(cmd.Context())
		defer cancelTUI()

		s, store, cleanup, err := resolveTUISource(tuiCtx, cmd, src, cfg, logger)
		if err != nil {
			return err
		}
		if cleanup != nil {
			defer cleanup()
		}

		viewer, err := prview.ResolveViewer(cfg)
		if err != nil {
			return fmt.Errorf("resolve pr viewer: %w", err)
		}
		differ, err := prview.ResolveDiffViewer(cfg)
		if err != nil {
			return fmt.Errorf("resolve pr differ: %w", err)
		}
		opts := []tui.Option{
			tui.WithPRViewer(viewer),
			tui.WithPRDiffer(differ),
			tui.WithNotificationConfig(cfg.Notifications),
		}
		return runTUI(tuiCtx, s, store, opts...)
	}

	// list command
	var listJSON bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List pull requests in review queue",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !listJSON {
				return ErrJSONRequired
			}

			s, _, err := resolveSource(cmd)
			if err != nil {
				return err
			}

			fetchCtx, cancel := context.WithTimeout(cmd.Context(), source.ColdFetchTimeout)
			defer cancel()

			queue, err := s.Fetch(fetchCtx)
			if err != nil {
				return fmt.Errorf("fetch queue: %w", err)
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(queue); err != nil {
				return fmt.Errorf("encode queue json: %w", err)
			}
			return nil
		},
	}
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output full queue in JSON format")
	rootCmd.AddCommand(listCmd)

	// why command
	var whyJSON bool
	whyCmd := &cobra.Command{
		Use:   "why <ref>",
		Short: "Explain priority score calculation for a pull request",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
				return ErrRefRequired
			}
			ref := args[0]

			s, _, err := resolveSource(cmd)
			if err != nil {
				return err
			}

			fetchCtx, cancel := context.WithTimeout(cmd.Context(), source.ColdFetchTimeout)
			defer cancel()

			queue, err := s.Fetch(fetchCtx)
			if err != nil {
				return fmt.Errorf("fetch queue: %w", err)
			}

			pr, err := queue.Find(ref)
			if err != nil {
				return fmt.Errorf("find pull request %q: %w", ref, err)
			}

			isAuthored := queue.IsAuthored(*pr)

			var breakdown score.Breakdown
			if isAuthored {
				breakdown = score.ExplainMine(*pr, time.Now(), score.DefaultMineWeights())
			} else {
				breakdown = score.Explain(*pr, time.Now(), queue.Viewer, queue.Teams, nil, score.DefaultWeights())
			}

			if whyJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(breakdown); err != nil {
					return fmt.Errorf("encode breakdown json: %w", err)
				}
				return nil
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Score: %.1f\n", breakdown.Total); err != nil {
				return fmt.Errorf("write score: %w", err)
			}
			for _, term := range breakdown.Terms {
				if _, err := fmt.Fprintf(
					cmd.OutOrStdout(),
					"  %-18s raw=%-6.1f wt=%-6.1f contrib=%+.1f\n",
					term.Name,
					term.Raw,
					term.Weight,
					term.Contribution,
				); err != nil {
					return fmt.Errorf("write term: %w", err)
				}
			}
			return nil
		},
	}
	whyCmd.Flags().BoolVar(&whyJSON, "json", false, "Output breakdown in JSON format")
	rootCmd.AddCommand(whyCmd)

	rootCmd.AddCommand(newServeCmd(src))
	rootCmd.AddCommand(newAPICmd())
	rootCmd.AddCommand(newWatchCmd())
	rootCmd.AddCommand(newWaitCmd())
	rootCmd.AddCommand(newAgentCmd())

	return rootCmd
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

func resolveTUISource(
	ctx context.Context,
	cmd *cobra.Command,
	injected source.Source,
	cfg config.Config,
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
	if injected != nil {
		if ds, ok := injected.(interface{ IsDaemon() bool }); ok && ds.IsDaemon() {
			return injected, defaultStore(), nil, nil
		}
	}

	socketPath := resolveSocketPath(cmd)
	store := defaultStore()

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
		underlyingSrc, store, srcErr = defaultSource(debug)
		if srcErr != nil {
			return nil, nil, nil, srcErr
		}
	}

	pollInterval := daemon.DefaultInterval
	if cfg.Server.PollInterval != "" {
		if parsed, err := time.ParseDuration(cfg.Server.PollInterval); err == nil {
			pollInterval = parsed
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
		Source:            underlyingSrc,
		Store:             store,
		Checker:           notify.DefaultPRStatusChecker,
		Interval:          pollInterval,
		LeakCheckInterval: leakInterval,
		LeakChecker:       leakChecker,
		LeakDumpDir:       leakDumpDir,
		SocketPath:        socketPath,
		Version:           version,
		Logger:            logger,
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
) (source.Source, cache.Store, error) {
	if injected != nil {
		return injected, nil, nil
	}
	if c := tryDaemonClient(ctx, resolveSocketPath(cmd), 500*time.Millisecond); c != nil {
		return client.NewQueueSource(c), defaultStore(), nil
	}
	debug := false
	if cmd != nil {
		debug, _ = cmd.Flags().GetBool("debug")
	}
	return defaultSource(debug)
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

func newAPICmd() *cobra.Command {
	apiCmd := &cobra.Command{
		Use:   "api",
		Short: "Inspect the socket API protocol",
	}
	// The output is the embedded schema.json verbatim — the same artifact the
	// drift tests pin to server.Methods and events.ValidTypes — so the command
	// itself has no content that can drift from the protocol.
	schemaCmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the socket API JSON Schema",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), events.SchemaJSON())
			return err
		},
	}
	apiCmd.AddCommand(schemaCmd)
	return apiCmd
}

func newAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "Print instructions and protocol guide for AI coding agents",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), pronto.AgentSkill)
			return err
		},
	}
}

func newWatchCmd() *cobra.Command {
	var typesFilter string
	var jsonOut bool
	watchCmd := &cobra.Command{
		Use:   "watch [ref]",
		Short: "Stream PR events from a running pronto daemon",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			c, err := dialDaemon(resolveSocketPath(cmd))
			if err != nil {
				return err
			}
			defer c.Close()

			sub, err := buildSubscription(ctx, c, args, typesFilter)
			if err != nil {
				return err
			}

			ch, err := c.Subscribe(ctx, sub)
			if err != nil {
				return fmt.Errorf("subscribe: %w", err)
			}

			for ev := range ch {
				if jsonOut {
					if _, err := fmt.Fprintln(out, eventJSON(ev)); err != nil {
						return err
					}
					continue
				}
				if _, err := fmt.Fprintln(out, formatEventPretty(ev)); err != nil {
					return err
				}
			}
			return nil
		},
	}
	watchCmd.Flags().StringVar(&typesFilter, "types", "", "Comma-separated event types to watch (default: all)")
	watchCmd.Flags().BoolVar(&jsonOut, "json", false, "Emit events as JSONL")
	return watchCmd
}

func newWaitCmd() *cobra.Command {
	var until, timeout string
	waitCmd := &cobra.Command{
		Use:   "wait <ref>",
		Short: "Block until an event occurs for a pull request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			ref := args[0]

			untilTypes, err := parseUntil(until)
			if err != nil {
				return err
			}

			c, err := dialDaemon(resolveSocketPath(cmd))
			if err != nil {
				return err
			}
			defer c.Close()

			pr, err := c.GetPR(ctx, ref)
			if err != nil {
				return fmt.Errorf("resolve %q: %w", ref, err)
			}

			ch, err := c.Subscribe(ctx, events.Subscription{
				Types: untilTypes,
				Repo:  pr.RepoNameWithOwner,
				PR:    pr.Number,
			})
			if err != nil {
				return fmt.Errorf("subscribe: %w", err)
			}

			var timeoutCh <-chan time.Time
			if timeout != "" {
				d, err := time.ParseDuration(timeout)
				if err != nil {
					return fmt.Errorf("parse --timeout: %w", err)
				}
				if d > 0 {
					timer := time.NewTimer(d)
					defer timer.Stop()
					timeoutCh = timer.C
				}
			}

			select {
			case ev, ok := <-ch:
				if !ok {
					return errors.New("event stream closed unexpectedly")
				}
				_, err := fmt.Fprintln(out, formatEventPretty(ev))
				return err
			case <-timeoutCh:
				return ErrWaitTimeout
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	waitCmd.Flags().StringVar(&until, "until", "", "Event to wait for: an event type or alias (merged, ci_settled)")
	waitCmd.Flags().StringVar(&timeout, "timeout", "", "Give up after this duration (e.g. 30m)")
	_ = waitCmd.MarkFlagRequired("until")
	return waitCmd
}

// parseUntil resolves an --until value into the event types it matches.
func parseUntil(until string) ([]events.Type, error) {
	switch until {
	case "merged":
		return []events.Type{events.TypePRMerged}, nil
	case "ci_settled":
		return []events.Type{events.TypeCIPassed, events.TypeCIFailed}, nil
	case "":
		return nil, errors.New("--until is required (event type or alias: merged, ci_settled)")
	}
	if !events.ValidTypes[events.Type(until)] {
		return nil, fmt.Errorf("unknown --until value %q (event type or alias: merged, ci_settled)", until)
	}
	return []events.Type{events.Type(until)}, nil
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

// buildSubscription resolves an optional ref and --types filter into a
// subscription.
func buildSubscription(
	ctx context.Context,
	c *client.Client,
	args []string,
	typesFilter string,
) (events.Subscription, error) {
	sub := events.Subscription{}
	if typesFilter != "" {
		types, err := events.ParseTypes(typesFilter)
		if err != nil {
			return sub, err
		}
		sub.Types = types
	}
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		pr, err := c.GetPR(ctx, args[0])
		if err != nil {
			return sub, fmt.Errorf("resolve %q: %w", args[0], err)
		}
		sub.Repo = pr.RepoNameWithOwner
		sub.PR = pr.Number
	}
	return sub, nil
}

func eventJSON(ev events.Event) string {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Sprintf(`{"type":%q,"seq":%d,"marshal_error":%q}`, ev.Type, ev.Seq, err.Error())
	}
	return string(data)
}

func formatEventPretty(ev events.Event) string {
	ts := ev.TS.Format("15:04:05")
	if ev.Type == events.TypeQueueRefreshed {
		var payload events.QueueRefreshedPayload
		if raw, err := json.Marshal(ev.Payload); err == nil {
			_ = json.Unmarshal(raw, &payload)
		}
		return fmt.Sprintf(
			"%s queue_refreshed ok=%t authored=%d inbox=%d",
			ts,
			payload.OK,
			payload.Authored,
			payload.Inbox,
		)
	}
	return fmt.Sprintf("%s %s %s %q", ts, ev.Type, model.PRKey{Repo: ev.Repo, Number: ev.PR}, ev.Title)
}

func customErrorHandler(w io.Writer, styles fang.Styles, err error) {
	fang.DefaultErrorHandler(w, styles, err)
	_, _ = fmt.Fprintf(w, "View logs at: %s\n", logging.LogPath())
}

// Run executes the pronto CLI with given arguments, I/O streams, and queue source.
func Run(ctx context.Context, args []string, src source.Source, stdout, stderr io.Writer) error {
	cmd := NewRootCmd(src, stdout, stderr)
	if args != nil {
		cmd.SetArgs(args)
	} else {
		cmd.SetArgs([]string{})
	}
	return fang.Execute(ctx, cmd,
		fang.WithVersion(version),
		fang.WithErrorHandler(customErrorHandler),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	)
}

func main() {
	if err := Run(context.Background(), os.Args[1:], nil, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, ErrWaitTimeout) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
