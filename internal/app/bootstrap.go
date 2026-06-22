// Package app — bootstrap + composition root entry points (PR4d-final).
//
// PR4d-final entry points (the full PR4d transformation is now complete):
//   1. NewComposition(ctx, cfg, dbs, log) → *ComposeRoot (12 bundles).
//   2. startBackgroundJobs(ctx, cfg, dbs, root, log, mode) → *backgroundJobs.
//      The returned handle exposes startJobRunner (a closure) for WireServices
//      to invoke AFTER WireRegistry has registered all handlers.
//   3. buildCleanup(dbs, root, jobs, cancel, log) → CleanupFunc (LIFO).
//   4. WireRegistry(ctx, cfg, log, root) mounts all modules + freezes
//      ProviderRegistry. Caller invokes jobs.startJobRunner() AFTER registry
//      Freeze() so the JobRunner picks up jobs only when no further handlers
//      can register.
//
// The legacy *CoreDeps projection was removed in PR4d-final (June 2026).
// `type services struct` (in dependencies.go) was removed in the same wave —
// it duplicated logic that now lives in composition.go's Build*Bundle()s.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	workersapi "github.com/Marcuss-ops/PipelineGen/internal/api/workers"

	assetsjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	workerassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	localbroker "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/jobs/local"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// CleanupFunc is the type returned by initialization functions for teardown.
type CleanupFunc func()

// databases holds the single SQLite database connection.
type databases struct {
	main *storage.SQLiteDB
}

func (d *databases) Close() {
	if d.main != nil {
		d.main.Close()
	}
}

func initDatabases(cfg *config.Config, log *zap.Logger) (*databases, error) {
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBMedia, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize main database: %w", err)
	}
	return &databases{main: mainDB}, nil
}

func runAllMigrations(dbs *databases, log *zap.Logger) error {
	mainMigrationsDir := filepath.Join("migrations", "sqlite")
	if err := dbs.main.RunMigrations(log, mainMigrationsDir); err != nil {
		return fmt.Errorf("failed to run main migrations: %w", err)
	}
	return nil
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
	Cleanup       func()
}

// openLogDB creates a separate SQLite database for API request logs.
func openLogDB(dataDir string) (*sql.DB, error) {
	logDir := filepath.Join(dataDir, "logs")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "api_requests.db.sqlite")
	dsn := logPath + "?_journal_mode=WAL&_busy_timeout=2000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open log db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping log db: %w", err)
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS api_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts DATETIME DEFAULT CURRENT_TIMESTAMP,
			request_id TEXT,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			status INTEGER,
			duration_ms REAL,
			client_ip TEXT,
			user_id TEXT,
			bytes_in INTEGER,
			bytes_out INTEGER,
			user_agent TEXT,
			error TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_api_requests_ts ON api_requests(ts);
	`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create log schema: %w", err)
	}
	return db, nil
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

	var logDB *sql.DB
	if cfg.Storage.DataDir != "" {
		logDB, err = openLogDB(cfg.Storage.DataDir)
		if err != nil {
			log.Warn("failed to open dedicated log database, falling back to main DB", zap.Error(err))
			logDB = root.DB.DB
		}
	} else {
		logDB = root.DB.DB
	}
	middleware.SetLogDB(logDB)

	registryWiring, err := WireRegistry(root.Ctx, cfg, log, root)
	if err != nil {
		coreClean()
		return nil, err
	}

	registryWiring.Registry.Freeze()

	// Trigger the JobRunner.Start loop. The closure is captured by
	// lifecycle.go::startBackgroundJobs and freezes the Dispatcher so no
	// further handlers can register once the runner claims jobs.
	if jobs != nil && jobs.startJobRunner != nil {
		jobs.startJobRunner()
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
	workerHandler := workersapi.NewInternalworkerHandler(broker, assetSvc, log)
	log.Info("wired internal worker handler (broker + asset transfer)")

	cleanup := func() {
		for i := len(cleanupStack) - 1; i >= 0; i-- {
			cleanupStack[i]()
		}
	}

	return &AppDeps{
		Registry:      registryWiring.Registry,
		WorkerHandler: workerHandler,
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
