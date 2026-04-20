package main

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/adapters"
	"velox/go-master/internal/api/handlers"
	"velox/go-master/internal/harvester"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/service/channelmonitor"
	"velox/go-master/internal/stockjob"
	"velox/go-master/pkg/config"
)

// BackgroundDeps holds the background service handlers and the services themselves.
type BackgroundDeps struct {
	ChannelMonitorHandler *handlers.ChannelMonitorHandler
	StockScheduler        *stockjob.Scheduler
	HarvesterSvc          *harvester.Harvester
	HarvesterHandler      *harvester.Handler
}

// initBackgroundServices initializes the long-running background services:
// channel monitor, stock job scheduler, and YouTube harvester.
//
// Services are created but NOT started here — they are returned as
// BackgroundService instances for registration with the ServiceGroup,
// which provides unified lifecycle management (start, stop, rollback).
func initBackgroundServices(
	cfg *config.Config, log *zap.Logger, core *CoreDeps, clips *ClipDeps, drive *DriveDeps,
) (*BackgroundDeps, []runtime.BackgroundService, error) {
	driveClient := drive.DriveHandler.GetDriveClient()
	var services []runtime.BackgroundService

	// === Channel Monitor === (disabled by default, set VELOX_ENABLE_CHANNEL_MONITOR=true to enable)
	var channelMonitorHandler *handlers.ChannelMonitorHandler
	if os.Getenv("VELOX_ENABLE_CHANNEL_MONITOR") == "true" && core.YouTubeClientV2 != nil && driveClient != nil {
		configPath := "data/channel_monitor_config.json"
		fileCfg, err := channelmonitor.LoadConfigWithDefaults(configPath)
		if err != nil {
			log.Warn("Failed to load channel monitor config", zap.Error(err))
		}
		monitorCfg := channelmonitor.MonitorConfig{
			Channels: fileCfg.Channels, CheckInterval: fileCfg.CheckInterval,
			VideoTimeframe: fileCfg.VideoTimeframe,
			StockRootID:    fileCfg.StockRootID, YtDlpPath: fileCfg.YtDlpPath,
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
			monitorCfg.StockRootID = "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh"
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
		channelMonitorHandler = handlers.NewChannelMonitorHandler(
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
	}, services, nil
}
