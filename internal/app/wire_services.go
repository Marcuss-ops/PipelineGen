// Package app — composition-root entry points (PR4d-final + PG-006, June 2026).
//
// PG-006 (June 2026): these entry-point functions were extracted out of
// bootstrap.go so the in-scope bootstrap.go can stay strictly free of
// `internal/infrastructure/*` imports. The signatures (`AppDeps`,
// `InitComposition`, `WireServices`, `WireMinimal`) and runtime
// semantics are unchanged so every cmd/admin caller keeps compiling
// without churn. Lifted verbatim — header comments preserved so the
// git-blame trail for logic decisions stays attached to the actual
// implementation lines.
//
// PR4d-final (June 2026): entry-point summary:
//  1. NewComposition(ctx, cfg, dbs, log) → *ComposeRoot (12 bundles).
//  2. startBackgroundJobs(ctx, cfg, dbs, root, log, mode) → *backgroundJobs.
//     The returned handle exposes the StartupStep list (job runner last,
//     built via buildJobRunnerStep in internal/app/lifecycle_job_runner.go).
//  3. buildCleanup(dbs, root, jobs, cancel, log) → CleanupFunc (LIFO).
//  4. WireRegistry(ctx, cfg, log, root) mounts all modules + freezes
//     ProviderRegistry.
//
// The legacy *CoreDeps projection was removed in PR4d-final (June 2026).
package app

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"

	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	assetsjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/assets"
	jobsfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/finalizer"
	workerassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	logsink "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/logsink"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
	localbroker "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/jobs/local"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/stager"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// InitComposition returns the unified *ComposeRoot tree directly.
// PR4d-final (June 2026): the legacy *CoreDeps projection was deleted —
// the public entry point now returns *ComposeRoot + *backgroundJobs so
// callers can start the JobRunner AFTER WireRegistry has registered all
// handlers.
func InitComposition(cfg *config.Config, log *zap.Logger) (*ComposeRoot, *backgroundJobs, CleanupFunc, error) {
	return initCompositionMinimal(cfg, log, "")
}

func initCompositionMinimal(cfg *config.Config, log *zap.Logger, mode string) (*ComposeRoot, *backgroundJobs, CleanupFunc, error) {
	return initCompositionMinimalWithContext(context.Background(), cfg, log, mode, context.Background())
}

func initCompositionMinimalWithContext(ctx context.Context, cfg *config.Config, log *zap.Logger, mode string, parent context.Context) (*ComposeRoot, *backgroundJobs, CleanupFunc, error) {
	ctx, cancel := context.WithCancel(parent)

	hosts := append(cfg.Security.AllowedDownloadHosts, "youtube.com", "youtu.be", "www.youtube.com")
	security.SetAllowedHosts(hosts)
	log.Info("Configured download host whitelist", zap.Int("hosts_count", len(hosts)))

	dbs, err := initDatabases(ctx, cfg, log)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}

	partialCleanup := func() {
		cancel()
		if dbs.main != nil {
			if err := dbs.main.Close(); err != nil {
				log.Error("Failed to close main database during cleanup", zap.Error(err))
			}
		}
	}

	if err := runAllMigrations(dbs, log); err != nil {
		partialCleanup()
		return nil, nil, nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	root, err := NewComposition(ctx, cfg, dbs, log)
	if err != nil {
		partialCleanup()
		return nil, nil, nil, fmt.Errorf("failed to build composition root: %w", err)
	}

	// PR-WORKER-RUNNER-INPROCESS-MIGRATION (July 2026): construct the
	// local broker BEFORE startBackgroundJobs so the job-runner sees a
	// non-nil CompletionPort and routes artifact-producing jobs
	// through CompleteWithArtifacts instead of failing with
	// "CompletionPort not wired". The WireServices caller reuses this
	// same broker instance via type-assertion for the full
	// appjobs.Broker surface needed by the internal worker handler.
	workerNodesRepo := workerassets.NewWorkerNodesRepository(dbs.dualPool.Writer)

	progressCoalesceWindow := 100 * time.Millisecond
	if cfg.Jobs.ProgressCoalesceWindow != "" {
		if parsed, perr := time.ParseDuration(cfg.Jobs.ProgressCoalesceWindow); perr == nil && parsed >= 0 {
			progressCoalesceWindow = parsed
		} else if perr != nil {
			log.Warn("invalid VELOX_PROGRESS_COALESCE_WINDOW; using default 100ms",
				zap.String("raw", cfg.Jobs.ProgressCoalesceWindow), zap.Error(perr))
		}
	}
	// Coalescer is ALWAYS constructed — when Window=0 the coalescer
	// runs in passthrough mode (every Take → immediate SetProgress),
	// which keeps the broker-side Progress + FlushJob paths uniform.
	progressSink := root.Jobs.Repo
	progressCoalescer := localbroker.NewProgressCoalescer(progressSink, localbroker.ProgressCoalesceConfig{
		Window: progressCoalesceWindow,
	}, log)

	broker, err := localbroker.New(localbroker.Deps{
		Jobs:      root.Jobs.Repo,
		Workers:   workerNodesRepo,
		Progress:  progressSink,
		Coalescer: progressCoalescer,
		Log:       log,
	})
	if err != nil {
		partialCleanup()
		return nil, nil, nil, fmt.Errorf("wire broker: %w", err)
	}
	// TIGHTENING (July 2026, godlike/07): explicit nil-broker fail-closed guard. Today
	// localbroker.New never returns (nil, nil), so this branch is dead code; it is a
	// permanent composition-time assertion that any future factory-method mutation
	// cannot smuggle a nil pointer into root.Jobs.Broker. If tripped, the partialCleanup
	// path is identical to the err branch so the operator's runbook surface is uniform.
	if broker == nil {
		partialCleanup()
		return nil, nil, nil, fmt.Errorf("wire broker: constructed broker is nil (Deps produced nil pointer despite err=nil)")
	}
	root.Jobs.Broker = broker

	// PR-COMPLETE-WORKER-BROAD-FIX (July 2026): wire the canonical
	// JobFinalizer into the broker at construction time so Path B
	// artifact-producing jobs (script.generate, image.generate.google,
	// books.process, lessons.process) can complete via the single-TX
	// finalization spine. root.Outbox.EventsRepo is available because
	// NewComposition already ran BuildProcessBundle which populated it.
	if root.Outbox != nil && root.Outbox.EventsRepo != nil && root.DB != nil && root.DB.DB != nil {
		assetTx := assetfinalizer.NewAssetTxFinalizer(log)
		finalizer := jobsfinalizer.New(root.DB.DB, root.Outbox.EventsRepo, assetTx, log)
		broker.WithFinalizer(finalizer)
		log.Info("wired JobFinalizer into local broker at construction time (Path B artifact-producing jobs can now complete via CompleteWithArtifacts)")
	} else {
		log.Warn("JobFinalizer NOT wired into local broker (one or more deps nil — root.Outbox, root.Outbox.EventsRepo, or root.DB). Path B artifact-producing jobs will fail at CompleteWithArtifacts with ErrFinalizerNotConfigured.",
			zap.Bool("Outbox_nil", root.Outbox == nil))
	}

	jobs := startBackgroundJobs(ctx, cfg, dbs, root, log, mode)
	cleanup := buildCleanup(dbs, root, jobs, cancel, log)

	return root, jobs, cleanup, nil
}

// WireServices initializes the full server composition root.
//
// PR4d-final flow (June 2026): initCompositionMinimal builds the *ComposeRoot
// via NewComposition, starts background jobs (the StartupStep plan —
// including the job runner — is captured in jobs.startupPlan), builds
// cleanup. WireRegistry takes ONLY root + ctx — there is no *CoreDeps
// projection. The job-runner StartupStep (built by
// lifecycle_job_runner.go::buildJobRunnerStep, PR4.8) freezes the
// dispatcher during Start so all handlers registered in
// NewComposition + WireRegistry are accepted before any new handler
// can register.
func WireServices(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	root, jobs, coreClean, err := initCompositionMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}

	// Observability DB is now opened (and schema-validated) by
	// storage.OpenSet. The middleware no longer holds *sql.DB; the
	// composition root constructs a typed SQLiteRequestLogSink that
	// owns the *sql.DB internally and exposes the typed
	// RequestLogSink port to middleware.SetLogSink. The adapter lives
	// under internal/infrastructure/database/sqlite/logsink so the
	// API layer never carries raw database/sql imports.
	if root.DB != nil && root.DB.DB != nil {
		logSink := logsink.NewSQLiteRequestLogSink(root.DB.DB, log)
		middleware.SetLogSink(logSink)
	} else {
		middleware.SetLogSink(nil)
	}

	registryWiring, err := WireRegistry(root.Ctx, cfg, log, root)
	if err != nil {
		coreClean()
		return nil, err
	}

	registryWiring.Registry.Freeze()

	// Lifecycle-runtime-ownership (June 2026): ALL background workers, scanners,
	// monitors, sweepers, and the job runner are captured in the startupPlan
	// built by startBackgroundJobs during composition but NOT executed until
	// serverLifecycle.Start. The plan includes Drive, Qdrant, and Outbox
	// prerequisite steps (from root.DriveStart/ProcessStart/OutboxStart) so
	// the dependency order is preserved: Drive → Qdrant → Outbox → plan
	// steps → job runner (always last).
	var startupPlan []StartupStep

	// Prerequisite steps: Drive folder validation, Qdrant collection, Outbox pool.
	// QDRANT-003 (June 2026): qdrant-collection step re-added with EnsureSchema.
	// These are required steps — failure aborts the entire startup sequence.
	if root != nil && root.DriveStart != nil {
		ds := root.DriveStart
		startupPlan = append(startupPlan, StartupStep{
			Name: "drive-init", Required: true,
			Start: func(ctx context.Context) error {
				return ds()
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// PR-QDRANT-CONFIG-MISMATCH-GATE (July 2026): defense-in-depth
	// gate at all 4 Qdrant composition sites. FOURTH wire site
	// (WireServices is the registry-level composition orchestrator
	// that adds EnsureSchema as part of the startup plan). godlike/07
	// no-fake-availability: catch the misconfiguration at the registry
	// level so the startup plan aborts BEFORE EnsureSchema is wired
	// against a half-built Qdrant runtime. Cross-ref:
	// internal/app/build_bundles_qdrant_gates.go::validateQdrantIndexerCompatibility.
	if err := validateQdrantIndexerCompatibility(cfg); err != nil {
		coreClean()
		return nil, err
	}

	// QDRANT-003: EnsureSchema step — creates/validates the versioned Qdrant collection
	// and sets the runtime alias. Required when Qdrant is enabled.
	// Fails startup if Qdrant is enabled but CollectionManager is nil.
	if cfg.Qdrant.Enabled {
		if root.Process == nil || root.Process.CollectionManager == nil {
			return nil, fmt.Errorf("qdrant is enabled but CollectionManager is nil — QDRANT-003 requires Client + CollectionManager + IndexWriter + Searcher when qdrant.enabled=true")
		}
		cm := root.Process.CollectionManager
		startupPlan = append(startupPlan, StartupStep{
			Name: "qdrant-collection", Required: true,
			Start: func(ctx context.Context) error {
				_, err := cm.EnsureSchema(ctx)
				return err
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	if root != nil && root.OutboxStart != nil {
		os := root.OutboxStart
		startupPlan = append(startupPlan, StartupStep{
			Name: "outbox-pool", Required: true,
			Start: func(ctx context.Context) error {
				return os()
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// PR-CHROME-POOL-WARM-AT-BOOT (July 2026): the Slides image worker
	// pool warmup used to be fired by a readiness probe (lazily on the
	// first /ready poll). That allowed the HTTP listener to accept
	// traffic against a cold / partially-warm pool, and added the full
	// browser-launch + login + slides.new latency to the FIRST image
	// generation request after boot.
	//
	// Convert the prewarm into a synchronous Required StartupStep.
	// serverLifecycle.Start runs the plan in order and BLOCKS until
	// every step succeeds; the HTTP listener bind in cmd/server/main.go
	// happens AFTER Start returns, so the server cannot advertise a
	// "running" state until every chrome pool worker has reported
	// ready. Falls into the canonical order:
	//
	//   drive-init → qdrant-collection → outbox-pool
	//   → chrome-pool-prewarm    [NEW, Required]
	//   → background services (scanner, monitor, sweepers)
	//   → job runner (always last)
	//
	// Per-worker profile isolation is already enforced by session.py
	// (MASTER_STORAGE.profile_<id> + PROFILE_DIR_<id>_<pid>); this
	// step just guarantees those workers actually exist server-side
	// before requests can reach them.
	if root != nil && root.Domains != nil && root.Domains.ImageService != nil && cfg.Features.ImagesEnabled {
		imgSvc := root.Domains.ImageService
		poolSize := cfg.Concurrency.MaxConcurrentGoogleSlidesGenerations
		startupPlan = append(startupPlan, StartupStep{
			Name: "chrome-pool-prewarm", Required: true,
			Start: func(ctx context.Context) error {
				log.Info("StartupStep: prewarming ChromeImageProviderPool", zap.Int("pool_size", poolSize))
				imgSvc.TriggerPrewarm(ctx, "startup-prewarm", poolSize)

				report := imgSvc.Diagnostics()
				if !report.ImageGenWired {
					return fmt.Errorf("chrome image provider pool is not wired")
				}
				if !report.ImageGenHealthy {
					return fmt.Errorf("chrome image provider pool is unhealthy")
				}
				if report.ImageGenCooldownProfiles > 0 {
					return fmt.Errorf("chrome image provider pool has %d unhealthy/cooldown profiles", report.ImageGenCooldownProfiles)
				}
				log.Info("StartupStep: ChromeImageProviderPool prewarmed successfully and healthy", zap.Int("pool_size", poolSize))
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Append the background services plan (scanner, monitor, sweepers, etc.)
	// followed by the job runner (always last, required).
	if jobs != nil {
		startupPlan = append(startupPlan, jobs.startupPlan...)
	}

	cleanupStack := make([]func(), 0, 8)
	cleanupStack = append(cleanupStack, coreClean)
	cleanupStack = append(cleanupStack, func() {
		if registryWiring.ArtlistSvc != nil && registryWiring.ArtlistSvc.Service != nil {
			registryWiring.ArtlistSvc.Service.Close()
		}
	})
	// PG-011: removed the defensive logDB.Close block — the observability
	// DB is the same handle as root.DB (the OpenSet opens both as a single
	// SQLiteDB-set; root.DB and dbs.main share the underlying *sql.DB).
	// Closing it twice would error; the partialCleanup inside coreClean
	// already handles dbs.main.Close() once.
	cleanupStack = append(cleanupStack, middleware.StopLogger)

	// Reuse the broker constructed in initCompositionMinimalWithContext
	// (must be set before startBackgroundJobs so the job-runner sees
	// a non-nil CompletionPort). The local broker satisfies both
	// appjobs.CompletionPort (stored in JobsBundle) and appjobs.Broker
	// (needed here for the full worker-handler surface).
	lb, ok := root.Jobs.Broker.(*localbroker.Broker)
	if !ok {
		return nil, fmt.Errorf("wire services: root.Jobs.Broker is not *local.Broker (got type %T)", root.Jobs.Broker)
	}
	broker := lb // *localbroker.Broker satisfies appjobs.Broker

	assetSvc := assetsjobs.NewService(
		root.Search.AssetIndexService,
		root.Repos.Assets,
		root.Repos.ImageRepo,
		root.Repos.VoiceoverRepo,
		log,
	)
	// PR-SOURCESTAGER-CONSOLIDATE (July 2026): wire the canonical
	// HTTPSourceStager into the jobs/assets Service so the URL
	// download path (Service.fetch) routes through
	// StageSourceV2 instead of inline http.NewRequest +
	// httpClient.Do. The staging dir is co-located with the rest
	// of the temp space and the 2-minute timeout mirrors the
	// legacy httpClient.Timeout so behaviour parity is preserved
	// for callers (Service.Download) that do not observe the
	// intermediate file.
	if assetSvc != nil {
		jobsStagerDir := filepath.Join(cfg.Storage.TempPath(), "jobs-staged-sources")
		jobsSourceStager, jobsStagerErr := stager.NewHTTPSourceStager(
			jobsStagerDir,
			&http.Client{Timeout: 2 * time.Minute},
			log,
		)
		if jobsStagerErr != nil {
			log.Error("wire_services: NewHTTPSourceStager init failed; jobs/assets URL downloads will fail closed",
				zap.String("stager_dir", jobsStagerDir), zap.Error(jobsStagerErr))
		} else {
			assetSvc.WithSourceStager(jobsSourceStager)
		}
	}
	// PR3 (June 2026): Wave 14 close — internal/api/workers/ was eliminated
	// and the handler moved to internal/api/jobs/ as a sibling receiver
	// (WorkersBrokerHandler). The ctor signature is identical so existing
	// logic is preserved without churn.
	workerHandler := jobsapi.NewWorkersBrokerHandler(broker, assetSvc, log)
	log.Info("wired internal worker handler (broker + asset transfer)")

	cleanup := func() {
		for i := len(cleanupStack) - 1; i >= 0; i-- {
			cleanupStack[i]()
		}
	}

	// commit fix/lifecycle-readiness — wire readiness-barrier probes so
	// serverLifecycle.Start actually USES ctx and fail-closes if any
	// dependency is unreachable. Probes are nil when the corresponding
	// capability is opted out at composition time (no DB / no Drive);
	// The vector probe was removed during earlier cleanup.
	// The Group skips nil probes automatically.
	var dbProbe func(ctx context.Context) error
	if root.DB != nil && root.DB.DB != nil {
		conn := root.DB.DB
		dbProbe = func(ctx context.Context) error { return conn.PingContext(ctx) }
	}
	var driveProbe func(ctx context.Context) error
	// FASE 9 (June 2026, P0.1 / DRIVE-005): the canonical Pattern 0 port
	// for the Drive readiness probe is drive.Admin (Ping method). Replace
	// the raw *gdrive.Service.About.Get call site in wire_services: the
	// composition root now hands out a typed port per Pattern 0 instead
	// of leaking the gdrive SDK into the readiness-builder closure.
	//
	// Typed-nil-safe: root.Drive.Admin is a DRIVE.Admin interface value;
	// when the uploader is nil (Drive feature disabled) admin is a true
	// nil interface, not a typed-nil (Build*Bundle guards the assignment
	// explicitly). The `root.Drive.Admin != nil` check is therefore the
	// correct "Drive feature configured" gate. The barrier's first-error
	// semantics propagate ctx.DeadlineExceeded back to the caller cleanly.
	//
	// Equality with previous contract: (*drive.Uploader).Ping(ctx) calls
	// u.Service.About.Get().Fields("user").Context(ctx).Do() — bit-for-bit
	// identical to the legacy probe implementation above. No observable
	// behaviour change; only the consumer surface moves from raw SDK to
	// typed port.
	//
	// PR-DRIVE-SOFT-MODE-PROBE (July 2026): when StrictStartupValidation
	// is false (soft-mode boot, e.g. VELOX_DRIVE_STRICT_STARTUP_VALIDATION=false),
	// skip the Drive readiness probe entirely. The credentials are dummy/non-existent
	// and the probe's Google API call would fail with 401, blocking the entire
	// readiness barrier. Soft-mode operators already accept that Drive is
	// unavailable; the probe would be a false-negative.
	if root.Drive != nil && root.Drive.Admin != nil && cfg.Drive.StrictStartupValidation {
		admin := root.Drive.Admin
		driveProbe = func(ctx context.Context) error {
			return admin.Ping(ctx)
		}
	}
	lifecycle := NewServerLifecycleWithProbes(
		startupPlan, cleanup,
		dbProbe, nil, driveProbe,
		log,
	)

	// (chrome-pool-prewarm now lives in startupPlan; the prior
	//  lifecycle.AddProbe("chrome-pool", ...) is gone, so HTTP traffic
	//  never reaches a cold pool.)

	var healthSvc any
	if root != nil && root.Utility != nil {
		healthSvc = root.Utility.HealthService
	}
	var readyChecker *systemhealth.ReadyChecker
	if root != nil && root.Utility != nil {
		readyChecker = root.Utility.ReadyChecker
	}
	var internalMediaHandler interface{ RegisterInternalMediaRoutes(*gin.RouterGroup) }
	if registryWiring.Assets != nil {
		internalMediaHandler = registryWiring.Assets.InternalMediaHandler
	}
	// QDRANT-005 (June 2026) PR 4 (refactor/single-qdrant-runtime):
	// bind the Qdrant liveness probe into the lifecycle readiness
	// barrier so /ready actually checks Qdrant reachability instead of
	// relying solely on DB+Drive. Constructed only when Qdrant is
	// enabled; nil-safe when disabled.
	//
	// PR 4 typed ProcessBundle.QdrantHealthProbe as *qdrant.HealthProbe
	// (was `any` pre-PR4). The concrete type satisfies the probe
	// contract via the compile-time assertion in
	// internal/infrastructure/qdrant/health.go (`_ interface{ Probe(...) error }
	// = (*HealthProbe)(nil)`); the runtime type-assertion
	// `if p, ok := ... .(...) ...; ok` is gone.
	var qdrantProbe interface{ Probe(context.Context) error }
	if cfg.Qdrant.Enabled && root != nil && root.Process != nil {
		qdrantProbe = root.Process.QdrantHealthProbe
	}

	// HIGH #7 (July 2026): construct QdrantHealthHandler for /qdrant/live
	// and /qdrant/ready. nil-safe when Qdrant is disabled.
	var qdrantHealth any
	if cfg.Qdrant.Enabled && root != nil && root.Process != nil &&
		root.Process.CollectionManager != nil && root.Process.QdrantSearcher != nil {
		var embedder search.TextEmbedder
		if root.AI != nil && root.AI.OllamaClient != nil {
			ollamaEmb := embeddings.NewOllamaEmbedderAdapter(root.AI.OllamaClient)
			embedder = search.NewTextEmbedderAdapter(ollamaEmb)
		}
		qdrantHealth = transport.NewQdrantHealthHandler(
			root.Process.QdrantHealthProbe,
			root.Process.CollectionManager,
			root.Process.QdrantSearcher,
			embedder,
		)
	}

	return &AppDeps{
		Registry:             registryWiring.Registry,
		WorkerHandler:        workerHandler,
		InternalMediaHandler: internalMediaHandler,
		OutboxHandler:        registryWiring.OutboxHandler,
		MediasearchHandler:   registryWiring.MediasearchHandler,
		QdrantProbe:          qdrantProbe,
		QdrantHealth:         qdrantHealth,
		Lifecycle:            lifecycle,
		HealthService:        healthSvc,
		ReadyChecker:         readyChecker,
	}, nil
}

// WireMinimal creates a minimal server with core services only.
// Uses InitComposition to build the full *ComposeRoot (so background jobs,
// migrations, and DB are wired identically to WireServices), but returns
// an empty registry so the caller can mount routes selectively.
func WireMinimal(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	_, _, coreClean, err := initCompositionMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}
	return &AppDeps{
		Registry:  nil, // forward-pointer: PR-COMPOSITION-REGISTRY-LIVE-WIRE
		Lifecycle: &minimalLifecycle{stop: coreClean},
	}, nil
}

// minimalLifecycle wraps a single stop function as a LifecycleManager.
// Used by WireMinimal to keep the AppDeps contract uniform: all callers
// use Lifecycle.Stop for teardown, never a separate Cleanup func.
//
// QDRANT-005 (June 2026): minimalLifecycle now also implements AddProbe
// (no-op) so it stays compatible with the extended api.LifecycleManager
// interface. WireMinimal callers don't get the readiness barrier —
// the AddProbe call simply pushes the probe onto the discard tray.
type minimalLifecycle struct {
	stop func()
}

func (m *minimalLifecycle) Start(_ context.Context) error { return nil }
func (m *minimalLifecycle) Stop(_ context.Context) error {
	if m.stop != nil {
		m.stop()
	}
	return nil
}

// AddProbe is a no-op for minimalLifecycle. WireMinimal does not run
// the readiness barrier; the probe is discarded so the call site remains
// type-safe.
func (m *minimalLifecycle) AddProbe(_ string, _ func(context.Context) error) {}
