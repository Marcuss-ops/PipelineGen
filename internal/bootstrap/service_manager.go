package bootstrap

import (
	"context"
	"os"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/core/maintenance"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/media"

	"velox/go-master/internal/service/catalogsync"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	"velox/go-master/internal/service/clipindexer"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/assetregistry"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/service/voiceoversync"
	"velox/go-master/internal/service/scheduler"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)


func initServices(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*services, error) {
	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	scriptGen := ollama.NewGenerator(ollamaClient)

	docClient, err := drive.NewDocClient(ctx, cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	driveClient, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		log.Warn("Google Drive client not initialized", zap.Error(err))
	}

	// 4. Media Processing
	clipsOnlyRepo := clips.NewRepository(dbs.clips.DB, log)
	mediaProcessor := initMediaProcessor(cfg, clipsOnlyRepo, log)
	clipsRegistry := assetregistry.NewClipsRegistry(clipsOnlyRepo)

	// Ensure all storage directories exist
	storageDirs := []string{
		cfg.Storage.DataDir,
		cfg.Storage.VoiceoversPath(),
		cfg.Storage.AssetsPath(),
		cfg.Storage.DownloadsPath(),
		cfg.Storage.BackupsPath(),
		cfg.Storage.TempPath(),
		cfg.Storage.AnimationsPath(),
		cfg.Storage.YoutubeClipsPath(),
		cfg.Storage.ArtlistPath(),
		cfg.Storage.ImagesPath(),
	}
	for _, dir := range storageDirs {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Warn("Failed to create storage directory", zap.String("path", dir), zap.Error(err))
		}
	}

	// Asset services (Index & Tree)
	assetIndexService, assetTreeService, err := initAssetServices(dbs, log)
	if err != nil {
		return nil, err
	}

	monitorsRepo := monitors.NewRepository(dbs.main.DB)

	// Create LifecycleService for youtubeclip using common factory
	ytLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	youtubeClipService := youtubeclip.NewService(
		cfg,
		log,
		clipsOnlyRepo,
		monitorsRepo,
		driveClient,
		mediaProcessor,
		ytLifecycle,
	)

	voDir := cfg.Storage.VoiceoversPath()
	voRepo := voiceovers.NewRepository(dbs.voiceover.DB)

	// Create voiceover registry adapter
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)

	// Create LifecycleService for voiceover using common factory
	voLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voRegistryAdapter,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	voService := voiceover.NewService(cfg, cfg.Paths.PythonScriptsDir, voDir, log, driveClient, voLifecycle)
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	clipsRepo := clips.NewRepository(dbs.stock.DB, log)
	artlistRepo := clips.NewRepository(dbs.artlist.DB, log)

	scriptsRepo := scripts.NewScriptRepository(dbs.main.DB)
	imageRepo := images.NewRepository(dbs.images.DB)

	imageService := imgservice.NewService(cfg, imageRepo, clipsRepo, driveClient, log)
	imageService.SetNvidiaConfig(cfg.External.NvidiaAPIKey, cfg.External.NvidiaModel)

	// Asset resolver (queries asset_index first, then falls back to specific DBs)
	clipsRepos := map[string]*clips.Repository{
		"youtube": clipsOnlyRepo,
		"stock":    clipsRepo,
		"artlist":  artlistRepo,
	}
	resolverCfg := &assetindex.ResolverConfig{
		ClipsRepos:    clipsRepos,
		ImageRepo:     imageRepo,
		VoiceoverRepo: voRepo,
	}
	assetResolver := assetindex.NewResolver(assetIndexService, resolverCfg, log)
	log.Info("asset resolver initialized")

	clipIndexerService := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		DBPath:                dbs.artlist.Path(),
	}, dbs.artlist.DB, dbs.artlist.Path(), log)

	indexingService := indexing.NewService(clipsRepo, clipIndexerService, log)
	catalogRepo := catalog.NewRepository(clipsOnlyRepo, clipsRepo, artlistRepo)

	assocService := association.NewService(cfg.Storage.DataDir, cfg.Paths.NodeScraperDir, clipsRepo, artlistRepo, clipsOnlyRepo, catalogRepo)

	// Build sync targets centrally
	syncTargets := buildSyncTargets(cfg, clipsOnlyRepo, clipsRepo, artlistRepo)

	catalogSync := catalogsync.NewService(driveClient, syncTargets, assetIndexService, assetTreeService, log)

	// Voiceover sync service
	var voiceoverSync *voiceoversync.Service
	if cfg.Drive.VoiceoverRootFolder != "" && voRepo != nil {
		voiceoverSync = voiceoversync.NewService(driveClient, voRepo, assetTreeService, cfg.Drive.VoiceoverRootFolder, log)
		log.Info("Voiceover sync service initialized", zap.String("root_folder_id", cfg.Drive.VoiceoverRootFolder))
	}

	// Jobs system
	jobsRepo := jobrepo.NewRepository(dbs.jobs.DB, log)
	jobsDispatcher := jobservice.NewDispatcher()
	if os.Getenv("VELOX_ENABLE_TEST_JOB_HANDLERS") == "true" {
		jobservice.RegisterTestHandlers(jobsDispatcher, log)
	}
	jobsService := jobservice.NewService(jobsRepo, jobsDispatcher, log)

	// Register job handlers
	catalogSync.RegisterHandler(jobsService)
	youtubeClipService.RegisterHandler(jobsService)
	voService.RegisterHandler(jobsService)

	// Create drive uploader
	var driveUploader *drive.Uploader
	if driveClient != nil {
		driveUploader = &drive.Uploader{Service: driveClient, Log: log}
	}

	// Create deletion service
	deletionSvc := media.NewDeletionService(
		artlistRepo,
		clipsOnlyRepo,
		clipsRepo,
		voRepo,
		imageRepo,
		driveUploader,
		assetTreeService,
		assetIndexService,
		log,
	)

	// Maintenance service
	maintenanceSvc := maintenance.NewService(cfg, log, assetIndexService, assetTreeService, deletionSvc, jobsService)
	maintenanceSvc.RegisterHandler()

	// Lifecycle Scheduler
	lifecycleScheduler := scheduler.NewLifecycleScheduler(cfg, jobsService, log)
	go lifecycleScheduler.Start(ctx)

	return &services{
		scriptGen:          scriptGen,
		docClient:          docClient,
		driveClient:        driveClient,
		utility:            common.NewUtilityHandler(),
		scriptsRepo:        scriptsRepo,
		imageRepo:          imageRepo,
		imageService:       imageService,
		stockDriveRepo:     clipsRepo,
		artlistRepo:        artlistRepo,
		clipsOnlyRepo:      clipsOnlyRepo,
		monitorsRepo:       monitorsRepo,
		voiceoverService:   voService,
		voiceoverSync:      voiceoverSync,
		indexingService:    indexingService,
		clipIndexerService: clipIndexerService,
		catalogRepo:        catalogRepo,
		catalogSync:        catalogSync,
		assocService:       assocService,
		jobsRepo:           jobsRepo,
		jobsService:        jobsService,
		jobsDispatcher:     jobsDispatcher,
		mediaProcessor:     mediaProcessor,
		youtubeClipService: youtubeClipService,
		assetIndexService:  assetIndexService,
		assetTreeService:   assetTreeService,
		assetResolver:      assetResolver,
		lifecycleScheduler: lifecycleScheduler,
		maintenanceSvc:     maintenanceSvc,
	}, nil
}

