package bootstrap

import (
	"context"
	"os"
	"time"

	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/service/indexing"
	svcjobs "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/monitor"
	"velox/go-master/internal/service/scheduler"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

type backgroundJobs struct {
	channelMonitor    *monitor.ChannelMonitor
	stockScheduler    *scheduler.StockScheduler
	driveSyncSchedule *scheduler.DriveSyncScheduler
	indexingService   *indexing.Service
	jobRunner         *svcjobs.Runner
	jobScanner        *jobrepo.Scanner
}

func startBackgroundJobs(ctx context.Context, cfg *config.Config, dbs *databases, svcs *services, log *zap.Logger, mode string) *backgroundJobs {
	// Check if background jobs are enabled
	if os.Getenv("VELOX_ENABLE_BACKGROUND_JOBS") == "false" {
		log.Info("Background jobs disabled via VELOX_ENABLE_BACKGROUND_JOBS=false")
		return &backgroundJobs{}
	}

	// Parse mode
	runWorker := mode == "all" || mode == "worker"
	runScheduler := mode == "all" || mode == "scheduler"
	runMaintenance := mode == "all" || mode == "maintenance"

	log.Info("Background jobs mode", zap.String("mode", mode),
		zap.Bool("worker", runWorker),
		zap.Bool("scheduler", runScheduler),
		zap.Bool("maintenance", runMaintenance))

	var jobRunner *svcjobs.Runner
	var jobScanner *jobrepo.Scanner
	var channelMon *monitor.ChannelMonitor
	var stockSched *scheduler.StockScheduler
	var driveSyncSched *scheduler.DriveSyncScheduler

	if runWorker {
		// Jobs system - Runner and Scanner
		if svcs.jobsService != nil && svcs.jobsDispatcher != nil && svcs.jobsRepo != nil {
			workers := cfg.Jobs.MaxParallelPerProject
			if workers <= 0 {
				workers = 1
			}
			leaseTTL := time.Duration(cfg.Jobs.LeaseTTLSeconds) * time.Second
			if leaseTTL <= 0 {
				leaseTTL = 5 * time.Minute
			}
			runnerConfig := svcjobs.RunnerConfig{
				Workers:   workers,
				PollEvery: 2 * time.Second,
				LeaseTTL:  leaseTTL,
				JobTypes:  nil, // all types
			}
			jobRunner = svcjobs.NewRunner(svcs.jobsRepo, svcs.jobsDispatcher, log, runnerConfig)
			go jobRunner.Start(ctx)
			log.Info("Job runner started", zap.Int("workers", runnerConfig.Workers))

			jobScanner = jobrepo.NewScanner(svcs.jobsRepo, log)
			go jobScanner.Start(ctx, 5*time.Minute)
			log.Info("Job scanner started")
		}
	}

	if runScheduler {
		if os.Getenv("VELOX_ENABLE_CHANNEL_MONITOR") == "true" {
			channelMon = monitor.NewChannelMonitor(cfg, svcs.stockDriveRepo, log, svcs.youtubeClipService)
			go channelMon.Start(ctx)
			log.Info("Channel monitor started")
		}

		if os.Getenv("VELOX_ENABLE_STOCK_SCHEDULER") == "true" {
			stockSched = scheduler.NewStockScheduler(cfg, log)
			go stockSched.Start(ctx)
			log.Info("Stock scheduler started")
		}

		// Periodic Drive sync scheduler - always enabled if sync services exist
		if svcs.catalogSync != nil || svcs.voiceoverSync != nil || svcs.imageService != nil {
			syncInterval := 6 * time.Hour // default
			if cfg.Jobs.CatalogSyncInterval != "" {
				if parsed, err := time.ParseDuration(cfg.Jobs.CatalogSyncInterval); err == nil {
					syncInterval = parsed
				}
			}
			driveSyncSched = scheduler.NewDriveSyncScheduler(
				svcs.catalogSync,
				svcs.voiceoverSync,
				svcs.imageService,
				log,
				syncInterval,
			)
			go driveSyncSched.Start(ctx)
			log.Info("Drive sync scheduler started", zap.Duration("interval", syncInterval))
		}
	}

	if runMaintenance {
		maintenanceInterval := 24 * time.Hour
		if cfg.Jobs.MaintenanceInterval != "" {
			if parsed, err := time.ParseDuration(cfg.Jobs.MaintenanceInterval); err == nil {
				maintenanceInterval = parsed
			}
		}
		log.Info("DB maintenance would run via jobs system", zap.Duration("interval", maintenanceInterval))

		backupInterval := 6 * time.Hour
		if cfg.Jobs.BackupInterval != "" {
			if parsed, err := time.ParseDuration(cfg.Jobs.BackupInterval); err == nil {
				backupInterval = parsed
			}
		}
		log.Info("DB backup would run via jobs system", zap.Duration("interval", backupInterval))
	}

	return &backgroundJobs{
		channelMonitor:    channelMon,
		stockScheduler:    stockSched,
		driveSyncSchedule: driveSyncSched,
		indexingService:   svcs.indexingService,
		jobRunner:         jobRunner,
		jobScanner:        jobScanner,
	}
}