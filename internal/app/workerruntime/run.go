// Package workerruntime — the worker binary's runtime, split out of
// cmd/worker/main.go per P1-3 (June 2026).
//
// File layout (1-orientation-per-file convention, AGENTS.md Pattern 5):
//
//   run.go          — this file; Run() orchestrator + log-fatals
//   config.go       — LoadConfig (cfgPath -> *config.Config, error)
//   identity.go     — WorkerIdentity tuple (id/name/version/hostname)
//   capabilities.go — ParseAndValidateCaps (env-raw JSON -> WorkerCapabilities)
//   preflight.go    — master URL resolve + /health pre-flight loop
//   heartbeat.go    — HeartbeatLoop (background broker.Heartbeat ticker)
//   registration.go — broker + asset-client wire-up + the register call
//
// Conforms to the cmd/archcheck/main.go layout convention (see
// AGENTS.md Pattern 5 — split-one-package-by-capability-stable).
// The cmd/worker/main.go entry is now 30-60 lines of flag parse +
// workerruntime.Run(ctx, *cfgPath) — the 351-line pre-P1-3 god-file
// is gone.
//
// Site-detachment contracts (godlike/07): the heartbeat goroutine
// uses the caller-owned ctx (signal.NotifyContext in main), and
// worker.NewRunner.Run(runCtx) blocks until ctx is cancelled; once
// cancelled, the deferred cleanup flushes any in-flight writes.
package workerruntime

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	worker "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	logging "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
)

// Run is the canonical entry for the worker binary. main.go is
// responsible for flag parsing + signal.NotifyContext wiring; Run
// itself does NOT manage signals (the ctx parameter is the
// caller's signal-aware context).
//
// On any non-fatal startup condition (config load, pre-flight
// /health, composition build, capability validation, registration)
// Run returns a non-nil error and a contextual zap log line has
// already fired. main.go prints the error to stderr and exits
// with code 1.
//
// On fatal composition/runtime errors (composition build failure,
// registration failure, runner.Run failure on a non-cancelled ctx),
// Run invokes log.Fatal directly — the zap logger's Fatal level
// calls os.Exit(1) so a process manager (systemd, Compose) can
// restart. Operator-visible error path is unified: every fatal
// surfaces via log.Fatal so log readers see one consistent exit
// signal.
func Run(ctx context.Context, cfgPath string) error {
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("worker config load: %w", err)
	}

	log, err := LoadLogger(cfg)
	if err != nil {
		return fmt.Errorf("worker logger init: %w", err)
	}
	defer func() { _ = logging.Sync() }()

	masterURL := ResolveMasterURL(cfg)
	log.Info("worker master URL resolved",
		zap.String("master_url", masterURL),
		zap.String("source", MasterURLSource(masterURL)),
	)

	if err := PreflightMasterHealth(masterURL); err != nil {
		log.Error("master /health pre-flight failed",
			zap.String("master_url", masterURL),
			zap.Duration("timeout", preflightTimeout),
			zap.Error(err),
		)
		return fmt.Errorf("worker /health pre-flight: %w", err)
	}
	log.Info("master /health pre-flight passed", zap.String("master_url", masterURL))

	// Build the local service graph so the worker can execute handlers.
	// The remote worker shares the same DB (via shared volume) and the
	// same service code; it only differs in how it claims jobs (HTTP
	// broker instead of in-process repo polling).
	compositionRoot, cleanup, err := app.InitWorkerComposition(cfg, log)
	if err != nil {
		log.Error("failed to build worker composition", zap.Error(err))
		return fmt.Errorf("worker composition: %w", err)
	}
	defer cleanup()

	registry, registeredCaps, err := app.BuildWorkerRegistry(compositionRoot)
	if err != nil {
		log.Error("failed to build worker registry", zap.Error(err))
		return fmt.Errorf("worker registry: %w", err)
	}
	if registry.Len() == 0 {
		log.Error("worker has no registered handlers — aborting startup")
		return fmt.Errorf("worker registry empty (no registered handlers)")
	}
	log.Info("worker registry built",
		zap.Int("handlers", registry.Len()),
		zap.Strings("capabilities", registeredCaps),
	)

	identity := WorkerIdentity()
	caps, err := ParseAndValidateCaps(Env("VELOX_WORKER_CAPABILITIES", ""), registeredCaps)
	if err != nil {
		log.Error("invalid worker capabilities", zap.Error(err))
		return fmt.Errorf("worker capabilities: %w", err)
	}
	// Freeze the registry after capabilities are resolved — no more
	// registrations are possible from this point.
	registry.Freeze()

	workspaceRoot, ws, err := initWorkspace()
	if err != nil {
		log.Error("workspace init failed", zap.Error(err))
		return fmt.Errorf("worker workspace: %w", err)
	}
	log.Info("worker workspace ready", zap.String("workspace_root", workspaceRoot))

	broker, assetClient := NewRegistrationClients(masterURL)
	session, err := RegisterWorkerSession(ctx, broker, identity, caps)
	if err != nil {
		log.Error("failed to register worker", zap.Error(err))
		return fmt.Errorf("worker registration: %w", err)
	}
	log.Info("worker registered",
		zap.String("worker_id", session.WorkerID),
		zap.String("session_id", session.SessionID),
	)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go HeartbeatLoop(runCtx, broker, identity.WorkerID, session.SessionID, log)

	runner := worker.NewRunner(broker, registry, ws, assetClient, log, identity.WorkerID, session.SessionID, caps.JobTypes)
	if rErr := runner.Run(runCtx); rErr != nil && runCtx.Err() == nil {
		log.Error("worker runner failed", zap.Error(rErr))
		return fmt.Errorf("worker runner: %w", rErr)
	}
	return nil
}
