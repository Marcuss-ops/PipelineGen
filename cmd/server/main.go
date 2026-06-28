// Package main is the canonical PipelineGen HTTP server binary.
//
// This binary is the in-tree default server entry point. It is the target
// of:
//   - `make build`  → `bin/pipelinegen`
//   - `Dockerfile` target `server-runtime` → `/usr/local/bin/pipelinegen`
//   - `docker-compose.yml` service `pipelinegen-server`
//
// The server bootstraps the composition root (DB, repos, services,
// in-process job runner — depending on --mode) via `app.WireServices`
// and exposes the HTTP API via `api.NewServer`.
//
// `--mode` semantics (matches startBackgroundJobs):
//
//	all         → HTTP + worker + scheduler + maintenance sweepers (default)
//	server      → HTTP only (used when an external cmd/worker handles jobs)
//	scheduler   → HTTP + scheduler + sweepers, no in-process job runner
//	maintenance → HTTP + sweepers, no in-process job runner
//	worker      → HTTP + in-process job runner, no scheduler
//
// Port: read from `cfg.Server.Port` (env VELOX_PORT). See
// `internal/platform/config` and AGENTS.md §"Port policy".
package main

import (
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/app"
	logging "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func main() {
	cfgPth := flag.String("config", "config.yaml", "Path to configuration file")
	mode := flag.String("mode", "all",
		"Server mode: all|server|scheduler|maintenance|worker")
	flag.Parse()

	// Fail-fast on invalid mode. startBackgroundJobs silently no-ops for
	// unknown mode strings (all three runWorker/runScheduler/runMaintenance
	// bools stay false) which would otherwise start a "server" that does
	// nothing. AGENTS.md §"Fail-fast" convention applied here.
	switch *mode {
	case "all", "server", "scheduler", "maintenance", "worker":
	default:
		fmt.Fprintln(os.Stderr, "FATAL: invalid --mode:", *mode,
			"(allowed: all|server|scheduler|maintenance|worker)")
		os.Exit(2)
	}

	// Fail-fast on malformed config (per audit P0 #5). Use config.GetFromPath
	// (NOT config.Get) so a typo in config.yaml is not silently falling back
	// to defaults.
	cfg, err := config.GetFromPath(*cfgPth)
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL: failed to load config:", err)
		os.Exit(2)
	}
	if cfg == nil {
		fmt.Fprintln(os.Stderr, "FATAL: nil config from config.GetFromPath")
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "FATAL: config validation failed:", err)
		os.Exit(2)
	}

	logging.Init(cfg.Logging.Level, cfg.Logging.Format)
	log := logging.Get().Named("server")
	defer func() { _ = logging.Sync() }()

	log.Info("server starting",
		zap.String("mode", *mode),
		zap.String("config", *cfgPth),
		zap.Int("port", cfg.Server.Port),
		zap.String("host", cfg.Server.Host),
	)

	// Build the full composition root, run wire-up, register all modules,
	// and freeze the job dispatcher. Lifecycle wraps startBackgroundJobs
	// (deferred job runner start) and buildCleanup (LIFO teardown).
	// The server manages background services via the lifecycle — no
	// separate defer.Cleanup() needed.
	deps, err := app.WireServices(cfg, log, *mode)
	if err != nil {
		log.Fatal("failed to wire services", zap.Error(err))
	}

	// Build the HTTP server with the module registry (all known routes),
	// the internal worker handler (the /internal/v1/* endpoints used
	// by the remote cmd/worker binary to claim jobs), the lifecycle
	// manager (background job runner + cleanup), and the health-service
	// (DB+Drive+Qdrant+Jobs checks wired from the composition root).
	//
	// QDRANT-route-constructor (June 2026, PR 3): outboxHandler and
	// mediasearchHandler are now passed INTO NewServerWithHealth so they
	// register on the /internal/v1 WorkerAuth group BEFORE Setup()
	// runs. The previous pattern (setting them via setters AFTER the
	// constructor returned) silently 404'd because Setup() had
	// already executed. The setters remain on Server for back-compat
	// but are NOT called from this file anymore.
	server := api.NewServerWithHealth(
		cfg,
		deps.Registry,
		deps.WorkerHandler,
		deps.InternalMediaHandler,
		deps.OutboxHandler,
		deps.MediasearchHandler,
		deps.Lifecycle,
		deps.HealthService,
		deps.ReadyChecker,
	)
	// QDRANT-005: plug the Qdrant probe into the lifecycle readiness
	// barrier so /ready actually checks Qdrant reachability. nil-safe.
	// QDRANT-005 v2: api.LifecycleManager now exposes AddProbe directly,
	// so the previous runtime type-assertion is unnecessary — a direct
	// call works for every production LifecycleManager (minimalLifecycle
	// wires a no-op AddProbe so WireMinimal callers stay type-safe).
	if deps.QdrantProbe != nil && deps.Lifecycle != nil {
		deps.Lifecycle.AddProbe("qdrant", deps.QdrantProbe.Probe)
	}
	if err := server.Start(); err != nil {
		log.Fatal("server failed", zap.Error(err))
	}

	log.Info("server exited cleanly")
}
