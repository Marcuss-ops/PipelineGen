// Package app — wire_services_orchestration.go (RUNTIME ORCHESTRATION,
// July 2026 split).
//
// Split rationale, see wire_services.go header.
//
// This file owns the RUNTIME ORCHESTRATION stage: the chain that runs
// AFTER composition has produced the *wiring.ComposeRoot:
//
//  1. Set the log sink (request-middleware observability) — logsink +
//     middleware.SetLogSink.
//  2. WireRegistry + Registry.Freeze
//  3. Build the StartupStep plan via the buildStartupPlan helper
//     (in the startup_plan sibling file) — drive-init / qdrant-
//     collection / outbox-pool / chrome-pool-prewarm + jobs.append.
//  4. Re-cast root.Jobs.Broker to *localbroker.Broker for the assetSvc
//     + workerHandler surface.
//  5. Construct assetSvc + workerHandler (PR-SOURCESTAGER-CONSOLIDATE,
//     July 2026).
//  6. Build the cleanupStack (LIFO) — drives graceful teardown order.
//  7. Build the readiness probes (dbProbe + driveProbe + qdrantProbe)
//     and the ServerLifecycleWithProbes.
//  8. Build the QdrantHealthHandler (HIGH #7, July 2026) for the
//     /qdrant/live + /qdrant/ready endpoints.
//  9. Return AppDeps (the server-side projection of the root).
//
// Cross-file deps (same package `app`, accessed without explicit import):
//   - WireRegistry (registry.go)
//   - NewServerLifecycleWithProbes (lifecycle.go)
//   - buildStartupPlan (startup_plan sibling — wire_services_startup_plan.go)
//   - validateQdrantIndexerCompatibility (build_bundles_qdrant_gates.go,
//     invoked from buildStartupPlan — orchestrator does NOT call it
//     directly anymore, the gate moved to the helper)
//
// The assetSvc + workerHandler construction is the one DOMAIN-COUPLED
// block in this file (it consumes root.Search + root.Repos for AssetSvc
// and broker for WorkerHandler). Per-domain wiring for stock / voiceover /
// artlist / images lives in the dedicated wire_* / build_bundles_* files;
// this file is cross-cutting.
package app

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	assetsjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/assets"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/logsink"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
	localbroker "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/jobs/local"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/stager"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/gin-gonic/gin"
)

// WireServices initializes the full server composition root.
//
// PR4d-final flow (June 2026): initCompositionMinimal builds the *wiring.ComposeRoot
// via NewComposition, starts background jobs (the StartupStep plan —
// including the job runner — is captured in jobs.startupPlan), builds
// cleanup. WireRegistry takes ONLY root + ctx — there is no *CoreDeps
// projection. The job-runner StartupStep (built by
// lifecycle_job_runner.go::buildJobRunnerStep, PR4.8) freezes the
// dispatcher during Start so all handlers registered in
// NewComposition + WireRegistry are accepted before any new handler
// can register.
//
// The startup-plan construction moved to buildStartupPlan (in the
// startup_plan sibling file) so the dependency order is visible at a
// glance and the gates (validateQdrantIndexerCompatibility +
// CollectionManager-nil) are consolidated.
func WireServices(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	if err := initLinguistics(cfg, log); err != nil {
		return nil, fmt.Errorf("wire services: %w", err)
	}

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

	// Startup plan construction consolidates the four Required
	// pre-flight steps (drive-init / qdrant-collection / outbox-pool /
	// chrome-pool-prewarm) plus the qdrant-config-mismatch gate.
	// Lives in the startup_plan sibling (see
	// wire_services_startup_plan.go header).
	startupPlan, err := buildStartupPlan(cfg, root, jobs, log)
	if err != nil {
		coreClean()
		return nil, err
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
	// SQLiteDB-set; root.DB and dbs.Main share the underlying *sql.DB).
	// Closing it twice would error; the partialCleanup inside coreClean
	// already handles dbs.Main.Close() once.
	cleanupStack = append(cleanupStack, middleware.StopLogger)

	// Reuse the broker constructed in initCompositionMinimalWithContext
	// (must be set before startBackgroundJobs so the job-runner sees
	// a non-nil CompletionPort). The local broker satisfies both
	// appjobs.CompletionPort (stored in wiring.JobsBundle) and appjobs.Broker
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
	// QDRANT-005 (June 2026) PR 4 (refactor/single-qdrant-runtime):
	// bind the Qdrant liveness probe into the lifecycle readiness
	// barrier so /ready actually checks Qdrant reachability instead of
	// relying solely on DB+Drive. Constructed only when Qdrant is
	// enabled; nil-safe when disabled.
	//
	// PR 4 typed wiring.ProcessBundle.QdrantHealthProbe as *qdrant.HealthProbe
	// (was `any` pre-PR4). The concrete type satisfies the probe
	// contract via the compile-time assertion in
	// internal/infrastructure/qdrant/health.go (`_ interface{ Probe(...) error }
	// = (*HealthProbe)(nil)`); the runtime type-assertion
	// `if p, ok := ... .(...) ...; ok` is gone.
	var qdrantProbe interface{ Probe(context.Context) error }
	if cfg.Qdrant.Enabled && root != nil && root.Process != nil {
		qdrantProbe = root.Process.QdrantHealthProbe
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

	// HIGH #7 (July 2026): construct QdrantHealthHandler for /qdrant/live
	// and /qdrant/ready. nil-safe when Qdrant is disabled.
	//
	// Sprint 3.4 step1 (godlike/06 SSOT — single composition root):
	// the adapter qdrantEndpointAdapter wires the 3 infra types into the
	// systemhealth.QdrantEndpointPort; the handler (api/transport) consumes
	// ONLY the port — no infra imports reach the api layer.
	var qdrantHealth any
	if cfg.Qdrant.Enabled && root != nil && root.Process != nil &&
		root.Process.CollectionManager != nil && root.Process.QdrantSearcher != nil {
		var embedder search.TextEmbedder
		if root.AI != nil && root.AI.OllamaClient != nil {
			ollamaEmb := embeddings.NewOllamaEmbedderAdapter(root.AI.OllamaClient)
			embedder = search.NewTextEmbedderAdapter(ollamaEmb)
		}
		qdrantEndpointPort := newQdrantEndpointAdapter(
			root.Process.QdrantHealthProbe,
			root.Process.CollectionManager,
			root.Process.QdrantSearcher,
			embedder,
		)
		qdrantHealth = transport.NewQdrantHealthHandler(qdrantEndpointPort)
	}

	// FASE 7 image-search routing singleton: nil-safe extraction from
	// the composition root. Inline pattern matches the qdrantHealth
	// handling above; nil root or nil root.Domains keeps
	// AppImage.ImageSearchResolver as a nil-typed-port so BuildServer’s
	// pass-through carries nil to api.ServerDeps.ImageSearchResolver.
	var imageRouting routing.ImageSearchResolver
	if root != nil && root.Domains != nil {
		imageRouting = root.Domains.ImageSearchResolver
	}

	return &AppDeps{
		Handlers: AppHandlers{
			Registry:             registryWiring.Registry,
			WorkerHandler:        workerHandler,
			InternalMediaHandler: internalMediaHandler,
			OutboxHandler:        registryWiring.OutboxHandler,
			MediasearchHandler:   registryWiring.MediasearchHandler,
		},
		Runtime: AppRuntime{
			Lifecycle: lifecycle,
		},
		Health: AppHealth{
			QdrantProbe:   qdrantProbe,
			QdrantHealth:  qdrantHealth,
			HealthService: healthSvc,
			ReadyChecker:  readyChecker,
		},
		Images: AppImage{
			ImageSearchResolver: imageRouting,
		},
	}, nil
}
