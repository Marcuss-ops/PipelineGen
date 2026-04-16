package main

import (
	"go.uber.org/zap"
	"velox/go-master/internal/api"
	"velox/go-master/internal/api/handlers"
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/service/stockorchestrator"
	"velox/go-master/internal/timestamp"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
)

// AppDeps holds all initialized dependencies for the server.
type AppDeps struct {
	RouterDeps    *api.RouterDepsWithHandlers
	JobService    *job.Service
	WorkerService *worker.Service
	ServiceGroup  *runtime.ServiceGroup
	Cleanup       func()
}

// wireServices is the Modular Composition Root. It initializes all services
// via scoped init functions and assembles the final handler graph.
//
// Background services (maintenance, scanners, sync, monitors, harvesters)
// are collected into a ServiceGroup for unified lifecycle management.
// They are NOT started here — the caller starts them via ServiceGroup.Start().
func wireServices(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	sg := runtime.NewServiceGroup(log)
	var cleanups []CleanupFunc

	// 1. Core infrastructure (storage, AI, video, YouTube, stock, downloaders, maintenance)
	coreDeps, coreBgSvcs, coreClean, err := initCore(cfg, log)
	if err != nil {
		runCleanups(cleanups)
		return nil, err
	}
	cleanups = append(cleanups, coreClean)
	for _, svc := range coreBgSvcs {
		sg.Add(svc)
	}

	// 2. Clip ecosystem (indexing, databases, mapper, scanner)
	clipDeps, clipBgSvcs, err := initClipSystem(cfg, log, coreDeps)
	if err != nil {
		runCleanups(cleanups)
		return nil, err
	}
	if clipDeps.CatalogDB != nil {
		cleanups = append(cleanups, func() {
			_ = clipDeps.CatalogDB.Close()
		})
	}
	for _, svc := range clipBgSvcs {
		sg.Add(svc)
	}

	// 3. Drive handler (OAuth client for Drive + Docs)
	driveDeps, driveClean, err := initDrive(cfg, log)
	if err != nil {
		runCleanups(cleanups)
		return nil, err
	}
	cleanups = append(cleanups, driveClean)

	// 4. Script pipelines (script+clips, async, approval, docs, artlist)
	pipelineDeps, pipelineBgSvcs, pipelineClean, err := initPipeline(cfg, log, coreDeps, clipDeps, driveDeps)
	if err != nil {
		runCleanups(cleanups)
		return nil, err
	}
	cleanups = append(cleanups, pipelineClean)
	for _, svc := range pipelineBgSvcs {
		sg.Add(svc)
	}

	// 5. Sync services (Watcher = Drive polling authority → drives DriveSync + ArtlistSync)
	_, syncBgSvcs, err := initSyncServices(cfg, log, clipDeps, driveDeps)
	if err != nil {
		runCleanups(cleanups)
		return nil, err
	}
	for _, svc := range syncBgSvcs {
		sg.Add(svc)
	}

	// 6. Background services (channel monitor, stock scheduler, harvester)
	bgDeps, bgBgSvcs, err := initBackgroundServices(cfg, log, coreDeps, clipDeps, driveDeps)
	if err != nil {
		runCleanups(cleanups)
		return nil, err
	}
	for _, svc := range bgBgSvcs {
		sg.Add(svc)
	}

	// === Stock Orchestrator ===
	var stockOrchestratorHandler *handlers.StockOrchestratorHandler
	if coreDeps.StockMgr != nil && driveDeps.DriveHandler.GetDriveClient() != nil {
		svc := stockorchestrator.NewStockOrchestratorService(
			coreDeps.StockMgr, driveDeps.DriveHandler.GetDriveClient(), coreDeps.EntityService, coreDeps.OllamaClient,
			cfg.GetDownloadDir(), cfg.GetOutputDir(),
		)
		stockOrchestratorHandler = handlers.NewStockOrchestratorHandler(svc)
		log.Info("Stock Orchestrator service initialized")
	}

	// === YouTube V2 & GPU handlers ===
	var youTubeV2Handler *handlers.YouTubeV2Handler
	if coreDeps.YouTubeClientV2 != nil {
		youTubeV2Handler = handlers.NewYouTubeV2Handler(coreDeps.YouTubeClientV2, coreDeps.GpuMgr, coreDeps.TextGen, log)
	}
	gpuTextGenHandler := handlers.NewGPUTextGenHandler(coreDeps.GpuMgr, coreDeps.TextGen, log)

	// === Video handler ===
	videoHandler, err := handlers.NewVideoHandler(coreDeps.PipelineService)
	if err != nil {
		runCleanups(cleanups)
		return nil, err
	}

	// === Assemble RouterDeps ===
	deps := &api.RouterDeps{
		VideoProcessor:  coreDeps.VideoProc,
		ScriptGen:       coreDeps.ScriptGen,
		OllamaClient:    coreDeps.OllamaClient,
		EdgeTTS:         coreDeps.EdgeTTS,
		StockManager:    coreDeps.StockMgr,
		EntityService:   coreDeps.EntityService,
		PipelineService: coreDeps.PipelineService,
		Downloader:      coreDeps.Downloader,
	}

	// === Assemble Handlers ===
	allHandlers := &api.Handlers{
		Job:          handlers.NewJobHandler(coreDeps.JobService),
		Worker:       handlers.NewWorkerHandler(coreDeps.WorkerService),
		Health:       handlers.NewHealthHandler(cfg, coreDeps.JobService, coreDeps.WorkerService),
		Admin:        handlers.NewAdminHandler(coreDeps.JobService, coreDeps.WorkerService),
		Video:        videoHandler,
		YouTube:      handlers.NewYouTubeHandler(cfg.GetYouTubeDir()),
		Script:       handlers.NewScriptHandler(coreDeps.ScriptGen, coreDeps.OllamaClient),
		Drive:        driveDeps.DriveHandler,
		Voiceover:    handlers.NewVoiceoverHandler(coreDeps.EdgeTTS),
		NLP:          handlers.NewNLPHandler(coreDeps.OllamaClient, coreDeps.EntityService),
		StockProject: handlers.NewStockProjectHandler(coreDeps.StockMgr),
		StockSearch:  handlers.NewStockSearchHandlerWithDownloadDir(coreDeps.StockMgr, cfg.GetDownloadDir()),
		StockProcess: handlers.NewStockProcessHandler(coreDeps.StockMgr, cfg.GetVideoStockCreatorBinary(), cfg.GetEffectsDir()),
		Clip:         clipDeps.ClipHandler,
		ClipIndex:    clipDeps.ClipIndexHandler,
		Dashboard:    handlers.NewDashboardHandler(coreDeps.JobService, coreDeps.WorkerService),
		Stats:        handlers.NewStatsHandler(coreDeps.JobService, coreDeps.WorkerService),
		Scraper:      handlers.NewScraperHandler(cfg.Scraper.Dir, cfg.Scraper.NodeBin),
		Download:     handlers.NewDownloadHandler(coreDeps.Downloader),
		Timestamp:    handlers.NewTimestampHandler(timestamp.NewService(clipDeps.ClipIndexHandler.GetIndexer(), clipDeps.ArtlistSrc)),
		ClipApproval: pipelineDeps.ClipApprovalHandler,

		YouTubeV2:         youTubeV2Handler,
		GPUTextGen:        gpuTextGenHandler,
		ScriptClips:       pipelineDeps.ScriptClipsHandler,
		ScriptFromClips:   pipelineDeps.ScriptFromClipsHandler,
		StockOrchestrator: stockOrchestratorHandler,
		ScriptDocs:        pipelineDeps.ScriptDocsHandler,
		ScriptPipeline: handlers.NewScriptPipelineHandler(
			coreDeps.ScriptGen,
			func() *drive.DocClient { return driveDeps.DriveHandler.GetDocClient() }(),
			clipDeps.StockDB,
			pipelineDeps.ArtlistIdx,
			pipelineDeps.ArtlistDB,
			clipDeps.ArtlistSrc,
			driveDeps.DriveHandler.GetDriveClient(),
			pipelineDeps.ClipSearch,
			clipDeps.ClipIndexHandler.GetIndexer(),
			cfg.Drive.StockRootFolderID,
			cfg.Drive.ArtlistFolderID,
		),
		ChannelMonitor:  bgDeps.ChannelMonitorHandler,
		AsyncPipeline:   pipelineDeps.AsyncPipelineHandler,
		ArtlistPipeline: pipelineDeps.ArtlistPipelineHandler,
		Harvester:       bgDeps.HarvesterHandler,
	}

	routerDeps := &api.RouterDepsWithHandlers{Handlers: allHandlers, Deps: deps}

	// === Aggregated Cleanup ===
	baseCleanup := func() {
		_ = sg.Stop()
		if pipelineDeps.ClipCache != nil {
			_ = pipelineDeps.ClipCache.Save()
		}
		runCleanups(cleanups)
	}

	return &AppDeps{
		RouterDeps:    routerDeps,
		JobService:    coreDeps.JobService,
		WorkerService: coreDeps.WorkerService,
		ServiceGroup:  sg,
		Cleanup:       baseCleanup,
	}, nil
}

func runCleanups(cleanups []CleanupFunc) {
	for i := len(cleanups) - 1; i >= 0; i-- {
		if cleanups[i] != nil {
			cleanups[i]()
		}
	}
}
