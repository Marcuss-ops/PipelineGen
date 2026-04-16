package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/adapters"
	"velox/go-master/internal/api"
	"velox/go-master/internal/api/handlers"
	artlistpipeline "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/artlistsync"
	"velox/go-master/internal/audio/tts"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipcache"
	"velox/go-master/internal/clipdb"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/download"
	"velox/go-master/internal/downloader"
	"velox/go-master/internal/gpu"
	"velox/go-master/internal/harvester"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/nvidia"
	"velox/go-master/internal/script"
	"velox/go-master/internal/service/asyncpipeline"
	"velox/go-master/internal/service/channelmonitor"
	"velox/go-master/internal/service/pipeline"
	"velox/go-master/internal/service/scriptclips"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/internal/service/stockorchestrator"
	"velox/go-master/internal/stock"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/stockjob"
	"velox/go-master/internal/stocksync"
	"velox/go-master/internal/storage/jsondb"
	"velox/go-master/internal/textgen"
	"velox/go-master/internal/timestamp"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/video"
	"velox/go-master/internal/watcher"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	cfg := config.Get()
	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	log.Info("Starting VeloxEditing Go Master",
		zap.String("version", "1.0.0"),
		zap.Int("port", cfg.Server.Port),
	)

	storage, err := jsondb.NewStorage(cfg.Storage.DataDir)
	if err != nil {
		log.Fatal("Failed to initialize storage", zap.Error(err))
		os.Exit(1)
	}
	defer storage.Close()

	jobService := job.NewService(storage, cfg)
	workerService := worker.NewService(storage, cfg)

	ctx := context.Background()
	if err := jobService.LoadQueue(ctx); err != nil {
		log.Warn("Failed to load job queue", zap.Error(err))
	}
	if err := workerService.LoadWorkers(ctx); err != nil {
		log.Warn("Failed to load workers", zap.Error(err))
	}

	ollamaClient := ollama.NewClient(cfg.External.OllamaURL, "")
	scriptGen := ollama.NewGenerator(ollamaClient)
	edgeTTS := tts.NewEdgeTTS(cfg.GetVoiceoverDir())

	videoProc, err := video.NewProcessor("", cfg.GetVideoWorkDir())
	if err != nil {
		log.Warn("Failed to create video processor", zap.Error(err))
		videoProc = nil
	}

	var youtubeClientV2 youtube.Client
	ytCfg := &youtube.Config{Backend: "ytdlp", YtDlpPath: cfg.Paths.YtDlpPath}
	youtubeClientV2, err = youtube.NewClient("ytdlp", ytCfg)
	if err != nil {
		log.Warn("Failed to create YouTube client v2", zap.Error(err))
	} else {
		log.Info("YouTube client v2 initialized")
	}

	stockMgr, err := stock.NewManager(cfg.GetStockDir(), youtubeClientV2)
	if err != nil {
		log.Warn("Failed to create stock manager", zap.Error(err))
		stockMgr = nil
	}

	extractor := entities.NewOllamaExtractor(ollamaClient)
	segmenter := entities.NewNLPSegmenter()
	entityService := entities.NewEntityService(extractor, segmenter)

	ttsAdapter := tts.NewTTSAdapter(edgeTTS)
	videoAdapter := video.NewVideoProcessorAdapter(videoProc)
	pipelineService := pipeline.NewVideoCreationServiceWithOutputDir(
		scriptGen, entityService, ttsAdapter, videoAdapter, cfg.GetOutputDir(),
	)
	videoDownloader := download.NewDownloader(cfg.GetDownloadDir())

	videoHandler, err := handlers.NewVideoHandler(pipelineService)
	if err != nil {
		log.Fatal("Failed to create video handler", zap.Error(err))
		os.Exit(1)
	}

	clipIndexStore, err := jsondb.NewClipIndexStore(cfg.Storage.DataDir)
	if err != nil {
		log.Warn("Failed to create clip index store", zap.Error(err))
	}
	if clipIndexStore != nil {
		backfilled, err := clipIndexStore.BackfillMediaTypes()
		if err != nil {
			log.Warn("Failed to backfill media_type", zap.Error(err))
		} else if backfilled > 0 {
			log.Info("Media type backfill completed", zap.Int("backfilled", backfilled))
		}
	}

	artlistSrc := initArtlistSource(cfg, log)

	var nvidiaClient *nvidia.Client
	nvCfg := nvidia.DefaultConfig()
	if nvCfg.APIKey != "" {
		nvidiaClient, err = nvidia.NewClient(nvCfg)
		if err != nil {
			log.Warn("Failed to initialize NVIDIA AI client", zap.Error(err))
		} else {
			log.Info("NVIDIA AI client initialized",
				zap.String("model", nvCfg.Model),
				zap.String("base_url", nvCfg.BaseURL),
			)
		}
	}

	gpuCfg := &gpu.GPUConfig{Enabled: true}
	gpuMgr := gpu.NewManager(gpuCfg)
	if err := gpuMgr.Initialize(ctx); err != nil {
		log.Warn("GPU manager initialization failed (continuing without GPU acceleration)", zap.Error(err))
	} else {
		selectedGPU, _ := gpuMgr.GetSelectedGPU()
		if selectedGPU != nil {
			log.Info("GPU manager initialized", zap.String("gpu_name", selectedGPU.Name))
		} else {
			log.Warn("GPU manager initialized but no GPU detected")
		}
	}

	textGenCfg := &textgen.GeneratorConfig{
		DefaultModel: cfg.TextGen.DefaultModel,
		Timeout:      time.Duration(cfg.TextGen.Timeout) * time.Second,
		GPUSupported: true,
	}
	textGen := textgen.NewGenerator(gpuMgr, textGenCfg)
	if textGen != nil {
		log.Info("Text generator initialized")
	}

	var scriptMapper *script.Mapper
	if clipIndexStore != nil {
		clipIndexHandler := handlers.NewClipIndexHandler(
			cfg.GetClipRootFolder(), cfg.GetCredentialsPath(), cfg.GetTokenPath(), clipIndexStore, artlistSrc,
		)
		indexer := clipIndexHandler.GetIndexer()
		if indexer != nil {
			semanticSuggester := clip.NewSemanticSuggester(indexer)
			scriptMapper = script.NewMapper(semanticSuggester, youtubeClientV2, &script.MapperConfig{
				MinScore:             cfg.ClipApproval.MinScore,
				MaxClipsPerScene:     cfg.ClipApproval.MaxClipsPerScene,
				AutoApproveThreshold: cfg.ClipApproval.AutoApproveThreshold,
				EnableYouTube:        youtubeClientV2 != nil,
				EnableArtlist:        artlistSrc != nil,
				RequiresApproval:     true,
			})
		}
	}

	driveHandler := handlers.NewDriveHandler(cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err := driveHandler.InitClient(ctx); err != nil {
		log.Warn("Drive client failed", zap.Error(err))
	}

	clipIndexHandler := handlers.NewClipIndexHandler(
		cfg.GetClipRootFolder(), cfg.GetCredentialsPath(), cfg.GetTokenPath(), clipIndexStore, artlistSrc,
	)
	clipHandler := handlers.NewClipHandler(cfg.GetClipRootFolder(), cfg.GetCredentialsPath(), cfg.GetTokenPath())

	if clipIndexHandler != nil {
		idx := clipIndexHandler.GetIndexer()
		if idx != nil {
			clipHandler.SetIndexer(idx)
			log.Info("Clip indexer wired to ClipHandler", zap.Int("indexed_clips", len(idx.GetIndex().Clips)))
			scanInterval := time.Duration(cfg.ClipIndex.ScanInterval) * time.Second
			scanner := clip.NewIndexScanner(idx, clipIndexStore, scanInterval)
			clipIndexHandler.SetScanner(scanner)
			scannerCtx, scannerCancel := context.WithCancel(context.Background())
			defer scannerCancel()
			go scanner.Start(scannerCtx)
		}
	}

	var clipApprovalHandler *handlers.ClipApprovalHandler

	var youTubeV2Handler *handlers.YouTubeV2Handler
	if youtubeClientV2 != nil {
		youTubeV2Handler = handlers.NewYouTubeV2Handler(youtubeClientV2, gpuMgr, textGen, log)
	}
	gpuTextGenHandler := handlers.NewGPUTextGenHandler(gpuMgr, textGen, log)

	var scriptClipsHandler *handlers.ScriptClipsHandler
	var scriptClipsService *scriptclips.ScriptClipsService
	if driveHandler.GetDriveClient() != nil && stockMgr != nil {
		ollamaC := ollama.NewClient("", "")
		scriptClipsService = scriptclips.NewScriptClipsService(
			scriptGen, entityService, stockMgr, driveHandler.GetDriveClient(),
			cfg.GetDownloadDir(), "", "", 20, ollamaC, true, edgeTTS,
		)
		scriptClipsHandler = handlers.NewScriptClipsHandler(scriptClipsService)
		log.Info("Script+Clips service initialized")
	}

	var asyncPipelineHandler *handlers.AsyncPipelineHandler
	var clipCache *clipcache.ClipCache
	if scriptClipsService != nil {
		var err error
		clipCache, err = clipcache.Open(filepath.Join(cfg.Storage.DataDir, "clip_cache.json"))
		if err != nil {
			log.Warn("Failed to open clip cache, starting fresh", zap.Error(err))
			clipCache, _ = clipcache.Open(filepath.Join(cfg.Storage.DataDir, "clip_cache.json"))
		}
		scriptClipsService.SetClipCache(clipCache)
		asyncPipelineSvc := asyncpipeline.NewAsyncPipelineService(scriptClipsService, cfg.Storage.DataDir)
		asyncPipelineHandler = handlers.NewAsyncPipelineHandler(asyncPipelineSvc)
		go func() {
			ticker := time.NewTicker(time.Duration(cfg.Jobs.CleanupInterval) * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				asyncPipelineSvc.CleanupJobs(time.Duration(cfg.Jobs.AutoCleanupHours) * time.Hour)
				clipCache.Cleanup()
			}
		}()
	}

	var scriptFromClipsHandler *handlers.ScriptFromClipsHandler
	if stockMgr != nil && clipIndexHandler != nil {
		indexer := clipIndexHandler.GetIndexer()
		if indexer != nil {
			svc := scriptclips.NewScriptFromClipsService(scriptGen, entityService, indexer, artlistSrc)
			scriptFromClipsHandler = handlers.NewScriptFromClipsHandler(svc)
			log.Info("Script FROM Clips service initialized", zap.Int("indexed_clips", len(indexer.GetIndex().Clips)))
		}
	}

	var stockOrchestratorHandler *handlers.StockOrchestratorHandler
	if stockMgr != nil && driveHandler.GetDriveClient() != nil {
		svc := stockorchestrator.NewStockOrchestratorService(
			stockMgr, driveHandler.GetDriveClient(), entityService, ollamaClient,
			cfg.GetDownloadDir(), cfg.GetOutputDir(),
		)
		stockOrchestratorHandler = handlers.NewStockOrchestratorHandler(svc)
		log.Info("Stock Orchestrator service initialized")
	}

	// Script Docs — initialize StockDB and wire
	stockDBPaths := []string{
		cfg.Storage.DataDir + "/stock.db.json",
		"src/go-master/data/stock.db.json",
		"data/stock.db.json",
	}
	var stockDB *stockdb.StockDB
	for _, stockDBPath := range stockDBPaths {
		if _, err := os.Stat(stockDBPath); err == nil {
			stockDB, err = stockdb.Open(stockDBPath)
			if err != nil {
				log.Warn("Failed to open StockDB", zap.String("path", stockDBPath), zap.Error(err))
			} else {
				log.Info("StockDB opened", zap.String("path", stockDBPath))
			}
			break
		}
	}

	// ClipDB — separate DB for clips
	var clipDB *clipdb.ClipDB
	clipDBPath := cfg.Storage.DataDir + "/clip_index.json"
	if _, err := os.Stat(clipDBPath); err == nil {
		clipDB, err = clipdb.Open(clipDBPath)
		if err != nil {
			log.Warn("Failed to open ClipDB", zap.Error(err))
		} else {
			log.Info("ClipDB opened", zap.Int("clips", clipDB.GetClipCount()))
		}
	} else {
		clipDB, err = clipdb.Open(clipDBPath)
		if err == nil {
			log.Info("ClipDB created", zap.String("path", clipDBPath))
		}
	}

	// Initialize clip approval handler (needs stockDB)
	if scriptMapper != nil {
		clipApprovalHandler = handlers.NewClipApprovalHandler(scriptMapper, nvidiaClient, stockDB, log)
	}

	var scriptDocsHandler *handlers.ScriptDocsHandler
	var artlistPipelineHandler *artlistpipeline.Handler
	var artlistIdx *scriptdocs.ArtlistIndex
	var artlistDB *artlistdb.ArtlistDB
	var clipSearch *clipsearch.Service

	artlistIndexPath := cfg.Storage.DataDir + "/artlist_stock_index.json"
	if idx, err := scriptdocs.LoadArtlistIndex(artlistIndexPath); err == nil {
		artlistIdx = idx
		log.Info("Artlist index loaded", zap.Int("clips", len(artlistIdx.Clips)))

		// Open local ArtlistDB
		artlistDB, err = artlistdb.Open(cfg.Storage.DataDir + "/artlist_local.db.json")
		if err != nil {
			log.Warn("Failed to open ArtlistDB", zap.Error(err))
		} else {
			log.Info("ArtlistDB opened", zap.String("path", cfg.Storage.DataDir+"/artlist_local.db.json"))
		}

		// Initialize dynamic clip search service
		if driveHandler.GetDriveClient() != nil && stockDB != nil {
			clipSearch = clipsearch.New(
				driveHandler.GetDriveClient(), stockDB, artlistDB,
				cfg.GetDownloadDir(), cfg.Paths.YtDlpPath,
			)
			log.Info("Dynamic clip search service initialized")
		}

		if driveHandler.GetDocClient() != nil {
			svc := scriptdocs.NewScriptDocServiceWithDynamicFolders(
				scriptGen, driveHandler.GetDocClient(),
				driveHandler.GetDriveClient(), cfg.Drive.StockRootFolderID,
				artlistIdx, stockDB, clipSearch, artlistSrc, artlistDB,
			)
			scriptDocsHandler = handlers.NewScriptDocsHandler(svc)
			log.Info("Script Docs service initialized")
		} else if stockDB != nil {
			stockFolders := convertConfigToStockFolders(cfg.Drive.GetStockFolderEntries())
			svc := scriptdocs.NewScriptDocService(
				scriptGen, nil, artlistIdx, stockDB, stockFolders, clipSearch, artlistSrc, artlistDB,
			)
			scriptDocsHandler = handlers.NewScriptDocsHandler(svc)
			log.Info("Script Docs service initialized (StockDB only)")
		}

		// Artlist Pipeline handler
		if artlistDB != nil && artlistSrc != nil {
			ffmpegPath := "ffmpeg"
			if p := os.Getenv("FFMPEG_PATH"); p != "" {
				ffmpegPath = p
			}

			// Initialize clip cache
			clipCachePath := "data/clip_cache.json"
			clipCache, err := clipcache.Open(clipCachePath)
			if err != nil {
				log.Warn("Failed to open clip cache, starting fresh", zap.Error(err))
				clipCache, _ = clipcache.Open(clipCachePath)
			}

			// Initialize keyword pool
			keywordPoolPath := "data/keyword_pool.json"
			keywordPool, err := artlistpipeline.NewKeywordPool(keywordPoolPath)
			if err != nil {
				log.Warn("Failed to initialize keyword pool", zap.Error(err))
				keywordPool, _ = artlistpipeline.NewKeywordPool(keywordPoolPath)
			}

			// Initialize stats store
			statsStorePath := "data/video_stats.json"
			statsStore, err := artlistpipeline.NewStatsStore(statsStorePath)
			if err != nil {
				log.Warn("Failed to initialize stats store", zap.Error(err))
				statsStore, _ = artlistpipeline.NewStatsStore(statsStorePath)
			}

			artlistPipelineHandler = artlistpipeline.New(
				artlistSrc, artlistDB, driveHandler.GetDriveClient(), ollamaClient, clipCache, keywordPool, statsStore,
				cfg.GetDownloadDir(), cfg.Paths.YtDlpPath, ffmpegPath, cfg.GetOutputDir(),
			)
			log.Info("Artlist Pipeline handler initialized")
		}
	}

	var channelMonitorHandler *handlers.ChannelMonitorHandler
	if youtubeClientV2 != nil && driveHandler.GetDriveClient() != nil {
		configPath := "data/channel_monitor_config.json"
		fileCfg, err := channelmonitor.LoadConfigWithDefaults(configPath)
		if err != nil {
			log.Warn("Failed to load channel monitor config", zap.Error(err))
		}
		monitorCfg := channelmonitor.MonitorConfig{
			Channels: fileCfg.Channels, CheckInterval: fileCfg.CheckInterval,
			StockRootID: fileCfg.StockRootID, YtDlpPath: fileCfg.YtDlpPath,
			CookiesPath: fileCfg.CookiesPath, MaxClipDuration: fileCfg.MaxClipDuration,
			OllamaURL: fileCfg.OllamaURL,
		}
		if len(monitorCfg.Channels) == 0 {
			monitorCfg.Channels = []channelmonitor.ChannelConfig{{
				URL: "https://www.youtube.com/@vladtv", Category: "HipHop",
				Keywords: []string{"rapper", "hip hop", "drill", "trap", "interview"},
				MinViews: 10000, MaxClipDuration: 60,
			}}
		}
		if monitorCfg.YtDlpPath == "" {
			monitorCfg.YtDlpPath = cfg.Paths.YtDlpPath
		}
		if monitorCfg.StockRootID == "" {
			monitorCfg.StockRootID = "1ayEZ-CV18xfHQT7RLB4Xgh-TrlkGs-0X"
		}
		if monitorCfg.CheckInterval == 0 {
			monitorCfg.CheckInterval = 24 * time.Hour
		}
		if monitorCfg.MaxClipDuration == 0 {
			monitorCfg.MaxClipDuration = 60
		}
		ollamaURL := "http://localhost:11434"
		if monitorCfg.OllamaURL != "" {
			ollamaURL = monitorCfg.OllamaURL
		}
		monitor := channelmonitor.NewMonitor(
			monitorCfg, youtubeClientV2, driveHandler.GetDriveClient(), ollamaURL,
		)
		monitorCtx, monitorCancel := context.WithCancel(context.Background())
		defer monitorCancel()

		// Start monitor in background — initial scan runs async so server can start immediately
		channelMonitorHandler = handlers.NewChannelMonitorHandler(
			monitor, youtubeClientV2, driveHandler.GetDriveClient(), ollamaURL,
		)
		go func() {
			monitor.Start(monitorCtx)
			log.Info("Channel monitor started (background)", zap.Duration("interval", monitorCfg.CheckInterval))
		}()
		log.Info("Channel monitor starting (non-blocking)", zap.Duration("interval", monitorCfg.CheckInterval))
	}

	// =====================================================
	// DriveSync — Sync Drive folders to StockDB + ClipDB
	// =====================================================
	var driveSync *stocksync.DriveSync
	if stockDB != nil && driveHandler.GetDriveClient() != nil {
		driveClient := driveHandler.GetDriveClient()
		// Use separate DBs for Stock and Clips
		if clipDB != nil {
			driveSync = stocksync.NewDriveSyncWithClips(driveClient, stockDB, clipDB, cfg.Drive.StockRootFolderID)
			log.Info("DriveSync initialized with separate StockDB + ClipDB")
		} else {
			driveSync = stocksync.NewDriveSync(driveClient, stockDB, cfg.Drive.StockRootFolderID)
			log.Info("DriveSync initialized with StockDB only")
		}
		// Start auto-sync immediately (runs in background)
		driveSync.StartAutoSync(context.Background(), time.Duration(cfg.DriveSync.Interval)*time.Second)
		// Run initial sync in background so server can start
		go func() {
			syncCtx, syncTimeoutCancel := context.WithTimeout(context.Background(), time.Duration(cfg.DriveSync.SyncTimeout)*time.Second)
			defer syncTimeoutCancel()
			if err := driveSync.Sync(syncCtx); err != nil {
				log.Warn("Initial DriveSync failed", zap.Error(err))
			} else {
				log.Info("Initial DriveSync completed")
			}
		}()
		log.Info("DriveSync started (background sync)", zap.Duration("interval", time.Duration(cfg.DriveSync.Interval)*time.Second))
	}

	// =====================================================
	// ArtlistSync — Sync Drive/Stock/Artlist → artlist_stock_index.json
	// =====================================================
	var artlistSync *artlistsync.ArtlistSync
	if driveHandler.GetDriveClient() != nil {
		artlistSync = artlistsync.NewArtlistSync(
			driveHandler.GetDriveClient(),
			cfg.Drive.ArtlistFolderID,
			cfg.Storage.DataDir+"/artlist_stock_index.json",
		)
		artlistSync.StartAutoSync(context.Background(), time.Duration(cfg.DriveSync.Interval)*time.Second)
		go func() {
			syncCtx, syncTimeoutCancel := context.WithTimeout(context.Background(), time.Duration(cfg.DriveSync.SyncTimeout)*time.Second)
			defer syncTimeoutCancel()
			if err := artlistSync.Sync(syncCtx); err != nil {
				log.Warn("Initial ArtlistSync failed", zap.Error(err))
			} else {
				log.Info("Initial ArtlistSync completed")
			}
		}()
		log.Info("ArtlistSync started (background sync)", zap.Duration("interval", time.Duration(cfg.DriveSync.Interval)*time.Second))
	}

	// =====================================================
	// Unified Watcher — Replaces stocksync + artlistsync + clip sync
	// =====================================================
	var driveWatcher *watcher.Watcher
	if driveHandler.GetDriveClient() != nil {
		driveWatcher = watcher.NewWatcher(
			driveHandler.GetDriveClient(),
			cfg.Drive.StockRootFolderID,
		)
		driveWatcher.RegisterHandler(watcher.EventFileCreated, func(event watcher.DriveEvent) error {
			log.Debug("Drive file created", zap.String("name", event.Name), zap.String("path", event.Path))
			return nil
		})
		driveWatcher.RegisterHandler(watcher.EventFileDeleted, func(event watcher.DriveEvent) error {
			log.Debug("Drive file deleted", zap.String("name", event.Name), zap.String("path", event.Path))
			return nil
		})
		go func() {
			if err := driveWatcher.Start(context.Background()); err != nil {
				log.Warn("DriveWatcher failed to start", zap.Error(err))
			}
		}()
		log.Info("DriveWatcher started")
	}

	// =====================================================
	// TikTok Client
	// =====================================================
	var tiktokClient downloader.Downloader
	tiktokBackend := downloader.NewTikTokBackend(cfg.Paths.YtDlpPath, "", "")
	if err := tiktokBackend.IsAvailable(context.Background()); err == nil {
		tiktokClient = tiktokBackend
		log.Info("TikTok client initialized")
	} else {
		log.Warn("TikTok client not available", zap.Error(err))
	}

	// =====================================================
	// Stock Job Scheduler — YouTube + TikTok clip discovery
	// =====================================================
	var stockScheduler *stockjob.Scheduler
	if stockDB != nil && youtubeClientV2 != nil {
		clipDB := &mainClipDB{db: stockDB}
		searchQueries := cfg.Scheduler.SearchQueries
		if len(searchQueries) == 0 {
			searchQueries = []string{"interview", "highlights", "documentary", "technology", "business"}
		}
		schedulerConfig := &stockjob.Config{
			Enabled:            true,
			CheckInterval:      time.Duration(cfg.Scheduler.Interval) * time.Second,
			SearchQueries:      searchQueries,
			MaxResultsPerQuery: cfg.Scheduler.MaxResults,
			MinViews:           10000,
			MaxDuration:        time.Duration(cfg.Scheduler.MaxDurationSec) * time.Second,
			MinDuration:        time.Duration(cfg.Scheduler.MinDurationSec) * time.Second,
		}
		stockScheduler = stockjob.NewScheduler(
			schedulerConfig, youtubeClientV2, tiktokClient, clipDB, nil,
		)
		schedCtx := context.Background()
		if err := stockScheduler.Start(schedCtx); err != nil {
			log.Warn("Stock Scheduler failed to start", zap.Error(err))
		} else {
			log.Info("Stock Scheduler started",
				zap.Duration("interval", schedulerConfig.CheckInterval),
				zap.Int("search_queries", len(schedulerConfig.SearchQueries)),
			)
		}
	}

	var harvesterSvc *harvester.Harvester
	if youtubeClientV2 != nil && driveHandler.GetDriveClient() != nil && clipDB != nil {
		ytAdapter := adapters.NewYouTubeSearcherAdapter(youtubeClientV2)
		harvesterConfig := &harvester.Config{
			Enabled:            true,
			CheckInterval:      1 * time.Hour,
			SearchQueries:      []string{"interview", "highlights", "documentary"},
			MaxResultsPerQuery: 20,
			MinViews:           10000,
			Timeframe:          "month",
			MaxConcurrentDls:   3,
			DownloadDir:        cfg.GetDownloadDir(),
			ProcessClips:       true,
			DriveFolderID:      cfg.Drive.StockRootFolderID,
		}
		clipAdapter := adapters.NewClipDBToHarvesterAdapter(clipDB)
		harvesterSvc = harvester.NewHarvester(harvesterConfig, ytAdapter, tiktokClient, driveHandler.GetDriveClient(), clipAdapter)
		log.Info("Harvester initialized")
	}

	deps := &api.RouterDeps{
		VideoProcessor: videoProc, ScriptGen: scriptGen, OllamaClient: ollamaClient,
		EdgeTTS: edgeTTS, StockManager: stockMgr, EntityService: entityService,
		PipelineService: pipelineService, Downloader: videoDownloader,
	}

	handlers_ := &api.Handlers{
		Job:               handlers.NewJobHandler(jobService),
		Worker:            handlers.NewWorkerHandler(workerService),
		Health:            handlers.NewHealthHandler(cfg, jobService, workerService),
		Admin:             handlers.NewAdminHandler(jobService, workerService),
		Video:             videoHandler,
		YouTube:           handlers.NewYouTubeHandler(cfg.GetYouTubeDir()),
		Script:            handlers.NewScriptHandler(scriptGen, ollamaClient),
		Drive:             driveHandler,
		Voiceover:         handlers.NewVoiceoverHandler(edgeTTS),
		NLP:               handlers.NewNLPHandler(ollamaClient, entityService),
		StockProject:      handlers.NewStockProjectHandler(stockMgr),
		StockSearch:       handlers.NewStockSearchHandlerWithDownloadDir(stockMgr, cfg.GetDownloadDir()),
		StockProcess:      handlers.NewStockProcessHandler(stockMgr, cfg.GetVideoStockCreatorBinary(), cfg.GetEffectsDir()),
		Clip:              clipHandler,
		ClipIndex:         clipIndexHandler,
		Dashboard:         handlers.NewDashboardHandler(jobService, workerService),
		Stats:             handlers.NewStatsHandler(jobService, workerService),
		Scraper:           handlers.NewScraperHandler(cfg.Scraper.Dir, cfg.Scraper.NodeBin),
		Download:          handlers.NewDownloadHandler(videoDownloader),
		Timestamp:         handlers.NewTimestampHandler(timestamp.NewService(clipIndexHandler.GetIndexer(), artlistSrc)),
		ClipApproval:      clipApprovalHandler,
		YouTubeV2:         youTubeV2Handler,
		GPUTextGen:        gpuTextGenHandler,
		ScriptClips:       scriptClipsHandler,
		ScriptFromClips:   scriptFromClipsHandler,
		StockOrchestrator: stockOrchestratorHandler,
		ScriptDocs:        scriptDocsHandler,
		ScriptPipeline: handlers.NewScriptPipelineHandler(
			scriptGen,
			func() *drive.DocClient { return driveHandler.GetDocClient() }(),
			stockDB,
			artlistIdx,
			artlistDB,
			artlistSrc,
			driveHandler.GetDriveClient(),
			clipSearch,
			clipIndexHandler.GetIndexer(),
			cfg.Drive.StockRootFolderID,
		),
		ChannelMonitor:  channelMonitorHandler,
		AsyncPipeline:   asyncPipelineHandler,
		ArtlistPipeline: artlistPipelineHandler,
		Harvester:       harvester.NewHandler(harvesterSvc),
	}

	routerDeps := &api.RouterDepsWithHandlers{Handlers: handlers_, Deps: deps}
	server := api.NewServerWithHandlers(cfg, jobService, workerService, routerDeps)

	if err := server.Start(); err != nil {
		log.Fatal("Server error", zap.Error(err))
		os.Exit(1)
	}
}

func initArtlistSource(cfg *config.Config, log *zap.Logger) *clip.ArtlistSource {
	artlistDBPath := cfg.GetArtlistDBPath()
	if artlistDBPath == "" {
		for _, candidate := range []string{"src/node-scraper/artlist_videos.db", "../src/node-scraper/artlist_videos.db"} {
			if _, err := os.Stat(candidate); err == nil {
				artlistDBPath = candidate
				break
			}
		}
	}
	if artlistDBPath == "" {
		log.Info("Artlist DB path not configured")
		return nil
	}
	if _, err := os.Stat(artlistDBPath); err != nil {
		log.Warn("Artlist DB not found", zap.String("path", artlistDBPath))
		return nil
	}
	artlistSrc := clip.NewArtlistSource(artlistDBPath)
	if err := artlistSrc.Connect(); err != nil {
		log.Warn("Failed to connect to Artlist DB", zap.Error(err))
		return nil
	}
	log.Info("Artlist source connected", zap.String("path", artlistDBPath))
	return artlistSrc
}

func convertConfigToStockFolders(entries map[string]config.StockFolderEntry) map[string]scriptdocs.StockFolder {
	result := make(map[string]scriptdocs.StockFolder, len(entries))
	for k, e := range entries {
		result[k] = scriptdocs.StockFolder{ID: e.ID, Name: e.Name, URL: e.URL}
	}
	return result
}

// mainClipDB adapts StockDB to the ClipDatabase interface required by stockjob.Scheduler
type mainClipDB struct {
	db *stockdb.StockDB
}

func (m *mainClipDB) ClipExists(platform downloader.Platform, videoID string) (bool, error) {
	clips, err := m.db.GetAllClips()
	if err != nil {
		return false, err
	}
	for _, clip := range clips {
		if clip.ClipID == videoID {
			return true, nil
		}
	}
	return false, nil
}

func (m *mainClipDB) AddClip(clip *stockjob.ClipRecord) error {
	entry := stockdb.StockClipEntry{
		ClipID:   clip.VideoID,
		FolderID: clip.DriveFolder,
		Filename: clip.Title + ".mp4",
		Source:   string(clip.Platform),
		Tags:     strings.Join(clip.Tags, ","),
		Duration: int(clip.Duration.Seconds()),
	}
	if err := m.db.UpsertClip(entry); err != nil {
		return fmt.Errorf("failed to persist clip to StockDB: %w", err)
	}
	logger.Info("Clip added to StockDB",
		zap.String("id", clip.VideoID),
		zap.String("platform", string(clip.Platform)),
		zap.String("title", clip.Title),
	)
	return nil
}

func (m *mainClipDB) UpdateClip(clip *stockjob.ClipRecord) error {
	entry := stockdb.StockClipEntry{
		ClipID:   clip.VideoID,
		FolderID: clip.DriveFolder,
		Filename: clip.Title + ".mp4",
		Source:   string(clip.Platform),
		Tags:     strings.Join(clip.Tags, ","),
		Duration: int(clip.Duration.Seconds()),
	}
	if err := m.db.UpsertClip(entry); err != nil {
		return fmt.Errorf("failed to update clip in StockDB: %w", err)
	}
	logger.Info("Clip updated in StockDB", zap.String("id", clip.VideoID))
	return nil
}

func (m *mainClipDB) GetClip(platform downloader.Platform, videoID string) (*stockjob.ClipRecord, error) {
	clips, err := m.db.GetAllClips()
	if err != nil {
		return nil, err
	}
	for _, c := range clips {
		if c.ClipID == videoID {
			return &stockjob.ClipRecord{
				ID:       c.ClipID,
				Platform: downloader.Platform(c.Source),
				VideoID:  c.ClipID,
				Title:    c.Filename,
				Tags:     strings.Split(c.Tags, ","),
				Duration: time.Duration(c.Duration) * time.Second,
			}, nil
		}
	}
	return nil, nil
}

func (m *mainClipDB) ListMissingClipsWithMetadata(limit int) ([]stockjob.ClipRecord, error) {
	// Return clips with empty tags or missing metadata
	clips, err := m.db.GetAllClips()
	if err != nil {
		return nil, err
	}
	var missing []stockjob.ClipRecord
	for _, c := range clips {
		if c.Tags == "" || c.Duration == 0 {
			missing = append(missing, stockjob.ClipRecord{
				ID:       c.ClipID,
				Platform: downloader.Platform(c.Source),
				VideoID:  c.ClipID,
				Title:    c.Filename,
				Tags:     strings.Split(c.Tags, ","),
				Duration: time.Duration(c.Duration) * time.Second,
			})
			if len(missing) >= limit {
				break
			}
		}
	}
	return missing, nil
}
