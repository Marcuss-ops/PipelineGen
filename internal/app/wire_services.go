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
//     The returned handle exposes the StartupStep list (job runner last).
//  3. buildCleanup(dbs, root, jobs, cancel, log) → CleanupFunc (LIFO).
//  4. WireRegistry(ctx, cfg, log, root) mounts all modules + freezes
//     ProviderRegistry.
//
// The legacy *CoreDeps projection was removed in PR4d-final (June 2026).
package app

import (
	"context"
	"fmt"

	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"

	assetsjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	workerassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	logsink "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/logsink"
	localbroker "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/jobs/local"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"

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

	dbs, err := initDatabases(cfg, log)
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

	jobs := startBackgroundJobs(ctx, cfg, dbs, root, log, mode)
	cleanup := buildCleanup(dbs, root, jobs, cancel, log)

	return root, jobs, cleanup, nil
}

// WireServices initializes the full server composition root.
//
// PR4d-final flow (June 2026): initCompositionMinimal builds the *ComposeRoot
// via NewComposition, starts background jobs (including the
// startJobRunner closure stored on jobs), builds cleanup. WireRegistry takes
// ONLY root + ctx — there is no *CoreDeps projection. JobRunner starts via
// jobs.startJobRunner() AFTER registry freeze (WireRegistry) so all
// handlers registered during NewComposition are accepted before the
// dispatcher freezes.
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

	// Prerequisite steps: Drive folder validation, Outbox pool.
	// PG-034 (June 2026): qdrant-collection step removed. These are
	// required steps — failure aborts the entire startup sequence.
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

	// Wire the internal worker handler so external workers (including
	// the Docker worker container) can register via /internal/v1/workers/register.
	workerNodesRepo := workerassets.NewWorkerNodesRepository(root.DB.DB)
	broker := localbroker.New(root.Jobs.Repo, workerNodesRepo)
	assetSvc := assetsjobs.NewService(
		root.Search.AssetIndexService,
		root.Repos.Assets,
		root.Repos.ImageRepo,
		root.Repos.VoiceoverRepo,
		log,
	)
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
	// PG-034 (June 2026): the vector probe was removed along with Qdrant.
	// The Group skips nil probes automatically.
	var dbProbe func(ctx context.Context) error
	if root.DB != nil && root.DB.DB != nil {
		conn := root.DB.DB
		dbProbe = func(ctx context.Context) error { return conn.PingContext(ctx) }
	}
	var driveProbe func(ctx context.Context) error
	if root.Drive != nil && root.Drive.DriveClient != nil {
		dc := root.Drive.DriveClient
		// Drive probe uses About.Get (canonical Drive liveness endpoint).
		// Files.Get("root") is NOT a valid Drive API alias — it does not
		// resolve to the user's root folder — so using it as a probe
		// would make the readiness barrier fail on every healthy Drive.
		// About.Get is what production token validation does in
		// internal/infrastructure/drive/uploader.go (canonical contract).
		//
		// Note: the parent ctx already carries the per-probe timeout
		// (serverLifecycle.Start wraps each probe in a 5s context.WithTimeout
		// before invoking the barrier), so this fn does not need to derive
		// its own. The barrier's first-error-wins semantics propagate
		// ctx.DeadlineExceeded back to the caller as a clean error.
		driveProbe = func(ctx context.Context) error {
			_, err := dc.About.Get().Fields("user").Context(ctx).Do()
			return err
		}
	}
	lifecycle := NewServerLifecycleWithProbes(
		startupPlan, cleanup,
		dbProbe, nil, driveProbe,
		log,
	)

	var healthSvc interface{}
	if root != nil && root.Utility != nil {
		healthSvc = root.Utility.HealthService
	}
	var readyChecker *systemhealth.ReadyChecker
	if root != nil && root.Utility != nil {
		readyChecker = root.Utility.ReadyChecker
	}
	return &AppDeps{
		Registry:      registryWiring.Registry,
		WorkerHandler: workerHandler,
		Lifecycle:     lifecycle,
		HealthService: healthSvc,
		ReadyChecker:  readyChecker,
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
		Registry:  nil,
		Lifecycle: &minimalLifecycle{stop: coreClean},
	}, nil
}

// minimalLifecycle wraps a single stop function as a LifecycleManager.
// Used by WireMinimal to keep the AppDeps contract uniform: all callers
// use Lifecycle.Stop for teardown, never a separate Cleanup func.
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
