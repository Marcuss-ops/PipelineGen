package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"velox/go-master/internal/cron"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/service/indexing"
	svcjobs "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/monitor"
	"velox/go-master/internal/service/scheduler"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

type backgroundJobs struct {
	harvesterCronSvc *cron.HarvesterCronService
	catalogSyncJob   *cron.CatalogSyncJob
	channelMonitor   *monitor.ChannelMonitor
	stockScheduler   *scheduler.StockScheduler
	dbMaintenanceJob *cron.DBMaintenanceJob
	dbBackupJob      *cron.DBBackupJob
	indexingService  *indexing.Service
	jobRunner        *svcjobs.Runner
	jobScanner       *jobrepo.Scanner
}

func startBackgroundJobs(ctx context.Context, cfg *config.Config, dbs *databases, svcs *services, log *zap.Logger) *backgroundJobs {
	host := cfg.Server.Host
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	apiURL := fmt.Sprintf("http://%s:%d", host, cfg.Server.Port)
	harvesterCronSvc := cron.NewHarvesterCronService(svcs.harvesterRepo, log, apiURL, cfg.Storage.DataDir)
	go harvesterCronSvc.Start(ctx)
	log.Info("Harvester cron service started", zap.String("api_url", apiURL))

	// Jobs system - Runner and Scanner
	var jobRunner *svcjobs.Runner
	var jobScanner *jobrepo.Scanner
	if svcs.jobsService != nil && svcs.jobsDispatcher != nil && svcs.jobsRepo != nil {
		runnerConfig := svcjobs.RunnerConfig{
			Workers:   2,
			PollEvery: 2 * time.Second,
			LeaseTTL:  5 * time.Minute,
			JobTypes:  nil, // all types
		}
		jobRunner = svcjobs.NewRunner(svcs.jobsRepo, svcs.jobsDispatcher, log, runnerConfig)
		go jobRunner.Start(ctx)
		log.Info("Job runner started", zap.Int("workers", runnerConfig.Workers))

		jobScanner = jobrepo.NewScanner(svcs.jobsRepo, log)
		go jobScanner.Start(ctx, 5*time.Minute)
		log.Info("Job scanner started")
	}

	catalogSyncJob := cron.NewCatalogSyncJob(svcs.catalogSync, log)
	catalogSyncInterval := 6 * time.Hour
	if cfg.Jobs.CatalogSyncInterval != "" {
		if parsed, err := time.ParseDuration(cfg.Jobs.CatalogSyncInterval); err == nil {
			catalogSyncInterval = parsed
		}
	}
	go catalogSyncJob.Start(ctx, catalogSyncInterval)
	log.Info("Catalog sync job started", zap.Duration("interval", catalogSyncInterval))

	var channelMon *monitor.ChannelMonitor
	if os.Getenv("VELOX_ENABLE_CHANNEL_MONITOR") == "true" {
		channelMon = monitor.NewChannelMonitor(cfg, svcs.stockDriveRepo, log)
		go channelMon.Start(ctx)
		log.Info("Channel monitor started")
	}

	var stockSched *scheduler.StockScheduler
	if os.Getenv("VELOX_ENABLE_STOCK_SCHEDULER") == "true" {
		stockSched = scheduler.NewStockScheduler(cfg, log)
		go stockSched.Start(ctx)
		log.Info("Stock scheduler started")
	}

	maintenanceInterval := 24 * time.Hour
	if cfg.Jobs.MaintenanceInterval != "" {
		if parsed, err := time.ParseDuration(cfg.Jobs.MaintenanceInterval); err == nil {
			maintenanceInterval = parsed
		}
	}
	dbMaintenanceJob := cron.NewDBMaintenanceJob(svcs.scriptsRepo, dbs.main, log)
	go dbMaintenanceJob.StartCron(ctx, maintenanceInterval)
	log.Info("DB maintenance cron started", zap.Duration("interval", maintenanceInterval))

	backupInterval := 6 * time.Hour
	if cfg.Jobs.BackupInterval != "" {
		if parsed, err := time.ParseDuration(cfg.Jobs.BackupInterval); err == nil {
			backupInterval = parsed
		}
	}
	backupDir := filepath.Join(cfg.Storage.DataDir, cfg.Storage.BackupsDir)
	dbBackupJob := cron.NewDBBackupJob(dbs.main, log, backupDir)
	go dbBackupJob.StartCron(ctx, backupInterval)
	log.Info("DB backup cron started", zap.String("backup_dir", backupDir), zap.Duration("interval", backupInterval))

	indexingInterval := 15 * time.Minute
	if cfg.Jobs.IndexingInterval != "" {
		if parsed, err := time.ParseDuration(cfg.Jobs.IndexingInterval); err == nil {
			indexingInterval = parsed
		}
	}
	downloadDir := filepath.Join(cfg.Storage.DataDir, cfg.Storage.DownloadsDir)
	svcs.indexingService.StartCron(ctx, downloadDir, indexingInterval)
	log.Info("Indexing cron started", zap.Duration("interval", indexingInterval))

	return &backgroundJobs{
		harvesterCronSvc: harvesterCronSvc,
		catalogSyncJob:   catalogSyncJob,
		channelMonitor:   channelMon,
		stockScheduler:   stockSched,
		dbMaintenanceJob: dbMaintenanceJob,
		dbBackupJob:      dbBackupJob,
		indexingService:  svcs.indexingService,
		jobRunner:        jobRunner,
		jobScanner:       jobScanner,
	}
}
