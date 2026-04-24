package bootstrap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/api"
	"velox/go-master/internal/api/handlers/catalog"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/nlp"
	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/api/handlers/stock"
	"velox/go-master/internal/api/handlers/video"
	"velox/go-master/internal/api/handlers/youtube"
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/service/stockorchestrator"
	"velox/go-master/internal/timestamp"
	driveupload "velox/go-master/internal/upload/drive"
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

// WireServices is the Modular Composition Root. It initializes all services
// via scoped init functions and assembles the final handler graph.
//
// Background services (maintenance, scanners, sync, monitors, harvesters)
// are collected into a ServiceGroup for unified lifecycle management.
// They are NOT started here — the caller starts them via ServiceGroup.Start().
func WireServices(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
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
	syncDeps, syncBgSvcs, err := initSyncServices(cfg, log, clipDeps, driveDeps)
	if err != nil {
		runCleanups(cleanups)
		return nil, err
	}
	for _, svc := range syncBgSvcs {
		sg.Add(svc)
	}

	// Trigger explicit sync after dynamic clip cycles that upload new clips.
	if pipelineDeps.ClipSearch != nil && syncDeps != nil {
		pipelineDeps.ClipSearch.SetPostCycleSync(func(ctx context.Context) error {
			syncTimeout := time.Duration(cfg.DriveSync.SyncTimeout) * time.Second
			if syncTimeout <= 0 {
				syncTimeout = 2 * time.Minute
			}
			syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
			defer cancel()

			var syncErr error
			if syncDeps.DriveSync != nil {
				if err := syncDeps.DriveSync.Sync(syncCtx); err != nil {
					if isAlreadyRunningSyncErr(err) {
						log.Info("Post-cycle DriveSync skipped (already running)")
					} else {
						log.Warn("Post-cycle DriveSync failed", zap.Error(err))
						syncErr = err
					}
				} else {
					log.Info("Post-cycle DriveSync completed")
				}
			}
			if syncDeps.ArtlistSync != nil {
				if err := syncDeps.ArtlistSync.Sync(syncCtx); err != nil {
					if isAlreadyRunningSyncErr(err) {
						log.Info("Post-cycle ArtlistSync skipped (already running)")
					} else {
						log.Warn("Post-cycle ArtlistSync failed", zap.Error(err))
						if syncErr == nil {
							syncErr = err
						}
					}
				} else {
					log.Info("Post-cycle ArtlistSync completed")
				}
			}
			if syncDeps.CatalogSync != nil {
				if err := syncDeps.CatalogSync.Sync(syncCtx); err != nil {
					if isAlreadyRunningSyncErr(err) {
						log.Info("Post-cycle CatalogSync skipped (already running)")
					} else {
						log.Warn("Post-cycle CatalogSync failed", zap.Error(err))
						if syncErr == nil {
							syncErr = err
						}
					}
				} else {
					log.Info("Post-cycle CatalogSync completed")
				}
			}
			if syncErr != nil {
				return fmt.Errorf("one or more post-cycle sync tasks failed: %w", syncErr)
			}
			return nil
		})
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
	var stockOrchestratorHandler *stock.StockOrchestratorHandler
	if coreDeps.StockMgr != nil && driveDeps.DriveHandler.GetDriveClient() != nil {
		svc := stockorchestrator.NewStockOrchestratorService(
			coreDeps.StockMgr, driveDeps.DriveHandler.GetDriveClient(), coreDeps.EntityService, coreDeps.OllamaClient,
			cfg.GetDownloadDir(), cfg.GetOutputDir(),
		)
		stockOrchestratorHandler = stock.NewStockOrchestratorHandler(svc)
		log.Info("Stock Orchestrator service initialized")
	}

	// === YouTube V2 & GPU handlers ===
	var youTubeV2Handler *youtube.YouTubeV2Handler
	if coreDeps.YouTubeClientV2 != nil {
		youTubeV2Handler = youtube.NewYouTubeV2Handler(coreDeps.YouTubeClientV2, coreDeps.GpuMgr, coreDeps.TextGen, log)
	}
	gpuTextGenHandler := nlp.NewGPUTextGenHandler(coreDeps.GpuMgr, coreDeps.TextGen, log)

	// === Video handler ===
	videoHandler, err := video.NewVideoHandler(coreDeps.PipelineService)
	if err != nil {
		runCleanups(cleanups)
		return nil, err
	}

	var catalogHandler *catalog.CatalogHandler
	if clipDeps.CatalogDB != nil {
		catalogHandler = catalog.NewCatalogHandler(clipDeps.CatalogDB)
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
	if clipDeps.ClipHandler != nil {
		clipDeps.ClipHandler.SetClipSearch(pipelineDeps.ClipSearch)
		clipDeps.ClipHandler.SetStockDB(clipDeps.StockDB)
	}
	allHandlers := &api.Handlers{
		Health:       common.NewHealthHandler(cfg, coreDeps.JobService, coreDeps.WorkerService),
		Video:        videoHandler,
		YouTube:      youtube.NewYouTubeHandler(cfg.GetYouTubeDir()),
		Drive:        driveDeps.DriveHandler,
		Voiceover:    common.NewVoiceoverHandler(coreDeps.EdgeTTS),
		NLP:          nlp.NewNLPHandler(coreDeps.OllamaClient, coreDeps.EntityService),
		StockProject: stock.NewStockProjectHandler(coreDeps.StockMgr),
		StockSearch:  stock.NewStockSearchHandlerWithDownloadDir(coreDeps.StockMgr, cfg.GetDownloadDir()),
		StockProcess: stock.NewStockProcessHandler(coreDeps.StockMgr, cfg.GetVideoStockCreatorBinary(), cfg.GetEffectsDir()),
		Clip:         clipDeps.ClipHandler,
		ClipIndex:    clipDeps.ClipIndexHandler,
		Catalog:      catalogHandler,
		Download:     video.NewDownloadHandler(coreDeps.Downloader),
		Timestamp:    common.NewTimestampHandler(timestamp.NewService(clipDeps.ClipIndexHandler.GetIndexer(), clipDeps.ArtlistSrc)),
		ClipApproval: pipelineDeps.ClipApprovalHandler,

		YouTubeV2:         youTubeV2Handler,
		GPUTextGen:        gpuTextGenHandler,
		ScriptClips:       pipelineDeps.ScriptClipsHandler,
		ScriptFromClips:   pipelineDeps.ScriptFromClipsHandler,
		StockOrchestrator: stockOrchestratorHandler,
		ScriptDocs:        pipelineDeps.ScriptDocsHandler,
		ScriptPipeline: script.NewScriptPipelineHandler(
			coreDeps.ScriptGen,
			coreDeps.EntityService,
			func() *driveupload.DocClient { return driveDeps.DriveHandler.GetDocClient() }(),
			clipDeps.StockDB,
			pipelineDeps.ArtlistIdx,
			pipelineDeps.ArtlistDB,
			clipDeps.ArtlistSrc,
			clipDeps.ClipDB,
			driveDeps.DriveHandler.GetDriveClient(),
			pipelineDeps.ClipSearch,
			clipDeps.ClipIndexHandler.GetIndexer(),
			cfg.Drive.StockRootFolderID,
			cfg.Drive.ArtlistFolderID,
		),
		ChannelMonitor:  bgDeps.ChannelMonitorHandler,
		ArtlistPipeline: pipelineDeps.ArtlistPipelineHandler,
		Harvester:       bgDeps.HarvesterHandler,
		CatalogSQLite:   clipDeps.CatalogSQLiteHandler,
		Utility:         coreDeps.Utility,
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

// WireScriptDocs initializes only Ollama, Entities, and Drive (for script docs workflow).
func WireScriptDocs(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	// 1. Core Minimal (Ollama, Entities)
	coreDeps, coreClean, err := initCoreMinimal(cfg, log)
	if err != nil {
		return nil, err
	}

	// 2. Drive handler (Necessario per caricare su Google Docs)
	driveDeps, driveClean, err := initDrive(cfg, log)
	if err != nil {
		coreClean()
		return nil, err
	}

	// 3. Script pipeline essentials
	allHandlers := &api.Handlers{
		ScriptPipeline: script.NewScriptPipelineHandler(
			coreDeps.ScriptGen,
			coreDeps.EntityService,
			driveDeps.DriveHandler.GetDocClient(),
			nil, nil, nil, nil, nil, 
			driveDeps.DriveHandler.GetDriveClient(),
			nil, nil, "", "",
		),
		Drive: driveDeps.DriveHandler,
		NLP:   nlp.NewNLPHandler(coreDeps.OllamaClient, coreDeps.EntityService),
	}

	deps := &api.RouterDepsWithHandlers{
		Handlers: allHandlers,
		Deps: &api.RouterDeps{
			ScriptGen:     coreDeps.ScriptGen,
			OllamaClient:  coreDeps.OllamaClient,
			EntityService: coreDeps.EntityService,
		},
	}

	cleanup := func() {
		if driveClean != nil {
			driveClean()
		}
		if coreClean != nil {
			coreClean()
		}
	}

	return &AppDeps{
		RouterDeps: deps,
		Cleanup:    cleanup,
	}, nil
}
// WireMinimal initializes only the bare essentials for text generation.
func WireMinimal(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	coreDeps, coreClean, err := initCoreMinimal(cfg, log)
	if err != nil {
		return nil, err
	}

	// === Assemble Handlers ===
	allHandlers := &api.Handlers{
		ScriptPipeline: script.NewScriptPipelineHandler(
			coreDeps.ScriptGen,
			coreDeps.EntityService,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "",
		),
		NLP: nlp.NewNLPHandler(coreDeps.OllamaClient, coreDeps.EntityService),
	}

	deps := &api.RouterDeps{
		ScriptGen:    coreDeps.ScriptGen,
		OllamaClient: coreDeps.OllamaClient,
	}

	routerDeps := &api.RouterDepsWithHandlers{Handlers: allHandlers, Deps: deps}

	return &AppDeps{
		RouterDeps: routerDeps,
		Cleanup:    coreClean,
	}, nil
}

func runCleanups(cleanups []CleanupFunc) {
	for i := len(cleanups) - 1; i >= 0; i-- {
		if cleanups[i] != nil {
			cleanups[i]()
		}
	}
}

func isAlreadyRunningSyncErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already running")
}
