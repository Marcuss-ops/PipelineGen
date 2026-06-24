// Package app — bootstrap + composition root entry points (PR4d-final).
//
// PR4d-final entry points (the full PR4d transformation is now complete):
//  1. NewComposition(ctx, cfg, dbs, log) → *ComposeRoot (12 bundles).
//  2. startBackgroundJobs(ctx, cfg, dbs, root, log, mode) → *backgroundJobs.
//     The returned handle exposes startJobRunner (a closure) for WireServices
//     to invoke AFTER WireRegistry has registered all handlers.
//  3. buildCleanup(dbs, root, jobs, cancel, log) → CleanupFunc (LIFO).
//  4. WireRegistry(ctx, cfg, log, root) mounts all modules + freezes
//     ProviderRegistry. Caller invokes jobs.startJobRunner() AFTER registry
//     Freeze() so the JobRunner picks up jobs only when no further handlers
//     can register.
//
// The legacy *CoreDeps projection was removed in PR4d-final (June 2026).
// `type services struct` (in dependencies.go) was removed in the same wave —
// it duplicated logic that now lives in composition.go's Build*Bundle()s.
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"

	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"

	assetsjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	workerassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	logsink "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/logsink"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	localbroker "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/jobs/local"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// CleanupFunc is the type returned by initialization functions for teardown.
type CleanupFunc func()

// databases is the composition-root view of `storage.DatabaseSet`.
// Exists only to keep the consumer-facing API of composition.go stable
// (every Build*Bundle() takes `*databases`); the inner state delegates
// to the canonical DatabaseSet opened by `storage.OpenSet` (rule: no
// `sql.Open` outside `internal/infrastructure/database/**`).
//
// `main` and `logs` fields are kept for back-compat with the dozens of
// `dbs.main.<X>` references in `composition.go` / `shutdown.go` /
// `registry.go` / `dependencies.go`. They are populated from the
// DatabaseSet at construction time; the canonical source of truth is
// `dbs.set.Primary` / `dbs.set.Observability`.
type databases struct {
	set  *storage.DatabaseSet
	main *storage.SQLiteDB
	logs *storage.SQLiteDB
}

func (d *databases) Close() {
	if d.set != nil {
		_ = d.set.Close()
	}
}

// initDatabases opens BOTH the primary + observability DBs via the
// canonical `storage.OpenSet` (codex/db-set-and-paths). No `sql.Open`
// remains outside `internal/infrastructure/database/**`.
func initDatabases(cfg *config.Config, log *zap.Logger) (*databases, error) {
	setCfg := storage.StorageConfig{
		DataDir:             cfg.Storage.DataDir,
		PrimaryDBPath:       cfg.Storage.PrimaryDBFullPath(),
		ObservabilityDBPath: cfg.Storage.ObservabilityDBFullPath(),
		WorkspaceDir:        cfg.Storage.WorkspaceDir,
		CacheDir:            cfg.Storage.CacheDir,
		ExportDir:           cfg.Storage.ExportDir,
	}
	set, err := storage.OpenSet(setCfg, log)
	if err != nil {
		return nil, fmt.Errorf("init databases: %w", err)
	}
	return &databases{
		set:  set,
		main: set.Primary,
		logs: set.Observability,
	}, nil
}

func runAllMigrations(dbs *databases, log *zap.Logger) error {
	return dbs.set.Migrate(log)
}

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

func resolveDynamicDriveFolders(ctx context.Context, db *sqassets.ClipsRepository, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) {
	if cfg.Drive.MediaRootFolder == "" || driveClient == nil || db == nil {
		return
	}

	uploader := &drive.Uploader{Service: driveClient, Log: log}

	resolveFolder := func(name, source string) string {
		id, err := db.LookupDriveFolderIDBySourcePath(ctx, source, name)
		if err != nil {
			log.Warn("Drive folder lookup failed", zap.String("source", source), zap.String("name", name), zap.Error(err))
		}
		if id != "" {
			return id
		}

		id, err = uploader.GetOrCreateFolder(ctx, name, cfg.Drive.MediaRootFolder)
		if err != nil {
			log.Warn("Failed to resolve dynamic folder on Drive", zap.String("name", name), zap.Error(err))
			return ""
		}

		now := timeutil.FormatRFC3339(time.Now())
		if err := db.UpsertDriveFolder(ctx, sqassets.DriveFolderAttrs{
			Source:     source,
			SourceURL:  "https://drive.google.com/drive/folders/" + id,
			FolderID:   id,
			FolderPath: name,
			GroupName:  name,
			CreatedAt:  now,
			UpdatedAt:  now,
		}); err != nil {
			log.Warn("Failed to upsert Drive folder row", zap.String("source", source), zap.String("name", name), zap.Error(err))
		}
		return id
	}

	log.Info("Resolving Google Drive root folders dynamically from MediaRootFolder...", zap.String("root", cfg.Drive.MediaRootFolder))

	if cfg.Drive.StockRootFolder == "" {
		cfg.Drive.StockRootFolder = resolveFolder("Stock", "stock")
	}
	if cfg.Drive.ClipsRootFolder == "" {
		cfg.Drive.ClipsRootFolder = resolveFolder("Clips", "youtube")
	}
	if cfg.Drive.ImagesRootFolder == "" {
		cfg.Drive.ImagesRootFolder = resolveFolder("Immagini", "images")
	}
	if cfg.Drive.VoiceoverRootFolder == "" {
		cfg.Drive.VoiceoverRootFolder = resolveFolder("Voiceover", "voiceover")
	}
	if cfg.Drive.ArtlistRootFolder == "" {
		cfg.Drive.ArtlistRootFolder = resolveFolder("Artlist", "artlist")
	}
	if cfg.Drive.AvatarAIRootFolder == "" {
		cfg.Drive.AvatarAIRootFolder = resolveFolder("Avatar Ai ", "avatar_ai")
	}
	if cfg.Drive.BooksRootFolder == "" {
		cfg.Drive.BooksRootFolder = resolveFolder("Books", "books")
	}
	if cfg.Drive.ScriptsRootFolder == "" {
		cfg.Drive.ScriptsRootFolder = resolveFolder("Scripts", "scripts")
	}
	if cfg.Drive.ScriptsRootFolder != "" {
		resolveScriptsSubfolder := func(name, source string) string {
			id, err := db.LookupDriveFolderIDBySourcePath(ctx, source, name)
			if err != nil {
				log.Warn("Scripts folder lookup failed", zap.String("source", source), zap.String("name", name), zap.Error(err))
			}
			if id != "" {
				return id
			}
			id, err = uploader.GetOrCreateFolder(ctx, name, cfg.Drive.ScriptsRootFolder)
			if err != nil {
				log.Warn("Failed to create scripts subfolder on Drive", zap.String("name", name), zap.Error(err))
				return ""
			}
			now := timeutil.FormatRFC3339(time.Now())
			if err := db.UpsertDriveFolder(ctx, sqassets.DriveFolderAttrs{
				Source:     source,
				SourceURL:  "https://drive.google.com/drive/folders/" + id,
				FolderID:   id,
				FolderPath: name,
				GroupName:  name,
				CreatedAt:  now,
				UpdatedAt:  now,
			}); err != nil {
				log.Warn("Failed to upsert scripts folder row", zap.String("source", source), zap.String("name", name), zap.Error(err))
			}
			return id
		}
		if cfg.Drive.ScriptsGenerateFolder == "" {
			cfg.Drive.ScriptsGenerateFolder = resolveScriptsSubfolder("Generate", "scripts_generate")
		}
	}

	if cfg.Drive.CopertineRootFolder == "" {
		cfg.Drive.CopertineRootFolder = resolveFolder("Copertine", "copertine")
	}
	if cfg.Drive.SoundEffectsRootFolder == "" {
		cfg.Drive.SoundEffectsRootFolder = resolveFolder("Effetti Suoni Online", "sound_effects")
	}

	log.Info("Successfully resolved all dynamic root folders",
		zap.String("stock", cfg.Drive.StockRootFolder),
		zap.String("clips", cfg.Drive.ClipsRootFolder),
		zap.String("images", cfg.Drive.ImagesRootFolder),
		zap.String("voiceover", cfg.Drive.VoiceoverRootFolder),
	)
	migrateLegacyScriptDocs(ctx, driveClient, cfg, log)
}

func migrateLegacyScriptDocs(ctx context.Context, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) {
	rootID := strings.TrimSpace(cfg.Drive.ScriptsRootFolder)
	targetID := strings.TrimSpace(cfg.Drive.ScriptsGenerateFolder)
	if rootID == "" || targetID == "" || rootID == targetID || driveClient == nil {
		return
	}
	log.Info("checking for legacy docs in Scripts root folder", zap.String("root", rootID), zap.String("target", targetID))
	q := fmt.Sprintf("'%s' in parents and mimeType='application/vnd.google-apps.document' and trashed=false", rootID)
	files, err := driveClient.Files.List().Q(q).Fields("files(id, name)").PageSize(100).Context(ctx).Do()
	if err != nil {
		log.Warn("failed to list legacy docs in Scripts root", zap.Error(err))
		return
	}
	if len(files.Files) == 0 {
		log.Info("no legacy docs to migrate from Scripts root")
		return
	}
	migrated := 0
	for _, f := range files.Files {
		_, err := driveClient.Files.Update(f.Id, nil).RemoveParents(rootID).AddParents(targetID).Fields("id").Context(ctx).Do()
		if err != nil {
			log.Warn("failed to migrate legacy doc", zap.String("doc_id", f.Id), zap.String("name", f.Name), zap.Error(err))
			continue
		}
		migrated++
		log.Info("migrated legacy doc to Generate subfolder", zap.String("doc_id", f.Id), zap.String("name", f.Name))
	}
	log.Info("legacy doc migration complete", zap.Int("migrated", migrated), zap.Int("total", len(files.Files)))
}

// AppDeps holds the minimal initialized dependencies for the server.
type AppDeps struct {
	Registry      *module.Registry
	WorkerHandler interface{ RegisterRoutes(*gin.RouterGroup) }
	Lifecycle     module.LifecycleManager    // wraps startBackgroundJobs + buildCleanup
	HealthService interface{}                // *systemhealth.Service; consumed by api.NewServerWithHealth
	ReadyChecker  *systemhealth.ReadyChecker // codex/health-ready-contract: concrete type, not interface{}
	Cleanup       func()                     // kept for backward compat (tests); delegates to Lifecycle.Stop
}

// openLogDB was REMOVED in codex/db-set-and-paths. The Observability DB
// is now opened by `storage.OpenSet` and the middleware uses the
// typed `*storage.DatabaseSet.Observability` handle. See Commit 2 of
// this branch in the MR for the path-migration note.

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

	// Prerequisite steps: Drive folder validation, Qdrant collection, Outbox pool.
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
	if root != nil && root.ProcessStart != nil {
		ps := root.ProcessStart
		startupPlan = append(startupPlan, StartupStep{
			Name: "qdrant-collection", Required: true,
			Start: func(ctx context.Context) error {
				return ps()
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
	// capability is opted out at composition time (no DB / no Drive /
	// no VectorSearch); the Group skips nil probes automatically.
	var dbProbe func(ctx context.Context) error
	if root.DB != nil && root.DB.DB != nil {
		conn := root.DB.DB
		dbProbe = func(ctx context.Context) error { return conn.PingContext(ctx) }
	}
	var vectorProbe func(ctx context.Context) error
	if root.Process.VectorSvc != nil {
		vs := root.Process.VectorSvc
		vectorProbe = func(ctx context.Context) error { return vs.Health(ctx) }
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
		dbProbe, vectorProbe, driveProbe,
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
		Cleanup:       cleanup,
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
		Registry: nil,
		Cleanup: func() {
			coreClean()
		},
	}, nil
}
