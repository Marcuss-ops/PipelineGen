package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/security"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

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

	// 6. Create VoiceoverRepo and canonical AssetStore
	voRepo := voiceovers.NewRepository(dbs.main.DB)
	assetStore := assets.NewAssetStoreSQLite(dbs.main.DB, log)

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
		AssetStore:         assetStore,
		AssetRepo:          svcs.assetRepo,
		AssetLocationRepo:  svcs.assetLocationsRepo,
		AssetProcessingRepo: svcs.assetProcessingRepo,
		AssetQueryService:   svcs.assetQueryService,
		MonitorsRepo:       svcs.monitorsRepo,
		VoiceoverRepo:      voRepo,
		VoiceoverService:   svcs.voiceoverService,
		VoiceoverSync:      svcs.voiceoverSync,
		IndexingService:    svcs.indexingService,
		ClipIndexerService: svcs.clipIndexerService,
		CatalogSyncService: svcs.catalogSync,
		ChannelMonitor:     jobs.channelMonitor,
		CatalogRepo:        svcs.catalogRepo,
		AssocService:       svcs.assocService,
		JobsService:        svcs.jobsService,
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

func resolveDynamicDriveFolders(ctx context.Context, db *sql.DB, driveClient *driveapi.Service, cfg *config.Config, log *zap.Logger) {
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
func migrateLegacyScriptDocs(ctx context.Context, driveClient *driveapi.Service, cfg *config.Config, log *zap.Logger) {
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
