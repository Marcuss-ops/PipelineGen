// Package workerruntime — the worker binary's runtime, split out of
// cmd/worker/main.go per P1-3 (June 2026).
//
// File layout (1-orientation-per-file convention, AGENTS.md Pattern 5):
//
//	run.go          — this file; Run() orchestrator + WorkerComposition + buildWorkerComposition
//	config.go       — LoadConfig (cfgPath -> *config.Config, error)
//	identity.go     — WorkerIdentity tuple (id/name/version/hostname)
//	capabilities.go — ParseAndValidateCaps (env-raw JSON -> WorkerCapabilities)
//	profiles.go     — WorkerProfile, WorkerProfileRegistry, ResolveCapabilities
//	preflight.go    — master URL resolve + /health pre-flight loop
//	heartbeat.go    — HeartbeatLoop (background broker.Heartbeat ticker)
//	registration.go — broker + asset-client wire-up + the register call
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
	"os"
	"os/exec"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	logging "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	obsmetrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// WorkerComposition is the canonical post-build surface for the
// worker. The buildWorkerComposition helper returns this for both
// profile-gated and legacy (no-profile) worker startup.
//
// Field semantics:
//
//	Registry       — frozen handler registry (no more registrations
//	                 allowed post-helper)
//	RegisteredCaps — handler-derived cap slice the worker registered
//	                 with the broker
//	Caps           — effective capability set the worker advertises
//	                 (may be narrowed from RegisteredCaps by env
//	                 override or profile ceiling)
//	Workspace      — pre-built *worker.Workspace for ALL paths
//	                 (unified: the original pre-refactor code only
//	                 pre-built it for Creator; non-Creator paths
//	                 called initWorkspace() after the switch — the
//	                 refactor unifies both into the helper)
//	WorkspaceRoot  — the directory path for job-artefact staging
//	Cleanup        — runs on Run() exit via `defer comp.Cleanup()`
//	                 (LIFO: defer order is comp.Cleanup() first,
//	                 then `defer cancel()` so cancel() runs first at
//	                 shutdown, freeing the runner, then comp.Cleanup
//	                 flushes any in-flight writes)
type WorkerComposition struct {
	Registry        *worker.Registry
	RegisteredCaps  []string
	Caps            appjobs.WorkerCapabilities
	Workspace       *worker.Workspace
	WorkspaceRoot   string
	Cleanup         func()
	ObservabilityDB *storage.SQLiteDB
	JobLedger       capjobregistry.Registry
}

// buildWorkerComposition builds the canonical worker composition
// for the given profile (or legacy no-profile path). 3 startup paths
// converge on a single returned *WorkerComposition:
//
//	profile=nil              → legacy (no-profile) path
//	profile.Name="creator"   → Creator runtime (minimal, no DB/Drive/Qdrant)
//	profile.Name="renderer"  → RenderingGen runtime (overlay-only, no DB/Drive/Qdrant)
//	profile.Name=other       → standard profile-gated worker
//
// Cleanup invariant: if any step fails, the helper calls cleanup()
// BEFORE returning the error. This fixes a pre-refactor resource leak
// where returning from inside the switch block never called the
// workerCleanup / creatorCleanup returned by InitWorkerComposition /
// BuildCreatorRuntime (defer cleanup() was registered after the switch,
// so the cleanup func was never executed on failure paths). The named
// return + smart defer pattern below guarantees cleanup runs on
// ANY error path (workerErr, registryErr, capsErr, wsErr).
func buildWorkerComposition(ctx context.Context, cfg *config.Config, profile *WorkerProfile, log *zap.Logger) (comp *WorkerComposition, err error) {
	var cleanup func()
	defer func() {
		// Cleanup invariant: any returned error (named return `err`)
		// triggers cleanup() exactly once. The local `cleanup` var
		// captures the func returned by InitWorkerComposition /
		// BuildCreatorRuntime; subsequent failed steps leak the
		// composition-root resources if not invoked. The named
		// return pattern makes the error value visible to this
		// defer (it sees the FINAL value of `err` at function
		// return, not the value at defer-registration time).
		if err != nil && cleanup != nil {
			cleanup()
		}
	}()
	_ = ctx // reserved for future ctx-aware error wrapping (e.g. ctx-aware retry on registry build)

	if profile == nil {
		// Legacy (no-profile) path
		compositionRoot, workerCleanup, workerErr := app.InitWorkerComposition(cfg, log)
		if workerErr != nil {
			log.Error("failed to build worker composition", zap.Error(workerErr))
			err = fmt.Errorf("worker composition: %w", workerErr)
			return nil, err
		}
		cleanup = workerCleanup

		registry, registeredCaps, registryErr := app.BuildWorkerRegistry(compositionRoot)
		if registryErr != nil {
			log.Error("failed to build worker registry", zap.Error(registryErr))
			err = fmt.Errorf("worker registry: %w", registryErr)
			return nil, err
		}
		if registry.Len() == 0 {
			log.Error("worker has no registered handlers — aborting startup")
			err = fmt.Errorf("worker registry empty (no registered handlers)")
			return nil, err
		}
		log.Info("worker registry built",
			zap.Int("handlers", registry.Len()),
			zap.Strings("capabilities", registeredCaps),
		)

		caps, capsErr := ParseAndValidateCaps(Env("VELOX_WORKER_CAPABILITIES", ""), registeredCaps)
		if capsErr != nil {
			log.Error("invalid worker capabilities", zap.Error(capsErr))
			err = fmt.Errorf("worker capabilities: %w", capsErr)
			return nil, err
		}

		workspaceRoot, ws, wsErr := initWorkspace()
		if wsErr != nil {
			log.Error("workspace init failed", zap.Error(wsErr))
			err = fmt.Errorf("worker workspace: %w", wsErr)
			return nil, err
		}
		registry.Freeze()
		return &WorkerComposition{
			Registry:        registry,
			RegisteredCaps:  registeredCaps,
			Caps:            caps,
			Workspace:       ws,
			WorkspaceRoot:   workspaceRoot,
			Cleanup:         cleanup,
			ObservabilityDB: compositionRoot.ObservabilityDB,
			JobLedger:       compositionRoot.Jobs.JobLedger,
		}, nil
	}

	log.Info("worker profile loaded",
		zap.String("profile", profile.Name),
		zap.Strings("allowed_job_types", profile.AllowedJobTypes),
		zap.Int("max_parallel", profile.MaxParallel),
	)

	switch profile.Name {
	case "creator":
		// Creator Blocco 3.1 (now P0 C8 — July 2026): minimal
		// composition without DB, Drive, Qdrant, Scheduler, or
		// CatalogSync reach. Registry + workspace are built by
		// the canonical CreatorRuntime factory
		// (app.BuildCreatorRuntime in creator_runtime.go).
		// The no-DB / no-Qdrant / no-Scheduler / no-CatalogSync
		// contract is enforced at the canonical surface via
		// compile-time orphan pin + import-allowlist AST scan.
		creatorRuntime, creatorCleanup, creatorErr := app.BuildCreatorRuntime(cfg, log)
		if creatorErr != nil {
			log.Error("failed to build creator runtime", zap.Error(creatorErr))
			err = fmt.Errorf("creator runtime: %w", creatorErr)
			return nil, err
		}
		cleanup = creatorCleanup

		registeredCaps := creatorRuntime.Caps.JobTypes
		log.Info("creator runtime ready",
			zap.Int("handlers", creatorRuntime.Registry.Len()),
			zap.Strings("capabilities", registeredCaps),
			zap.String("workspace_root", creatorRuntime.Workspace.Root),
		)
		creatorRuntime.Registry.Freeze()
		return &WorkerComposition{
			Registry:        creatorRuntime.Registry,
			RegisteredCaps:  registeredCaps,
			Caps:            creatorRuntime.Caps,
			Workspace:       creatorRuntime.Workspace,
			WorkspaceRoot:   creatorRuntime.Workspace.Root,
			Cleanup:         cleanup,
			ObservabilityDB: nil,
		}, nil

	case "renderer":
		renderingRuntime, renderingCleanup, renderingErr := app.BuildRenderingRuntime(cfg, log)
		if renderingErr != nil {
			err = fmt.Errorf("rendering runtime: %w", renderingErr)
			return nil, err
		}
		chrononBin := os.Getenv("CHRONON_RENDER_BIN")
		if chrononBin == "" {
			chrononBin = "/opt/chronon3d/bin/chronon3d_cli"
		}
		if _, lookErr := exec.LookPath(chrononBin); lookErr != nil {
			renderingCleanup()
			return nil, fmt.Errorf("renderer profile: Chronon renderer %q is unavailable: %w", chrononBin, lookErr)
		}
		cleanup = renderingCleanup
		caps, capsErr := ResolveCapabilities(profile, Env("VELOX_WORKER_CAPABILITIES", ""), renderingRuntime.Registry.JobTypes())
		if capsErr != nil {
			err = fmt.Errorf("renderer capabilities: %w", capsErr)
			return nil, err
		}
		gpu, ffmpeg := DetectRendererHardware()
		if profile.RequiresGPU && !gpu {
			err = fmt.Errorf("renderer preflight: NVIDIA GPU not detected")
			return nil, err
		}
		if profile.RequiresFFmpeg && !ffmpeg {
			err = fmt.Errorf("renderer preflight: ffmpeg not detected")
			return nil, err
		}
		caps.GPU, caps.FFmpeg = gpu, ffmpeg
		return &WorkerComposition{Registry: renderingRuntime.Registry, RegisteredCaps: renderingRuntime.Registry.JobTypes(), Caps: caps, Workspace: renderingRuntime.Workspace, WorkspaceRoot: renderingRuntime.Workspace.Root, Cleanup: cleanup}, nil

	default:
		// Standard profile-gated worker: full ComposeRoot with DB, Drive, etc.
		compositionRoot, workerCleanup, workerErr := app.InitWorkerComposition(cfg, log)
		if workerErr != nil {
			log.Error("failed to build worker composition", zap.Error(workerErr))
			err = fmt.Errorf("worker composition: %w", workerErr)
			return nil, err
		}
		cleanup = workerCleanup

		registry, registeredCaps, registryErr := app.BuildProfileWorkerRegistry(compositionRoot, profile.AllowedJobTypes)
		if registryErr != nil {
			log.Error("failed to build profile worker registry", zap.Error(registryErr))
			err = fmt.Errorf("profile worker registry: %w", registryErr)
			return nil, err
		}
		if registry.Len() == 0 {
			log.Error("profile worker has no registered handlers — aborting startup")
			err = fmt.Errorf("profile worker registry empty (no registered handlers)")
			return nil, err
		}
		log.Info("profile worker registry built",
			zap.Int("handlers", registry.Len()),
			zap.Strings("capabilities", registeredCaps),
		)

		caps, capsErr := ResolveCapabilities(profile, Env("VELOX_WORKER_CAPABILITIES", ""), registeredCaps)
		if capsErr != nil {
			log.Error("invalid worker capabilities", zap.Error(capsErr))
			err = fmt.Errorf("worker capabilities: %w", capsErr)
			return nil, err
		}

		workspaceRoot, ws, wsErr := initWorkspace()
		if wsErr != nil {
			log.Error("workspace init failed", zap.Error(wsErr))
			err = fmt.Errorf("worker workspace: %w", wsErr)
			return nil, err
		}
		registry.Freeze()
		return &WorkerComposition{
			Registry:        registry,
			RegisteredCaps:  registeredCaps,
			Caps:            caps,
			Workspace:       ws,
			WorkspaceRoot:   workspaceRoot,
			Cleanup:         cleanup,
			ObservabilityDB: compositionRoot.ObservabilityDB,
			JobLedger:       compositionRoot.Jobs.JobLedger,
		}, nil
	}
}

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

	if err := PreflightMasterHealth(ctx, masterURL); err != nil {
		log.Error("master /health pre-flight failed",
			zap.String("master_url", masterURL),
			zap.Duration("timeout", preflightTimeout),
			zap.Error(err),
		)
		return fmt.Errorf("worker /health pre-flight: %w", err)
	}
	log.Info("master /health pre-flight passed", zap.String("master_url", masterURL))

	profileName := Env("VELOX_WORKER_PROFILE", "")
	if profileName != "renderer" {
		if err := PreflightMasterScriptGenerateReady(ctx, masterURL); err != nil {
			log.Error("master /ready script_generate pre-flight failed",
				zap.String("master_url", masterURL),
				zap.Duration("timeout", preflightTimeout),
				zap.Error(err),
			)
			return fmt.Errorf("worker /ready script_generate pre-flight: %w", err)
		}
		log.Info("master /ready script_generate pre-flight passed", zap.String("master_url", masterURL))
	}

	// Detect profile BEFORE building composition — the Creator uses a
	// minimal graph (no DB, Drive, Qdrant, Repos).
	//
	// Audit-pin (FIX-APP-WORKERRUNTIME-SYNTAX, July 2026): err is already in scope via the earlier cfg/log := LoadConfig/LoadLogger calls; the slim 7-file split pins err to the smallest possible scope (no var err error).
	var profile *WorkerProfile
	if profileName != "" {
		profileReg := NewProfileRegistry()
		p, lookupErr := profileReg.Lookup(profileName)
		if lookupErr != nil {
			log.Error("invalid worker profile", zap.String("profile", profileName), zap.Error(lookupErr))
			return fmt.Errorf("worker profile: %w", lookupErr)
		}
		profile = p
	}

	identity := WorkerIdentity()

	comp, err := buildWorkerComposition(ctx, cfg, profile, log)
	if err != nil {
		return err
	}
	defer comp.Cleanup()

	log.Info("worker workspace ready", zap.String("workspace_root", comp.WorkspaceRoot))

	broker, assetClient := NewRegistrationClients(masterURL)
	session, err := RegisterWorkerSession(ctx, broker, identity, comp.Caps)
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

	runner := worker.NewRunner(broker, comp.Registry, comp.Workspace, assetClient, log, identity.WorkerID, session.SessionID, comp.Caps.JobTypes)
	// Canonical observability: durable run lifecycle is written to the
	// dedicated observability DB; Prometheus remains a projection.
	var recorder kernobs.Recorder
	if comp.ObservabilityDB != nil && comp.ObservabilityDB.DB != nil {
		recorder = obsmetrics.NewSQLiteRecorderWithLogger(comp.ObservabilityDB.DB, log)
		if reconciler, ok := recorder.(kernobs.AbandonedRunReconciler); ok {
			if _, err := reconciler.RecoverAbandoned(context.Background(), time.Now().UTC()); err != nil {
				log.Warn("observability abandoned-run recovery failed", zap.Error(err))
			}
		}
	} else {
		log.Warn("observability recorder unavailable; using metrics-only projection")
	}
	runner.WithObserver(kernobs.NewRunObserverWithCollector(recorder, obsmetrics.NewRunReportsCollector()))
	runner.WithJobRegistry(comp.JobLedger)
	if rErr := runner.Run(runCtx); rErr != nil && runCtx.Err() == nil {
		log.Error("worker runner failed", zap.Error(rErr))
		return fmt.Errorf("worker runner: %w", rErr)
	}
	return nil
}
