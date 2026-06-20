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
	common "github.com/Marcuss-ops/PipelineGen/internal/api/common"
	contentapi "github.com/Marcuss-ops/PipelineGen/internal/api/content"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/api/script"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/association"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	booksService "github.com/Marcuss-ops/PipelineGen/internal/media/books"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipresolver"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	lessonsService "github.com/Marcuss-ops/PipelineGen/internal/media/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/media/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	mediastorage "github.com/Marcuss-ops/PipelineGen/internal/upload/drive"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// CleanupFunc is returned by initialization functions to handle teardown.
// Callers should defer or schedule cleanup to release resources and cancel
// background goroutines on shutdown. Nil is a valid CleanupFunc (no-op).
type CleanupFunc func()

// databases holds the single SQLite database connection.
// All data (scripts, jobs, asset index, media assets) is consolidated
// into a single file at data/media/media.db.sqlite.
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

	return &databases{
		main: mainDB,
	}, nil
}

// runAllMigrations applies database migrations to each database.
// Each database gets only the migrations relevant to its purpose.
func runAllMigrations(dbs *databases, log *zap.Logger) error {
	// 1. Generic/Main database (Velox)
	// This now includes Scripts, Pipeline, Jobs, and Asset Index migrations
	mainMigrationsDir := filepath.Join("migrations", "sqlite")
	if err := dbs.main.RunMigrations(log, mainMigrationsDir); err != nil {
		return fmt.Errorf("failed to run main migrations: %w", err)
	}

	return nil
}

// CoreDeps holds the core dependencies of the system.
type CoreDeps struct {
	Context            context.Context
	ScriptGen          *ollama.Generator
	DocClient          drive.DocClient
	DriveUploader      *drive.Uploader
	DriveClient        *gdrive.Service
	Utility            *common.UtilityHandler
	DB                 *storage.SQLiteDB // Unified database (scripts, jobs, asset index, media assets)
	ScriptsRepo        *sqlitescripts.ScriptRepository
	ImageRepo          *sqlite.ImagesRepository
	ImageService       *imgservice.Service
	ClipsRepo          *sqlite.ClipsRepository // canonical unified clips repository
	Assets             *asset.Service         // unified assets service authority (PR2)
	MonitorsRepo       *sqlite.MonitorsRepository
	VoiceoverRepo      *sqlite.VoiceoversRepository
	VoiceoverService   *voiceover.Service
	VoiceoverSync      *voiceoversync.Service
	ClipIndexerService *clipindexer.Service
	CatalogSyncService *catalogsync.Service
	ChannelMonitor     *monitor.ChannelMonitor
	CatalogRepo        *catalog.Repository
	AssocService       *association.Service
	JobsService        *appjobs.Service
	JobServiceFacade   *job.Service // domain facade wrapping JobsService
	MediaProcessor     processor.Processor
	YoutubeClipService *youtube.Service
	AssetIndexService  *assetindex.Service
	AssetTreeService   *assettree.Service
	StyleRegistry      *generation.StyleRegistry
	ClipResolver       *clipresolver.Service
	VectorStore        *vectorstore.Service
	RealtimeService    *realtime.Service
	DeletionService    *media.DeletionService
	MaintenanceService *maintenance.Service
	MemoryService      *gemmamemory.Service
	ScriptEngine       *scriptcore.Engine
	ScriptFlowHandler  *scriptpkg.ScriptFlowHandler
	BooksService       *booksService.Service
	BooksHandler       *contentapi.BooksHandler
	LessonsService     *lessonsService.Service
	LessonsHandler     *contentapi.LessonsHandler
	MediaStore         *mediastorage.Store
	ArtifactService    *artifacts.Service

	// ProviderRegistry is the canonical providers.Registry populated
	// by WireRegistry and frozen before the job runner starts. It
	// resolves SearchProvider / FetchProvider instances by name and
	// capability — see internal/application/assets/providers.
	ProviderRegistry *providers.Registry
	// startJobRunner is set by initCoreMinimal and invoked by WireServices
	// after WireRegistry completes, ensuring all job handlers are registered
	// before workers begin claiming jobs.
	startJobRunner func()
}

// InitCore bootstraps the core dependency graph.
func InitCore(cfg *config.Config, log *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	return initCoreMinimal(cfg, log, "")
}

// initCoreMinimal creates only the services needed by the text/doc server.
func initCoreMinimal(cfg *config.Config, log *zap.Logger, mode string) (*CoreDeps, CleanupFunc, error) {
	return initCoreMinimalWithContext(cfg, log, mode, context.Background())
}

func initCoreMinimalWithContext(cfg *config.Config, log *zap.Logger, mode string, parent context.Context) (*CoreDeps, CleanupFunc, error) {
	ctx, cancel := context.WithCancel(parent)

	// 1. Security & Infrastructure - Set download host whitelist from config
	hosts := append(cfg.Security.AllowedDownloadHosts, "youtube.com", "youtu.be", "www.youtube.com")
	security.SetAllowedHosts(hosts)
	log.Info("Configured download host whitelist", zap.Int("hosts_count", len(hosts)))

	// 2. Databases
	dbs, err := initDatabases(cfg, log)
	if err != nil {
		cancel()
		return nil, nil, err
	}

	// Build a cleanup function that always closes DBs even on partial failure.
	partialCleanup := func() {
		cancel()
		if dbs.main != nil {
			if err := dbs.main.Close(); err != nil {
				log.Error("Failed to close main database during cleanup", zap.Error(err))
			}
		}
	}

	// Run all database migrations centrally
	if err := runAllMigrations(dbs, log); err != nil {
		partialCleanup()
		return nil, nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	// 3. Core Services
	svcs, err := initServices(ctx, cfg, dbs, log, nil)
	if err != nil {
		partialCleanup()
		return nil, nil, err
	}

	// 5. Background Jobs (creates runner but does NOT start it yet)
	jobs := startBackgroundJobs(ctx, cfg, dbs, svcs, log, mode)

	// 6. Create VoiceoverRepo
	voRepo := sqlite.NewVoiceoversRepository(dbs.main.DB)

	// 7. Cleanup
	cleanup := buildCleanup(dbs, jobs, cancel, log)

	styleRegistry, _ := generation.NewStyleRegistry("config/generation_styles.yaml")

	return &CoreDeps{
		Context:            ctx,
		ScriptGen:          svcs.scriptGen,
		DocClient:          svcs.docClient,
		DriveUploader:      svcs.driveUploader,
		DriveClient:        svcs.driveClient,
		Utility:            svcs.utility,
		DB:                 dbs.main,
		ScriptsRepo:        svcs.scriptsRepo,
		ImageRepo:          svcs.imageRepo,
		ImageService:       svcs.imageService,
		ClipsRepo:          svcs.clipsRepo,
		Assets:             svcs.assetsSvc,
		MonitorsRepo:       svcs.monitorsRepo,
		VoiceoverRepo:      voRepo,
		VoiceoverService:   svcs.voiceoverService,
		VoiceoverSync:      svcs.voiceoverSync,
		ClipIndexerService: svcs.clipIndexerService,
		CatalogSyncService: svcs.catalogSync,
		ChannelMonitor:     jobs.channelMonitor,
		CatalogRepo:        svcs.catalogRepo,
		AssocService:       svcs.assocService,
		JobsService:        svcs.jobsService,
		JobServiceFacade:   svcs.jobServiceFacade,
		MediaProcessor:     svcs.mediaProcessor,
		YoutubeClipService: svcs.youtubeClipService,
		AssetIndexService:  svcs.assetIndexService,
		AssetTreeService:   svcs.assetTreeService,
		StyleRegistry:      styleRegistry,
		MaintenanceService: svcs.maintenanceSvc,
		VectorStore:        svcs.vectorSvc,
		RealtimeService:    svcs.realtimeSvc,
		BooksService:       svcs.booksService,
		LessonsService:     svcs.lessonsService,
		MediaStore:         svcs.mediaStore,
		startJobRunner: func() {
			if jobs.jobRunner != nil {
				svcs.jobsDispatcher.Freeze()
				concurrent.SafeGo("job-runner", func() { jobs.jobRunner.Start(ctx) })
				log.Info("Job runner started after full wiring",
					zap.Int("workers", cfg.Jobs.MaxParallelPerProject))
			}
		},
	}, cleanup, nil
}

func resolveDynamicDriveFolders(ctx context.Context, db *sql.DB, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) {
	if cfg.Drive.MediaRootFolder == "" || driveClient == nil {
		return
	}

	uploader := &drive.Uploader{Service: driveClient, Log: log}

	// Helper to resolve folder by path & source
	resolveFolder := func(name, source string) string {
		// 1. Try DB lookup first
		var id string
		err := db.QueryRowContext(ctx, "SELECT folder_id FROM clip_folders WHERE source = ? AND folder_path = ? LIMIT 1", source, name).Scan(&id)
		if err == nil && id != "" {
			return id
		}

		// 2. Query/Create on Google Drive
		id, err = uploader.GetOrCreateFolder(ctx, name, cfg.Drive.MediaRootFolder)
		if err != nil {
			log.Warn("Failed to resolve dynamic folder on Drive", zap.String("name", name), zap.Error(err))
			return ""
		}

		// 3. Cache in DB to make future startups near-instantaneous
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
	// Create subfolders under Scripts for organized doc storage
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

	// Migrate legacy docs from Scripts root to Generate subfolder
	migrateLegacyScriptDocs(ctx, driveClient, cfg, log)
}

// migrateLegacyScriptDocs moves any Google Docs sitting directly in the Scripts root folder
// into the Generate subfolder. This is a one-time idempotent migration for docs created
// before the subfolder split was introduced.
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
			log.Warn("failed to migrate legacy doc",
				zap.String("doc_id", f.Id),
				zap.String("name", f.Name),
				zap.Error(err),
			)
			continue
		}
		migrated++
		log.Info("migrated legacy doc to Generate subfolder",
			zap.String("doc_id", f.Id),
			zap.String("name", f.Name),
		)
	}
	log.Info("legacy doc migration complete",
		zap.Int("migrated", migrated),
		zap.Int("total", len(files.Files)),
	)
}

// AppDeps holds the minimal initialized dependencies for the server.
type AppDeps struct {
	Registry *module.Registry
	Cleanup  func()
}

// openLogDB creates a separate SQLite database for API request logs.
// Isolating logs from the operational DB reduces contention, write amplification,
// and backup blast radius.
func openLogDB(dataDir string) (*sql.DB, error) {
	logDir := filepath.Join(dataDir, "logs")
	// Ensure directory exists; on failure, fall back to dataDir root
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

	// Create the api_requests table if it doesn't exist.
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
func WireServices(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	coreDeps, coreClean, err := initCoreMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}

	// Initialize persistent API request logging in a dedicated SQLite database.
	// This isolates operational data from high-volume log writes.
	var logDB *sql.DB
	if cfg.Storage.DataDir != "" {
		logDB, err = openLogDB(cfg.Storage.DataDir)
		if err != nil {
			log.Warn("failed to open dedicated log database, falling back to main DB", zap.Error(err))
			logDB = coreDeps.DB.DB
		}
	} else {
		logDB = coreDeps.DB.DB
	}
	middleware.SetLogDB(logDB)

	// Wire up the registry with all modules
	registryWiring, err := WireRegistry(coreDeps.Context, cfg, log, coreDeps)
	if err != nil {
		// Leak prevention: registry wiring failed, but core services and DB
		// are already open. Clean them up before returning the error.
		coreClean()
		return nil, err
	}

	// Freeze the registry and start the job runner after all modules are wired,
	// ensuring no new modules or job handlers can be registered while workers are active.
	registryWiring.Registry.Freeze()
	if coreDeps.startJobRunner != nil {
		coreDeps.startJobRunner()
	}

	// Build a LIFO cleanup stack so every new resource is freed in reverse
	// construction order.
	cleanupStack := make([]func(), 0, 8)
	cleanupStack = append(cleanupStack, coreClean)
	cleanupStack = append(cleanupStack, func() {
		if registryWiring.ArtlistSvc != nil && registryWiring.ArtlistSvc.Service != nil {
			registryWiring.ArtlistSvc.Service.Close()
		}
	})
	// Close log DB if it was opened separately (not the main DB)
	cleanupStack = append(cleanupStack, func() {
		if logDB != nil && logDB != coreDeps.DB.DB {
			if err := logDB.Close(); err != nil {
				log.Warn("failed to close log database", zap.Error(err))
			}
		}
	})
	// StopLogger must be last so it flushes any logs emitted by earlier cleanup.
	cleanupStack = append(cleanupStack, middleware.StopLogger)

	cleanup := func() {
		for i := len(cleanupStack) - 1; i >= 0; i-- {
			cleanupStack[i]()
		}
	}

	return &AppDeps{
		Registry: registryWiring.Registry,
		Cleanup:  cleanup,
	}, nil
}

// WireMinimal creates a minimal server with core services only.
// This is the recommended entry point for local tools and minimal deployments.
func WireMinimal(cfg *config.Config, log *zap.Logger, mode string) (*AppDeps, error) {
	_, coreClean, err := initCoreMinimal(cfg, log, mode)
	if err != nil {
		return nil, err
	}
	return &AppDeps{
		Registry: nil,
		Cleanup:  coreClean,
	}, nil
}
