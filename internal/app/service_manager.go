package app

import (
	"context"
	"os"
	"velox/go-master/internal/media/videomuscles"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/core/maintenance"
	"velox/go-master/internal/media"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/association"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/repository/voiceovers"

	"velox/go-master/internal/config"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/assetregistry"
	"velox/go-master/internal/media/catalogsync"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/generation"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/media/indexing"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/reranker"
	"velox/go-master/internal/media/semantic"
	"velox/go-master/internal/media/storage"
	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/media/voiceoversync"
	pkgffmpeg "velox/go-master/internal/pkg/media/ffmpeg"
	"velox/go-master/internal/sources/youtube"
	"velox/go-master/internal/storage/scheduler"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/api/handlers/script/handlers"

	"go.uber.org/zap"
)

func initServices(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*services, error) {
	styleRegistry, _ := generation.NewStyleRegistry("config/generation_styles.yaml")

	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	ollamaClient.SetNvidiaConfig(cfg.External.UseNvidiaForLLM, cfg.External.NvidiaAPIKey, cfg.External.NvidiaLLMModel)
	scriptGen := ollama.NewGenerator(ollamaClient)

	docClient, err := drive.NewDocClient(ctx, cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	driveClient, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		log.Warn("Google Drive client not initialized", zap.Error(err))
	}
	var driveUploader *drive.Uploader
	if driveClient != nil {
		driveUploader = &drive.Uploader{Service: driveClient, Log: log}
		imageRoot := cfg.Drive.ImagesFolder()
		if imageRoot != "" {
			go ensureStyleDriveFolders(ctx, driveUploader, imageRoot, styleRegistry, log)
		}
	}

	// 4. Media Processing
	clipsOnlyRepo := clips.NewRepository(dbs.media.DB, log)
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

	clipIndexerService := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		DBPath:                dbs.media.Path(),
	}, dbs.media.DB, dbs.media.Path(), log)

	// Vector store (Qdrant) and real-time matching
	var vectorSvc *vectorstore.Service
	var realtimeSvc *realtime.Service

	if cfg.VectorSearch.Enabled {
		qdrantCfg := vectorstore.Config{
			URL:              cfg.VectorSearch.URL,
			Collection:       cfg.VectorSearch.Collection,
			TextVectorName:   cfg.VectorSearch.TextVectorName,
			VisualVectorName: cfg.VectorSearch.VisualVectorName,
			TextDimensions:   cfg.VectorSearch.TextDimensions,
			VisualDimensions: cfg.VectorSearch.VisualDimensions,
			MinInstantScore:  cfg.VectorSearch.MinInstantScore,
			TimeoutMs:        cfg.VectorSearch.TimeoutMs,
		}
		qdrantClient := vectorstore.NewQdrantClient(qdrantCfg)
		vectorSvc = vectorstore.NewService(qdrantClient, qdrantCfg, log)
		if err := vectorSvc.EnsureCollection(ctx); err != nil {
			log.Warn("vector store collection setup failed (will retry on upsert)", zap.Error(err))
		}

		// Wire vector store into clipindexer
		clipIndexerAdapter := vectorstore.NewClipIndexerAdapter(dbs.media.DB, vectorSvc, qdrantCfg, log)
		if clipIndexerAdapter != nil {
			clipIndexerService.SetVectorStore(clipIndexerAdapter)
			log.Info("vector store enabled for clip indexer")
		}
	}

	monitorsRepo := monitors.NewRepository(dbs.main.DB)

	// Unified media storage + destination resolver (replaces assetdestination)
	storageResolver := storage.NewResolver(
		storage.MediaRoot(cfg.Storage.MediaPath()),
		storage.DriveRoot(cfg.Drive.RootFolder()),
	)
	mediaStore := storage.NewStore(storageResolver, driveUploader, cfg.Drive.RootFolder(), cfg.Drive.ImagesRootFolder, cfg.Drive.VideoAIRootFolder, cfg.Drive.SoundEffectsRootFolder, log)
	destResolver := storage.NewDestinationResolver(mediaStore)

	// Create LifecycleService for youtubeclip using common factory
	ytLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	clipProcessor := pkgffmpeg.New(cfg)
	videoPipeline := videomuscles.NewPipeline(cfg, log, clipProcessor)

	youtubeClipService := youtube.NewService(
		cfg,
		log,
		clipsOnlyRepo,
		monitorsRepo,
		driveClient,
		mediaProcessor,
		videoPipeline,
		ytLifecycle,
		clipIndexerService,
		destResolver,
	)

	voDir := cfg.Storage.VoiceoversPath()
	voRepo := voiceovers.NewRepository(dbs.media.DB)

	// Create voiceover registry adapter
	voRegistryAdapter := voiceover.NewVoiceoverRegistryAdapter(voRepo)

	// Create LifecycleService for voiceover using common factory
	voLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    voRegistryAdapter,
		DriveClient: driveClient,
		AssetIndex:  assetIndexService,
	}, log)

	voService := voiceover.NewService(cfg, dbs.media.DB, cfg.Paths.PythonScriptsDir, voDir, log, driveUploader, voLifecycle, destResolver)
	log.Info("Voiceover service initialized", zap.String("python_scripts_dir", cfg.Paths.PythonScriptsDir))

	clipsRepo := clips.NewRepository(dbs.media.DB, log)
	artlistRepo := clips.NewRepository(dbs.media.DB, log)

	scriptsRepo := scripts.NewScriptRepository(dbs.main.DB)
	imageRepo := images.NewRepository(dbs.media.DB)

	imageService := imgservice.NewService(cfg, imageRepo, clipsRepo, driveClient, styleRegistry, log)
	imageService.SetNvidiaConfig(cfg.External.NvidiaAPIKey, cfg.External.NvidiaModel)
	imageService.SetGoogleAccountingConfig(
		cfg.GoogleAccounting.ServerURL,
		cfg.GoogleAccounting.DownloadDir,
		cfg.GoogleAccounting.VidsProjectID,
		cfg.GoogleAccounting.FlowProjectID,
	)
	imageService.SetMediaStore(mediaStore)
	imageService.SetLLMGenerator(scriptGen)
	if vectorSvc != nil {
		imageService.SetVectorStore(vectorSvc)
	}

	// Wire unified metadata writer into image service (covers images, videos, audio)
	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	imageService.SetMetadataWriter(metaWriter)

	// Wire up semantic tagger for voiceover metadata enrichment
	// Uses metaWriter.GeneratePayload() (the unified code path) instead of calling semantic.Tagger() directly.
	// This ensures voiceovers get the same fallback + extension logic as all other media types.
	voService.SetSemanticTagger(func(ctx context.Context, prompt, style, mediaType, generator string) (*voiceover.SemanticTaggerResult, error) {
		payload, _, err := metaWriter.GeneratePayload(ctx, semantic.WriteRequest{
			AssetID:   "",
			AssetType: "voiceover",
			MediaType: mediaType,
			Source:    "voiceover",
			Generator: generator,
			Style:     style,
			Prompt:    prompt,
		})
		if err != nil {
			return nil, err
		}
		return &voiceover.SemanticTaggerResult{
			SearchText: payload.SearchText,
			Tags:       payload.Tags,
			Subjects:   payload.Subjects,
			Mood:       payload.Mood,
		}, nil
	})

	// Asset resolver (queries asset_index first, then falls back to specific DBs)
	clipsRepos := map[string]*clips.Repository{
		"youtube": clipsOnlyRepo,
		"stock":   clipsRepo,
		"artlist": artlistRepo,
	}
	resolverCfg := &assetindex.ResolverConfig{
		ClipsRepos:    clipsRepos,
		ImageRepo:     imageRepo,
		VoiceoverRepo: voRepo,
	}
	assetResolver := assetindex.NewResolver(assetIndexService, resolverCfg, log)
	log.Info("asset resolver initialized")

	indexingService := indexing.NewService(log)
	catalogRepo := catalog.NewRepository(clipsOnlyRepo, clipsRepo, artlistRepo)

	assocService := association.NewService(cfg.Storage.DataDir, "node-scraper", cfg.Paths.PythonScriptsDir, clipsRepo, artlistRepo, clipsOnlyRepo, catalogRepo)

	// Build sync targets centrally
	syncTargets := buildSyncTargets(cfg, clipsOnlyRepo, clipsRepo, artlistRepo)

	catalogSync := catalogsync.NewService(driveUploader, syncTargets, assetIndexService, assetTreeService, clipIndexerService, log)

	// Voiceover sync service
	var voiceoverSync *voiceoversync.Service
	if voFolder := cfg.Drive.VoiceoverFolder(); voFolder != "" && voRepo != nil {
		voiceoverSync = voiceoversync.NewService(driveUploader, voRepo, assetTreeService, voFolder, log)
		log.Info("Voiceover sync service initialized", zap.String("root_folder_id", voFolder))
	}

	// Jobs system
	jobsRepo := jobrepo.NewRepository(dbs.jobs.DB, log)
	jobsDispatcher := jobservice.NewDispatcher()
	if os.Getenv("VELOX_ENABLE_TEST_JOB_HANDLERS") == "true" {
		jobservice.RegisterTestHandlers(jobsDispatcher, log)
	}
	jobsService := jobservice.NewService(jobsRepo, jobsDispatcher, log)

	// Create real-time matching service (needs jobsService, so done after it)
	if cfg.VectorSearch.Enabled && cfg.VectorSearch.RealtimeEnabled && vectorSvc != nil {
		embedder := realtime.NewPythonEmbeddingAdapter(cfg.ClipIndexer.ServerURL)
		jobAdapter := realtime.NewJobServiceAdapter(jobsService, log)
		rerankerClient := reranker.NewClient(reranker.Config{
			Enabled:   cfg.Reranker.Enabled,
			URL:       cfg.Reranker.URL,
			Model:     cfg.Reranker.Model,
			TopK:      cfg.Reranker.TopK,
			TimeoutMs: cfg.Reranker.TimeoutMs,
			Weight:    cfg.Reranker.Weight,
		})
		realtimeSvc = realtime.NewService(vectorSvc, embedder, jobAdapter, rerankerClient, cfg.Reranker, &cfg.VectorSearch, log)
		log.Info("real-time matching service enabled",
			zap.Bool("reranker_enabled", cfg.Reranker.Enabled),
			zap.Int("reranker_top_k", cfg.Reranker.TopK),
			zap.Int("reranker_timeout_ms", cfg.Reranker.TimeoutMs),
		)
	}

	// Register job handlers
	catalogSync.RegisterHandler(jobsService)
	youtubeClipService.RegisterHandler(jobsService)
	voService.RegisterHandler(jobsService)

	scriptFlowHandler := handlers.NewScriptFlowHandler(scriptGen, imageService, realtimeSvc, voService, docClient, jobsService, cfg, log)
	scriptFlowHandler.RegisterJobHandlers(jobsService)

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
	maintenanceSvc := maintenance.NewService(cfg, log, assetIndexService, assetTreeService, deletionSvc, jobsService, dbs.main.DB)
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
		styleRegistry:      styleRegistry,
		vectorSvc:          vectorSvc,
		realtimeSvc:        realtimeSvc,
	}, nil
}