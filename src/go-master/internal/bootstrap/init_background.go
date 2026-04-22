package bootstrap

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/adapters"
	"velox/go-master/internal/api/handlers/monitoring"
	"velox/go-master/internal/harvester"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/service/channelmonitor"
	"velox/go-master/internal/stockjob"
	"velox/go-master/pkg/config"

	// New pipeline imports
	"velox/go-master/internal/pipeline/analyzer"
	"velox/go-master/internal/pipeline/coordinator"
	"velox/go-master/internal/pipeline/downloader"
	"velox/go-master/internal/pipeline/fetcher"
	"velox/go-master/internal/pipeline/store"
)

// BackgroundDeps holds the background service handlers and the services themselves.
type BackgroundDeps struct {
	ChannelMonitorHandler *monitoring.ChannelMonitorHandler
	StockScheduler        *stockjob.Scheduler
	HarvesterSvc          *harvester.Harvester
	HarvesterHandler      *harvester.Handler
	PipelineEngine        *coordinator.Engine
}

// initBackgroundServices initializes the long-running background services:
// channel monitor, stock job scheduler, and YouTube harvester.
func initBackgroundServices(
	cfg *config.Config, log *zap.Logger, core *CoreDeps, clips *ClipDeps, drive *DriveDeps,
) (*BackgroundDeps, []runtime.BackgroundService, error) {
	driveClient := drive.DriveHandler.GetDriveClient()
	var services []runtime.BackgroundService

	// === New Modular Pipeline Engine ===
	pipelineStore, err := store.NewPipelineStore(cfg.GetDataPath("pipeline.db"))
	if err != nil {
		log.Error("Failed to initialize pipeline store", zap.Error(err))
		return nil, nil, err
	}

	pipelineFetcher := fetcher.NewYtDlpFetcher(cfg.Paths.YtDlpPath)
	pipelineAnalyzer := analyzer.NewGemmaAnalyzer("", "gemma3:4b")
	pipelineDownloader := downloader.NewYtDlpDownloader(cfg.Paths.YtDlpPath, cfg.GetDownloadDir())

	pipelineEngine := coordinator.NewEngine(pipelineStore, pipelineFetcher, pipelineAnalyzer, pipelineDownloader, 3)

	// Wrap for ServiceGroup
	services = append(services, runtime.NewServiceAdapter(
		"PipelineEngineV3",
		func(ctx context.Context) error {
			pipelineEngine.Start(ctx)
			return nil
		},
		func() error {
			pipelineEngine.Stop()
			return nil
		},
	))

	// === Channel Monitor === (disabled by default, set VELOX_ENABLE_CHANNEL_MONITOR=true to enable)
	var channelMonitorHandler *monitoring.ChannelMonitorHandler
	if os.Getenv("VELOX_ENABLE_CHANNEL_MONITOR") == "true" && core.YouTubeClientV2 != nil && driveClient != nil {
		configPath := "data/channel_monitor_config.json"
		fileCfg, err := channelmonitor.LoadConfigWithDefaults(configPath)
		if err != nil {
			log.Warn("Failed to load channel monitor config", zap.Error(err))
		}
		clipRootID := cfg.GetClipRootFolder()
		if clipRootID == "" && fileCfg != nil {
			clipRootID = fileCfg.ClipRootID
		}
		monitorCfg := channelmonitor.MonitorConfig{
			Channels: fileCfg.Channels, CheckInterval: fileCfg.CheckInterval,
			VideoTimeframe: fileCfg.VideoTimeframe,
			ClipRootID:     clipRootID,
			YtDlpPath:      fileCfg.YtDlpPath,
			FFmpegPath:     fileCfg.FFmpegPath,
			CookiesPath:    fileCfg.CookiesPath, MaxClipDuration: fileCfg.MaxClipDuration,
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
		if monitorCfg.FFmpegPath == "" {
			monitorCfg.FFmpegPath = "ffmpeg"
		}
		if monitorCfg.ClipRootID == "" {
			monitorCfg.ClipRootID = clipRootID
		}
		if monitorCfg.CheckInterval == 0 {
			monitorCfg.CheckInterval = 24 * time.Hour
		}
		if monitorCfg.VideoTimeframe == "" {
			monitorCfg.VideoTimeframe = "month"
		}
		if monitorCfg.MaxClipDuration == 0 {
			monitorCfg.MaxClipDuration = 60
		}
		ollamaURL := "http://localhost:11434"
		if monitorCfg.OllamaURL != "" {
			ollamaURL = monitorCfg.OllamaURL
		}
		monitor := channelmonitor.NewMonitor(
			monitorCfg, core.YouTubeClientV2, driveClient, ollamaURL,
		)
		channelMonitorHandler = monitoring.NewChannelMonitorHandler(
			monitor, core.YouTubeClientV2, driveClient, ollamaURL, cfg.Storage.DataDir,
		)
		// Register as BackgroundService (native implementation)
		services = append(services, monitor)
	}

	// === Stock Job Scheduler === (disabled by default)
	var stockScheduler *stockjob.Scheduler
	if os.Getenv("VELOX_ENABLE_STOCK_SCHEDULER") == "true" && clips.StockDB != nil && core.YouTubeClientV2 != nil {
		clipDBAdapter := &mainClipDB{db: clips.StockDB}
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
			schedulerConfig, core.YouTubeClientV2, core.TikTokClient, clipDBAdapter, nil,
		)
		// Register as BackgroundService (native implementation)
		services = append(services, stockScheduler)
	}

	// === Harvester ===
	var harvesterSvc *harvester.Harvester
	var harvesterHandler *harvester.Handler
	if core.YouTubeClientV2 != nil && driveClient != nil && clips.ClipDB != nil {
		ytAdapter := adapters.NewYouTubeSearcherAdapter(core.YouTubeClientV2)

		harvesterConfig, err := harvester.LoadConfigWithDefaults("data/harvester_config.json")
		if err != nil {
			log.Warn("Failed to load harvester config, using defaults", zap.Error(err))
			harvesterConfig = harvester.DefaultConfig()
		}

		harvesterConfig.DownloadDir = cfg.GetDownloadDir()
		harvesterConfig.DriveFolderID = cfg.Drive.StockRootFolderID

		clipAdapter := adapters.NewClipDBToHarvesterAdapter(clips.ClipDB)
		harvesterSvc = harvester.NewHarvester(harvesterConfig, ytAdapter, core.TikTokClient, driveClient, clipAdapter, core.Queue)
		harvesterHandler = harvester.NewHandler(harvesterSvc)
		if cronManager := harvesterHandler.CronManager(); cronManager != nil {
			services = append(services, runtime.NewServiceAdapter(
				"HarvesterCronManager",
				func(ctx context.Context) error {
					cronManager.Start(ctx)
					return nil
				},
				func() error {
					cronManager.Stop()
					return nil
				},
			))
		}

		// Register as BackgroundService (native implementation)
		services = append(services, harvesterSvc)
		log.Info("Harvester initialized")
	}

	return &BackgroundDeps{
		ChannelMonitorHandler: channelMonitorHandler,
		StockScheduler:        stockScheduler,
		HarvesterSvc:          harvesterSvc,
		HarvesterHandler:      harvesterHandler,
		PipelineEngine:        pipelineEngine,
	}, services, nil
}
