// Package main provides the entrypoint for the pronto CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/logging"
	"github.com/kalverra/pronto/internal/prview"
	"github.com/kalverra/pronto/internal/source"
	"github.com/kalverra/pronto/internal/tui"
)

// ErrUnknownCommand is returned when an unsupported CLI command is requested.
var ErrUnknownCommand = errors.New("unknown command")

// version is the daemon protocol version reported by ping and CLI version.
var version = "dev"

var runTUI = tui.Run

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

	// Bare `pronto` launches the TUI; unknown arguments are rejected.
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("%w: %s", ErrUnknownCommand, args[0])
		}
		return runRootTUI(cmd, src)
	}

	rootCmd.AddCommand(newListCmd(src))
	rootCmd.AddCommand(newWhyCmd(src))
	rootCmd.AddCommand(newServeCmd(src))
	rootCmd.AddCommand(newAPICmd())
	rootCmd.AddCommand(newWatchCmd())
	rootCmd.AddCommand(newWaitCmd())
	rootCmd.AddCommand(newAgentCmd())

	return rootCmd
}

func runRootTUI(cmd *cobra.Command, src source.Source) error {
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
