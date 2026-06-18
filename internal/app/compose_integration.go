package app

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/common"
	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/script/handlers"
	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/association"
	"github.com/Marcuss-ops/PipelineGen/internal/media/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/indexing"
	"github.com/Marcuss-ops/PipelineGen/internal/media/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/media/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	"github.com/Marcuss-ops/PipelineGen/internal/outboxhandlers"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/assetlocations"
	assetprocessing "github.com/Marcuss-ops/PipelineGen/internal/repository/assetprocessing"
	assetrelations "github.com/Marcuss-ops/PipelineGen/internal/repository/assetrelations"
	assettags "github.com/Marcuss-ops/PipelineGen/internal/repository/assettags"
	assetversions "github.com/Marcuss-ops/PipelineGen/internal/repository/assetversions"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	jobrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/service/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/service/scriptcore"
	"github.com/Marcuss-ops/PipelineGen/internal/storage/scheduler"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
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
		"stock":   core.ClipsOnlyRepo,
		"artlist": core.ClipsOnlyRepo,
	}
	resolverCfg := &assetindex.ResolverConfig{
		ClipsRepos:    clipsRepos,
		ImageRepo:     mediaDomain.ImageRepo,
		VoiceoverRepo: mediaDomain.VoiceoverRepo,
	}
	assetResolver := assetindex.NewResolver(core.AssetIndexService, resolverCfg, log)
	log.Info("asset resolver initialized")

	indexingService := indexing.NewService(log)
	catalogRepo := catalog.NewRepository(core.ClipsOnlyRepo, core.ClipsOnlyRepo, core.ClipsOnlyRepo)

	assocService := association.NewService(cfg.Storage.DataDir, "node-scraper", cfg.Paths.PythonScriptsDir,
		core.ClipsOnlyRepo, core.ClipsOnlyRepo, core.ClipsOnlyRepo, catalogRepo)
	if core.VectorSvc != nil {
		assocService.SetVectorStore(core.VectorSvc)
		log.Info("vector store wired into association service for hybrid search")
	}

	syncTargets := buildSyncTargets(cfg, core.ClipsOnlyRepo, core.ClipsOnlyRepo, core.ClipsOnlyRepo)
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

	// Register Job Handlers
	catalogSync.RegisterHandler(jobsService)
	catalogSync.RegisterDriveFolderSyncHandler(jobsService)
	mediaDomain.YoutubeClipService.RegisterHandler(jobsService)
	mediaDomain.VoiceoverService.RegisterHandler(jobsService)
	mediaDomain.BooksService.RegisterJobHandler(jobsService)
	core.ClipIndexerService.RegisterJobHandler(jobsService)

	// ── Outbox Events Repository (PR5) ─────────────────────────────────
	// The canonical outbox_events table is the single source of truth for
	// asynchronous event dispatch (indexing, delivery, metadata, provider
	// sync, workflow steps). Constructed BEFORE composeRealtimeService so
	// IndexHealth can report pending/dead_letter counts from outbox_events.
	outboxEventsRepo := outboxevents.NewRepository(dbs.main.DB)

	// ── Outbox Events Dispatcher (canonical ingestion entry point) ─────
	// The outbox.Dispatcher now enqueues to outbox_events (not
	// media_index_outbox). MultiClipsUpserter routes clip.Source to the
	// appropriate repository.
	multiClipsUp := outbox.NewMultiClipsUpserter(
		map[string]outbox.ClipsUpserter{
			"youtube": core.ClipsOnlyRepo,
			"stock":   core.ClipsOnlyRepo,
			"artlist": core.ClipsOnlyRepo,
		},
		core.ClipsOnlyRepo, // default fallback for unknown clip.Source
		log,
	)
	outboxTxMgr := outbox.NewManager(dbs.main.DB, log)
	outboxDispatcher := outbox.NewDispatcher(multiClipsUp, outboxEventsRepo, outboxTxMgr, log)
	log.Info("outbox dispatcher instantiated: canonical upsert+outbox_events enqueue path")

	// Inject the canonical dispatcher into catalogsync — replaces the
	// `repo.UpsertClip; concurrent.SafeGoFunc(IndexClip)` pattern with an
	// atomic upsert+outbox_events enqueue transaction.
	catalogSync.SetDispatcher(outboxDispatcher)
	log.Info("outbox dispatcher wired into catalogsync")

	// Inject the canonical dispatcher into stockpipeline. The stock
	// service was constructed inside WireStockPipeline during
	// WireRegistry (before the dispatcher existed), so the setter is
	// invoked here in a late-binding step.
	if registryWiring != nil && registryWiring.StockPipeline != nil && registryWiring.StockPipeline.Service != nil {
		registryWiring.StockPipeline.Service.SetDispatcher(outboxDispatcher)
		log.Info("outbox dispatcher wired into stockpipeline (legacy SafeGoFunc(IndexClip) gated)")
	}

	// ── Real-time Matching Service ───────────────────────────────────
	var realtimeSvc *realtime.Service
	if cfg.VectorSearch.Enabled && cfg.VectorSearch.RealtimeEnabled && core.VectorSvc != nil {
		realtimeSvc = composeRealtimeService(ctx, cfg, log, core.VectorSvc, core.ClipsOnlyRepo, outboxEventsRepo, jobsService)
	}

	// ── Books API Handler ──────────────────────────────────────────────
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
	wireScriptFlowExtras(scriptFlowHandler, core.OllamaClient, core.VectorSvc, core.ClipsOnlyRepo, engine, cfg, log)
	scriptFlowHandler.RegisterJobHandlers(jobsService)

	// ── Auto-Tagging Service ───────────────────────────────────────────
	autotagSvc := autotag.NewService(core.ClipsOnlyRepo, core.VLMClient, log)
	if core.ClipIndexerService != nil {
		autotagSvc.SetVectorStore(core.ClipIndexerService.VectorStore())
	}

	// ── Deletion Service ───────────────────────────────────────────────
	deletionSvc := media.NewDeletionService(
		core.ClipsOnlyRepo, core.ClipsOnlyRepo, core.ClipsOnlyRepo,
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

	// ── Outbox Events Pool (PR5 — canonical outbox for async events) ───
	// The outbox_events Pool replaces the legacy media_index_outbox Worker.
	// It uses CTE-based atomic claim + lease fencing + retry/dead-letter.
	// The handler registry includes:
	//   - workflow.step.completed (audit log)
	//   - workflow.step.failed    (ERROR audit log + hookFn for alerting)
	//   - asset.index.requested   (real — calls clipIndexer.IndexClip)
	//   - delivery / metadata_export / provider_sync (stubs — return errors
	//     so events retry until dead_letter for operator visibility)
	outboxEventsRegistry := outboxevents.NewHandlerRegistry()
	if err := outboxhandlers.RegisterAll(outboxEventsRegistry, log, core.ClipIndexerService); err != nil {
		log.Warn("failed to register outbox events handlers", zap.Error(err))
	}
	cfgPoll := 500 * time.Millisecond
	if cfg.Outbox.PollIntervalMs > 0 {
		cfgPoll = time.Duration(cfg.Outbox.PollIntervalMs) * time.Millisecond
	}
	cfgReclaim := 60 * time.Second
	if cfg.Outbox.ReclaimIntervalSeconds > 0 {
		cfgReclaim = time.Duration(cfg.Outbox.ReclaimIntervalSeconds) * time.Second
	}
	cfgProcess := 30 * time.Second
	if cfg.Outbox.ProcessTimeoutSeconds > 0 {
		cfgProcess = time.Duration(cfg.Outbox.ProcessTimeoutSeconds) * time.Second
	}
	outboxEventsCfg := outboxevents.WorkerPollConfig{
		PollInterval:    cfgPoll,
		ProcessTimeout:  cfgProcess,
		ReclaimInterval: cfgReclaim,
	}
	outboxEventsPool := outboxevents.NewPool("outbox-events", outboxEventsRepo, outboxEventsRegistry, log, outboxEventsCfg)
	concurrent.SafeGo("outbox-events-pool", func() {
		outboxEventsPool.Start(ctx, 1)
	})
	concurrent.SafeGo("outbox-events-shutdown", func() {
		<-ctx.Done()
		if err := outboxEventsPool.Stop(15 * time.Second); err != nil {
			log.Warn("outbox events pool stop returned error", zap.Error(err))
		}
	})
	log.Info("outbox events pool started for workflow.step.* + asset.index.requested + stubs",
		zap.Duration("poll_interval", outboxEventsCfg.PollInterval),
		zap.Duration("process_timeout", outboxEventsCfg.ProcessTimeout))

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

	// ── Asset Satellite Repositories (canonical model completion, PR0) ────
	assetLocRepo := assetlocations.NewRepository(dbs.main.DB)
	assetProcRepo := assetprocessing.NewAdapter(assetprocessing.NewRepository(dbs.main.DB))
	assetRelRepo := assetrelations.NewRepository(dbs.main.DB)
	assetTagRepo := assettags.NewRepository(dbs.main.DB)
	assetVerRepo := assetversions.NewRepository(dbs.main.DB)

	// Wire asset lifecycle repos into YouTube service (late-binding).
	if mediaDomain.YoutubeClipService != nil {
		mediaDomain.YoutubeClipService.SetAssetRepos(assetProcRepo, assetVerRepo)
		log.Debug("asset lifecycle repos wired into youtube service")
	}

	return &services{
		scriptGen:          core.ScriptGen,
		docClient:          core.DocClient,
		driveUploader:      core.DriveUploader,
		driveClient:        core.DriveClient,
		driveDests:         core.DriveDests,
		utility:            common.NewUtilityHandler(),
		scriptsRepo:        mediaDomain.ScriptsRepo,
		imageRepo:          mediaDomain.ImageRepo,
		imageService:       mediaDomain.ImageService,
		stockDriveRepo:     mediaDomain.ClipsRepo,
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

		outboxDispatcher: outboxDispatcher,

		outboxEventsRepo:     outboxEventsRepo,
		outboxEventsPool:     outboxEventsPool,
		outboxEventsRegistry: outboxEventsRegistry,

		assetLocationsRepo:  assetLocRepo,
		assetProcessingRepo: assetProcRepo,
		assetRelationsRepo:  assetRelRepo,
		assetTagsRepo:       assetTagRepo,
		assetVersionsRepo:   assetVerRepo,
	}, nil
}
