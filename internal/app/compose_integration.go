package app

import (
	"context"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/config"
	"velox/go-master/internal/core/maintenance"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/association"
	"velox/go-master/internal/media/autotag"
	"velox/go-master/internal/media/catalogsync"
	"velox/go-master/internal/media/indexing"
	"velox/go-master/internal/media/lessons"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/media/voiceoversync"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/repository/outbox"
	"velox/go-master/internal/service/gemmamemory"
	"velox/go-master/internal/service/scriptcore"
	"velox/go-master/internal/storage/scheduler"
	"velox/go-master/pkg/concurrent"
)

// composeIntegration initializes cross-domain integration services and builds the final services struct.
func composeIntegration(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	core *CoreInfra,
	mediaDomain *MediaDomain,
	registryWiring *RegistryWiring,
) (*services, error) {
	// ── Asset Resolver, Association, Catalog Sync ──────────────────────
	clipsRepos := map[string]*clips.Repository{
		"youtube": core.ClipsOnlyRepo,
		"stock":   mediaDomain.ClipsRepo,
		"artlist": mediaDomain.ArtlistRepo,
	}
	resolverCfg := &assetindex.ResolverConfig{
		ClipsRepos:    clipsRepos,
		ImageRepo:     mediaDomain.ImageRepo,
		VoiceoverRepo: mediaDomain.VoiceoverRepo,
	}
	assetResolver := assetindex.NewResolver(core.AssetIndexService, resolverCfg, log)
	log.Info("asset resolver initialized")

	indexingService := indexing.NewService(log)
	catalogRepo := catalog.NewRepository(core.ClipsOnlyRepo, mediaDomain.ClipsRepo, mediaDomain.ArtlistRepo)

	assocService := association.NewService(cfg.Storage.DataDir, "node-scraper", cfg.Paths.PythonScriptsDir,
		mediaDomain.ClipsRepo, mediaDomain.ArtlistRepo, core.ClipsOnlyRepo, catalogRepo)
	if core.VectorSvc != nil {
		assocService.SetVectorStore(core.VectorSvc)
		log.Info("vector store wired into association service for hybrid search")
	}

	syncTargets := buildSyncTargets(cfg, core.ClipsOnlyRepo, mediaDomain.ClipsRepo, mediaDomain.ArtlistRepo)
	catalogSync := catalogsync.NewService(core.DriveUploader, syncTargets, core.AssetIndexService, core.AssetTreeService, core.ClipIndexerService, log)

	var voiceoverSync *voiceoversync.Service
	if voFolder := cfg.Drive.VoiceoverFolder(); voFolder != "" && mediaDomain.VoiceoverRepo != nil {
		voiceoverSync = voiceoversync.NewService(core.DriveUploader, mediaDomain.VoiceoverRepo, core.AssetTreeService, voFolder, log)
		log.Info("Voiceover sync service initialized", zap.String("root_folder_id", voFolder))
	}

	// ── Jobs System ────────────────────────────────────────────────────
	jobsRepo := jobrepo.NewRepository(dbs.main.DB, log)
	jobsDispatcher := jobservice.NewDispatcher()
	jobsService := jobservice.NewService(jobsRepo, jobsDispatcher, log)

	// Note: the historical EnableTestJobHandlers config flag and the
	// corresponding jobservice.RegisterTestHandlers import were removed
	// in PR-1.5 (June 2026). The handlers they registered (test.echo,
	// test.slow, test.fail) were useful only for local smoke-testing
	// of the dispatcher and did not exercise the production job
	// lifecycle. Their coverage is now provided by
	// internal/repository/jobs/transition_test.go which verifies the
	// actual optimistic-lock primitive used by all production paths.

	// Register Job Handlers
	catalogSync.RegisterHandler(jobsService)
	catalogSync.RegisterDriveFolderSyncHandler(jobsService)
	mediaDomain.YoutubeClipService.RegisterHandler(jobsService)
	mediaDomain.VoiceoverService.RegisterHandler(jobsService)
	mediaDomain.BooksService.RegisterJobHandler(jobsService)
	core.ClipIndexerService.RegisterJobHandler(jobsService)

	// ── Outbox Repository (PR3-5b.4) ───────────────────────────────────
	// Constructed BEFORE composeRealtimeService so realtime.IndexHealth can
	// report pending_outbox / dead_letter counts. The same instance is reused
	// further down by outbox.NewWorker for idempotent Qdrant indexing.
	outboxRepo := outbox.NewRepository(dbs.main.DB, log)

	// ── Outbox Dispatcher (Task 1 — canonical ingestion entry point) ───
	// Compose a multi-repo ClipsUpserter that routes by clip.Source so a
	// single Dispatcher can drive catalogsync across youtube/stock/artlist.
	// The default repo catches sources not in the map and preserves the
	// prior silent fallback behaviour (those flows were calling
	// repo.UpsertClip directly against a single chosen repo).
	multiClipsUp := outbox.NewMultiClipsUpserter(
		map[string]outbox.ClipsUpserter{
			"youtube": core.ClipsOnlyRepo,
			"stock":   mediaDomain.ClipsRepo,
			"artlist": mediaDomain.ArtlistRepo,
		},
		core.ClipsOnlyRepo, // default fallback for unknown clip.Source
		log,
	)
	outboxTxMgr := outbox.NewManager(dbs.main.DB, log)
	outboxDispatcher := outbox.NewDispatcher(multiClipsUp, outboxRepo, outboxTxMgr, log)
	log.Info("outbox dispatcher instantiated: canonical upsert+outbox-enqueue path")

	// Inject the canonical dispatcher into catalogsync — replaces the
	// `repo.UpsertClip; concurrent.SafeGoFunc(IndexClip)` pattern with an
	// atomic upsert+outbox-enqueue transaction. Subsequent PRs will inject
	// the same dispatcher into YouTube registration, Artlist orchestrator,
	// stock upload and manual upload paths.
	catalogSync.SetDispatcher(outboxDispatcher)
	log.Info("outbox dispatcher wired into catalogsync (SafeGoFunc(IndexClip) removed)")

	// Inject the canonical dispatcher into stockpipeline (Task 5). The
	// stock service was constructed inside WireStockPipeline during
	// WireRegistry (before the dispatcher existed), so the setter is
	// invoked here in a late-binding step. nil registryWiring means the
	// registry was not assembled (test harnesses / partial wiring) and
	// the stock fleet falls back to its SafeGoFunc(IndexClip) legacy path.
	if registryWiring != nil && registryWiring.StockPipeline != nil && registryWiring.StockPipeline.Service != nil {
		registryWiring.StockPipeline.Service.SetDispatcher(outboxDispatcher)
		log.Info("outbox dispatcher wired into stockpipeline (legacy SafeGoFunc(IndexClip) gated)")
	}

	// ── Real-time Matching Service ───────────────────────────────────
	var realtimeSvc *realtime.Service
	if cfg.VectorSearch.Enabled && cfg.VectorSearch.RealtimeEnabled && core.VectorSvc != nil {
		realtimeSvc = composeRealtimeService(ctx, cfg, log, core.VectorSvc, core.ClipsOnlyRepo, outboxRepo, jobsService)
	}

	// ── Books API Handler ──────────────────────────────────────────────
	// Wire voiceover service into books service (for Drive→Book→Voiceover pipeline)
	if mediaDomain.VoiceoverService != nil {
		mediaDomain.BooksService.SetVoiceoverService(mediaDomain.VoiceoverService)
	}

	// ── Gemma Memory & Script Engine ───────────────────────────────────
	memoryRepo := gemmamemory.NewRepository(dbs.main.DB)
	memorySvc := gemmamemory.NewService(memoryRepo, log)
	log.Info("Gemma Memory Gate service initialized")

	engine := scriptcore.NewEngine(core.ScriptGen, memorySvc, mediaDomain.ScriptsRepo, log)
	scriptFlowHandler := handlers.NewScriptFlowHandler(
		core.ScriptGen, engine, mediaDomain.ImageService, realtimeSvc, assocService,
		mediaDomain.VoiceoverService, core.AssetTreeService, core.DocClient, core.DriveUploader,
		jobsService, mediaDomain.ScriptsRepo, memorySvc,
		cfg.Drive.ScriptsGenFolder(), cfg, log,
	)

	// ── ClipSourceBuilder (Clip→Script + Catalog→Script) ───────────────
	// MUST be wired BEFORE RegisterJobHandlers so the job handler
	// method value captures h with clipSourceBuilder already set.
	wireScriptFlowExtras(scriptFlowHandler, core.OllamaClient, core.VectorSvc, core.ClipsOnlyRepo, engine, cfg, log)

	scriptFlowHandler.RegisterJobHandlers(jobsService)

	// ── Auto-Tagging Service ───────────────────────────────────────────
	autotagSvc := autotag.NewService(core.ClipsOnlyRepo, core.VLMClient, log)
	if core.ClipIndexerService != nil {
		autotagSvc.SetVectorStore(core.ClipIndexerService.VectorStore())
	}

	// ── Deletion Service ───────────────────────────────────────────────
	deletionSvc := media.NewDeletionService(
		mediaDomain.ArtlistRepo, core.ClipsOnlyRepo, mediaDomain.ClipsRepo,
		mediaDomain.VoiceoverRepo, mediaDomain.ImageRepo,
		core.DriveUploader, core.AssetTreeService, core.AssetIndexService, log,
	)

	// ── Maintenance Service ────────────────────────────────────────────
	maintenanceSvc := maintenance.NewService(cfg, log,
		core.AssetIndexService, core.AssetTreeService, deletionSvc,
		jobsService, dbs.main.DB)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}

	// ── Lifecycle Scheduler ────────────────────────────────────────────
	lifecycleScheduler := scheduler.NewLifecycleScheduler(cfg, jobsService, log)
	concurrent.SafeGo("lifecycle-scheduler", func() { lifecycleScheduler.Start(ctx) })

	// ── Outbox Worker (transactional outbox for idempotent Qdrant indexing) ──
	// Map cfg.Outbox (yaml + env-overridable) to the WorkerConfig time + int
	// surfaces. Defaults are already populated by applyDefaults + applyEnvVars
	// in internal/config/reflect.go, so a zero value here means the YAML
	// default tag fired. We construct the struct directly to make the
	// mapping self-documenting; NewWorker normalizes any zero that slipped
	// through.
	workerCfg := outbox.WorkerConfig{
		PollInterval:    time.Duration(cfg.Outbox.PollIntervalMs) * time.Millisecond,
		BatchSize:       cfg.Outbox.BatchSize,
		Workers:         cfg.Outbox.Workers,
		ProcessTimeout:  time.Duration(cfg.Outbox.ProcessTimeoutSeconds) * time.Second,
		ReclaimInterval: time.Duration(cfg.Outbox.ReclaimIntervalSeconds) * time.Second,
		StaleThreshold:  time.Duration(cfg.Outbox.StaleThresholdSeconds) * time.Second,
		MaxAttempts:     cfg.Outbox.MaxAttempts,
	}
	outboxWorker := outbox.NewWorker(outboxRepo, func(ctx context.Context, payload *outbox.Payload) error {
		// ProcessFunc: re-index via clipindexer (idempotent — content hash check skips if already done)
		return core.ClipIndexerService.IndexClip(ctx, payload.AssetID)
	}, workerCfg, log)
	concurrent.SafeGo("outbox-worker", func() { outboxWorker.Start(ctx) })
	log.Info("outbox worker pool started for idempotent Qdrant indexing",
		zap.Int("batch_size", workerCfg.BatchSize),
		zap.Int("workers", workerCfg.Workers),
		zap.Duration("poll_interval", workerCfg.PollInterval),
		zap.Duration("reclaim_interval", workerCfg.ReclaimInterval))

	// ── Lessons Service ────────────────────────────────────────────────
	lessonsSvc := lessons.NewService(
		&lessons.LessonsConfig{
			Enabled:             cfg.Lessons.Enabled,
			DefaultModel:        cfg.Lessons.DefaultModel,
			DefaultTone:         cfg.Lessons.DefaultTone,
			DefaultLanguage:     cfg.Lessons.DefaultLanguage,
			DefaultImageModel:   cfg.Lessons.DefaultImageModel,
			MaxParallelChapters: cfg.Lessons.MaxParallelChapters,
			OllamaURL:           cfg.External.OllamaURL,
		},
		core.ScriptGen, mediaDomain.ImageService, core.DocClient, log,
	)
	log.Info("Lessons service initialized", zap.Bool("enabled", cfg.Lessons.Enabled))
	lessonsSvc.RegisterJobHandler(jobsService)

	return &services{
		scriptGen:          core.ScriptGen,
		docClient:          core.DocClient,
		driveUploader:      core.DriveUploader,
		driveClient:        core.DriveClient,
		utility:            common.NewUtilityHandler(),
		scriptsRepo:        mediaDomain.ScriptsRepo,
		imageRepo:          mediaDomain.ImageRepo,
		imageService:       mediaDomain.ImageService,
		stockDriveRepo:     mediaDomain.ClipsRepo,
		artlistRepo:        mediaDomain.ArtlistRepo,
		clipsOnlyRepo:      core.ClipsOnlyRepo,
		monitorsRepo:       mediaDomain.MonitorsRepo,
		voiceoverService:   mediaDomain.VoiceoverService,
		voiceoverSync:      voiceoverSync,
		indexingService:    indexingService,
		clipIndexerService: core.ClipIndexerService,
		catalogRepo:        catalogRepo,
		catalogSync:        catalogSync,
		assocService:       assocService,
		jobsRepo:           jobsRepo,
		jobsService:        jobsService,
		jobsDispatcher:     jobsDispatcher,
		memoryRepo:         memoryRepo,
		mediaProcessor:     core.MediaProcessor,
		ollamaClient:       core.OllamaClient,
		youtubeClipService: mediaDomain.YoutubeClipService,
		assetIndexService:  core.AssetIndexService,
		assetTreeService:   core.AssetTreeService,
		assetResolver:      assetResolver,
		lifecycleScheduler: lifecycleScheduler,
		maintenanceSvc:     maintenanceSvc,
		styleRegistry:      core.StyleRegistry,
		vectorSvc:          core.VectorSvc,
		realtimeSvc:        realtimeSvc,
		vlmClient:          core.VLMClient,
		autotagService:     autotagSvc,
		booksService:       mediaDomain.BooksService,
		lessonsService:     lessonsSvc,
		mediaStore:         core.MediaStore,
		outboxRepo:         outboxRepo,
		outboxDispatcher:   outboxDispatcher,
		outboxWorker:       outboxWorker,
	}, nil
}
