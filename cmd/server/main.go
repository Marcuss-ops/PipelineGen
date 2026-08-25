// Package main is the canonical PipelineGen HTTP server binary.
//
// P2-1 (June 2026): this file is a pure transport shell. The
// composition-root work (WireServices + NewServerWithHealth +
// qdrantProbe wiring) lives in
// `internal/app/build_server.go::BuildServer`; the runtime driver
// lives next to it as `(*ServerRuntime).Run`. The slim main only
// owns the three surfaces BuildServer does NOT touch (per its
// contract): flag parsing, fail-fast mode + config validation, and
// logging init/sync.
//
// Sequence from boot to clean exit:
//
//	flag.Parse            → parse --config + --mode
//	mode validation       → fail-fast on non-allowed --mode
//	config load           → fail-fast on malformed/typo'd config
//	logging.Init + defer  → caller-side resource life-cycle
//	BuildServer(cfg, mode, log) → composition root + HTTP server
//	signal.NotifyContext  → caller-side signal ownership (mirrors worker pattern)
//	Run(sigCtx)           → drives HTTP serve loop + graceful shutdown
//	defer logging.Sync    → flushes buffered logs after Run returns
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	logging "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"
)

// `--mode` semantics (matches `app.startBackgroundJobs`):
//
//	all         → HTTP + worker + scheduler + maintenance sweepers (default)
//	server      → HTTP only (used when an external cmd/worker handles jobs)
//	scheduler   → HTTP + scheduler + sweepers, no in-process job runner
//	maintenance → HTTP + sweepers, no in-process job runner
//	worker      → HTTP + in-process job runner, no scheduler
//
// Port: read from `cfg.Server.Port` (env VELOX_PORT). See
// `internal/platform/config` and AGENTS.md §"Port policy".
const modeUsage = "Server mode: all|server|scheduler|maintenance|worker"

func main() {
	cfgPth := flag.String("config", "config.yaml", "Path to configuration file")
	mode := flag.String("mode", "all", modeUsage)
	flag.Parse()

	// Fail-fast on invalid mode. startBackgroundJobs silently no-ops
	// for unknown mode strings (all three runWorker/runScheduler/runMaintenance
	// bools stay false) which would otherwise start a "server" that
	// does nothing. AGENTS.md §"Fail-fast" convention applied here.
	switch *mode {
	case "all", "server", "scheduler", "maintenance", "worker":
	default:
		fmt.Fprintln(os.Stderr, "FATAL: invalid --mode:", *mode,
			"(allowed: all|server|scheduler|maintenance|worker)")
		os.Exit(2)
	}

	// Fail-fast on malformed config (per audit P0 #5). Use
	// config.GetFromPath (NOT config.Get) so a typo in config.yaml
	// is not silently falling back to defaults.
	resolved, err := config.GetResolvedFromPath(*cfgPth)
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL: failed to load config:", err)
		os.Exit(2)
	}
	if resolved == nil {
		fmt.Fprintln(os.Stderr, "FATAL: nil resolved config")
		os.Exit(2)
	}
	cfg := resolved.View()

	logging.Init(cfg.Logging.Level, cfg.Logging.Format)
	log := logging.Get().Named("server")
	defer func() { _ = logging.Sync() }()

	log.Info("server starting",
		zap.String("mode", *mode),
		zap.String("config", *cfgPth),
		zap.Int("port", cfg.Server.Port),
		zap.String("host", cfg.Server.Host),
	)

	runtime, err := wiring.BuildServer(cfg, *mode, log)
	if err != nil {
		log.Fatal("failed to build server", zap.Error(err))
	}

	// Signal-aware context — main owns OS signals (SIGINT + SIGTERM),
	// hands the resulting ctx into Run. Mirrors workerruntime/signals
	// flow on the worker side (P1-3) and the pre-P2-1 server behaviour
	// for graceful shutdown.
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runtime.Run(sigCtx); err != nil {
		log.Fatal("server failed", zap.Error(err))
	}
	log.Info("server exited cleanly")
}
