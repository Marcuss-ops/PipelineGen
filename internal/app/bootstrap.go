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
	"database/sql"
	"fmt"
	"strings"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"

	assetsjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	workerassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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

func resolveDynamicDriveFolders(ctx context.Context, db *sql.DB, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) {
	if cfg.Drive.MediaRootFolder == "" || driveClient == nil {
		return
	}

	uploader := &drive.Uploader{Service: driveClient, Log: log}

	resolveFolder := func(name, source string) string {
		var id string
		err := db.QueryRowContext(ctx, "SELECT folder_id FROM clip_folders WHERE source = ? AND folder_path = ? LIMIT 1", source, name).Scan(&id)
		if err == nil && id != "" {
			return id
		}

		id, err = uploader.GetOrCreateFolder(ctx, name, cfg.Drive.MediaRootFolder)
		if err != nil {
			log.Warn("Failed to resolve dynamic folder on Drive", zap.String("name", name), zap.Error(err))
			return ""
		}

		now := timeutil.FormatRFC3339(time.Now())
		searchKey := strings.ToLower(source + name)
		searchKey = strings.ReplaceAll(searchKey, " ", "")
		_, _ = db.ExecContext(ctx, `
			INSERT OR IGNORE INTO clip_folders (id, source, source_url, folder_id, folder_path, group_name, created_at, updated_at, search_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, source, "https://drive.google.com/drive/folders/"+id, id, name, name, now, now, searchKey)
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
			var id string
			err := db.QueryRowContext(ctx, "SELECT folder_id FROM clip_folders WHERE source = ? AND folder_path = ? LIMIT 1", source, name).Scan(&id)
			if err == nil && id != "" {
				return id
			}
			id, err = uploader.GetOrCreateFolder(ctx, name, cfg.Drive.ScriptsRootFolder)
			if err != nil {
				log.Warn("Failed to create scripts subfolder on Drive", zap.String("name", name), zap.Error(err))
				return ""
			}
			now := timeutil.FormatRFC3339(time.Now())
			searchKey := strings.ToLower(source + name)
			searchKey = strings.ReplaceAll(searchKey, " ", "")
			_, _ = db.ExecContext(ctx, `INSERT OR IGNORE INTO clip_folders (id, source, source_url, folder_id, folder_path, group_name, created_at, updated_at, search_key) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, source, "https://drive.google.com/drive/folders/"+id, id, name, name, now, now, searchKey)
			return id
		}
		if cfg.Drive.ScriptsGenerateFolder == "" {
			cfg.Drive.ScriptsGenerateFolder = resolveScriptsSubfolder("Generate", "scripts_generate")
		}
	}
	if cfg.Drive.VideoAIRootFolder == "" {
		cfg.Drive.VideoAIRootFolder = resolveFolder("Ai Images", "videoai")
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
	Lifecycle     module.LifecycleManager // wraps startBackgroundJobs + buildCleanup
	Cleanup       func()                  // kept for backward compat (tests); delegates to Lifecycle.Stop
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
	// storage.OpenSet; middleware uses the typed *sql.DB inside
	// `set.Observability` so app-layer never owns a raw *sql.DB.
	var logDB *sql.DB
	if root.DB != nil && root.DB.DB != nil {
		logDB = root.DB.DB
	}
	middleware.SetLogDB(logDB)

	registryWiring, err := WireRegistry(root.Ctx, cfg, log, root)
	if err != nil {
		coreClean()
		return nil, err
	}

	registryWiring.Registry.Freeze()

	// Defer the JobRunner.Start loop to the lifecycle. The closure is
	// captured by lifecycle.go::startBackgroundJobs and freezes the
	// Dispatcher so no further handlers can register once the runner
	// claims jobs. It is invoked by serverLifecycle.Start() which
	// runs inside server.Start() after all wiring is complete.
	var deferredStartJobRunner func()
	if jobs != nil && jobs.startJobRunner != nil {
		deferredStartJobRunner = jobs.startJobRunner
	}

	// PR9-A (June 2026): capture the Drive background-initialisation
	// closure extracted from BuildDriveBundle.
	var driveStart func()
	if root != nil {
		driveStart = root.DriveStart
	}

	cleanupStack := make([]func(), 0, 8)
	cleanupStack = append(cleanupStack, coreClean)
	cleanupStack = append(cleanupStack, func() {
		if registryWiring.ArtlistSvc != nil && registryWiring.ArtlistSvc.Service != nil {
			registryWiring.ArtlistSvc.Service.Close()
		}
	})
	cleanupStack = append(cleanupStack, func() {
		if logDB != nil && logDB != root.DB.DB {
			if err := logDB.Close(); err != nil {
				log.Warn("failed to close log database", zap.Error(err))
			}
		}
	})
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

	lifecycle := NewServerLifecycle(deferredStartJobRunner, driveStart, cleanup)

	return &AppDeps{
		Registry:      registryWiring.Registry,
		WorkerHandler: workerHandler,
		Lifecycle:     lifecycle,
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
