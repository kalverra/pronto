package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/logging"
	"github.com/kalverra/pronto/internal/profiling"
)

type debugSession struct {
	debug     bool
	logPath   string
	pprofURL  string
	pprofStop func() error
	cpuPath   string
	cpuFile   *os.File
	memPath   string
	leakDir   string
	errWriter io.Writer
}

func formatDebugInfo(ds *debugSession) string {
	var b strings.Builder
	b.WriteString("Pronto Debug Mode Enabled\n\n")

	b.WriteString("Logging:\n")
	fmt.Fprintf(&b, "  File:      %s\n", ds.logPath)
	b.WriteString("  Level:     DEBUG\n")
	fmt.Fprintf(&b, "  Tail logs: tail -f %s\n\n", ds.logPath)

	b.WriteString("Live pprof HTTP server:\n")
	fmt.Fprintf(&b, "  URL:       %s\n", ds.pprofURL)
	b.WriteString("  Endpoints:\n")
	fmt.Fprintf(&b, "    • %s/debug/pprof/goroutineleak (Go 1.27+ leak detection)\n", ds.pprofURL)
	fmt.Fprintf(&b, "    • %s/debug/pprof/heap\n", ds.pprofURL)
	fmt.Fprintf(&b, "    • %s/debug/pprof/profile (CPU)\n", ds.pprofURL)
	fmt.Fprintf(&b, "    • %s/debug/pprof/goroutine\n", ds.pprofURL)
	b.WriteString("  Analyze live:\n")
	fmt.Fprintf(&b, "    go tool pprof %s/debug/pprof/heap\n", ds.pprofURL)
	fmt.Fprintf(&b, "    go tool pprof %s/debug/pprof/profile\n\n", ds.pprofURL)

	b.WriteString("Profiles on exit:\n")
	fmt.Fprintf(&b, "  CPU:       %s\n", ds.cpuPath)
	fmt.Fprintf(&b, "  Heap:      %s\n", ds.memPath)
	b.WriteString("  Analyze dumps:\n")
	fmt.Fprintf(&b, "    go tool pprof %s\n", ds.cpuPath)
	fmt.Fprintf(&b, "    go tool pprof %s\n\n", ds.memPath)

	b.WriteString("Leak snapshots:\n")
	fmt.Fprintf(&b, "  Directory: %s\n", ds.leakDir)
	b.WriteString("  Analyze leaks:\n")
	fmt.Fprintf(&b, "    go tool pprof %s/goroutineleak-*.pprof\n", ds.leakDir)

	return b.String()
}

func startProfiling(
	cmd *cobra.Command,
	cfg config.Config,
	debug bool,
	cpuFlag, memFlag string,
	logger zerolog.Logger,
) (*debugSession, error) {
	cacheDir, err := cache.Dir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	_ = os.MkdirAll(cacheDir, 0o700)

	cpuPath := cpuFlag
	memPath := memFlag
	if debug {
		if cpuPath == "" {
			cpuPath = filepath.Join(cacheDir, "cpu.pprof")
		}
		if memPath == "" {
			memPath = filepath.Join(cacheDir, "mem.pprof")
		}
	}

	pprofAddr := cfg.Server.PProfAddr
	if pprofAddr == "" && debug {
		pprofAddr = "localhost:6060"
	}

	var pprofURL string
	var pprofStop func() error
	if pprofAddr != "" {
		ln, stop, err := profiling.StartHTTP(pprofAddr)
		if err != nil && debug && pprofAddr == "localhost:6060" {
			ln, stop, err = profiling.StartHTTP("localhost:0")
		}
		if err != nil {
			return nil, fmt.Errorf("start pprof: %w", err)
		}
		pprofURL = fmt.Sprintf("http://%s", ln.Addr())
		pprofStop = stop
	}

	var cpuFile *os.File
	if cpuPath != "" {
		//nolint:gosec // user flag or cache dir
		f, err := os.Create(cpuPath)
		if err != nil {
			if pprofStop != nil {
				_ = pprofStop()
			}
			return nil, fmt.Errorf("create cpu profile %q: %w", cpuPath, err)
		}
		if err := profiling.StartCPUProfile(f); err != nil {
			_ = f.Close()
			if pprofStop != nil {
				_ = pprofStop()
			}
			return nil, fmt.Errorf("start cpu profile: %w", err)
		}
		cpuFile = f
	}

	leakDir := filepath.Join(cacheDir, "leaks")
	session := &debugSession{
		debug:     debug,
		logPath:   logging.LogPath(),
		pprofURL:  pprofURL,
		pprofStop: pprofStop,
		cpuPath:   cpuPath,
		cpuFile:   cpuFile,
		memPath:   memPath,
		leakDir:   leakDir,
		errWriter: cmd.ErrOrStderr(),
	}

	if debug {
		logger.Debug().
			Str("pprof_url", pprofURL).
			Str("cpu_profile", cpuPath).
			Str("mem_profile", memPath).
			Msg("debug mode activated")
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), formatDebugInfo(session))
	} else if pprofURL != "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "pprof listening on %s\n", pprofURL)
	}

	return session, nil
}

func (s *debugSession) Stop(logger zerolog.Logger) {
	if s == nil {
		return
	}
	if s.cpuFile != nil {
		profiling.StopCPUProfile()
		_ = s.cpuFile.Close()
	}
	if s.memPath != "" {
		f, err := os.Create(s.memPath)
		if err != nil {
			logger.Warn().Err(err).Msg("creating mem profile failed")
		} else {
			if err := profiling.WriteHeapProfile(f); err != nil {
				logger.Warn().Err(err).Msg("writing mem profile failed")
			}
			_ = f.Close()
		}
	}
	if s.pprofStop != nil {
		_ = s.pprofStop()
	}
	if s.debug {
		_, _ = fmt.Fprintf(s.errWriter, "\nProfiles written on exit:\n  CPU:  %s\n  Heap: %s\n", s.cpuPath, s.memPath)
	}
}
