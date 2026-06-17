package app

import (
	"context"
	"database/sql"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	svcjobs "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/indexing"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/monitor"
	jobrepo "velox/go-master/internal/repository/jobs"
	scriptrepo "velox/go-master/internal/repository/scripts"
	searchqueriesrepo "velox/go-master/internal/repository/searchqueries"
	"velox/go-master/internal/storage/scheduler"
	"velox/go-master/pkg/concurrent"
)

type backgroundJobs struct {
	channelMonitor    *monitor.ChannelMonitor
	driveSyncSchedule *scheduler.DriveSyncScheduler
	indexingService   *indexing.Service
	jobRunner         *svcjobs.Runner
	jobScanner        *jobrepo.Scanner
	scriptsRepo       *scriptrepo.ScriptRepository
}

func startBackgroundJobs(ctx context.Context, cfg *config.Config, dbs *databases, svcs *services, log *zap.Logger, mode string) *backgroundJobs {
	// Check if background jobs are enabled
	if !cfg.Jobs.EnableBackgroundJobs {
		log.Info("Background jobs disabled via config")
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
			// Job runner is NOT started here — it will be started in WireServices
			// after WireRegistry completes and all job handlers are registered.
			// See startJobRunner() for the actual start call.
			log.Info("Job runner created", zap.Int("workers", runnerConfig.Workers))

			jobScanner = jobrepo.NewScanner(svcs.jobsRepo, log)
			concurrent.SafeGo("job-scanner", func() { jobScanner.Start(ctx, 5*time.Minute) })
			log.Info("Job scanner started")

			// Refresh queue / oldest-pending / stale-assets gauges every 30s so
			// Prometheus has fresh data instead of leaving the gauges at zero.
			svcjobs.StartMetricsRefresher(ctx, svcs.jobsRepo, 30*time.Second, log)
		}
	}

	if runScheduler {
		if cfg.Jobs.EnableChannelMonitor {
			var dbForChannels *sql.DB
			if dbs.main != nil && dbs.main.DB != nil {
				dbForChannels = dbs.main.DB
			}

			channelMon = monitor.NewChannelMonitor(cfg, svcs.stockDriveRepo, log, svcs.youtubeClipService, dbForChannels, svcs.ollamaClient)

			// Wire search queries repo for topic-based searches
			if dbForChannels != nil {
				sqRepo := searchqueriesrepo.NewRepository(dbForChannels)
				channelMon.SetSearchQueriesRepo(sqRepo)
				log.Info("Search queries repo wired to channel monitor")
			}

			// Set YouTube search rate limit from config
			if cfg.Jobs.SearchRateLimit > 0 {
				channelMon.SetSearchRateLimit(cfg.Jobs.SearchRateLimit)
			}

			concurrent.SafeGo("channel-monitor", func() { channelMon.Start(ctx) })
			log.Info("Channel monitor started")
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
			concurrent.SafeGo("drive-sync-scheduler", func() { driveSyncSched.Start(ctx) })
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
		backupInterval := 6 * time.Hour
		if cfg.Jobs.BackupInterval != "" {
			if parsed, err := time.ParseDuration(cfg.Jobs.BackupInterval); err == nil {
				backupInterval = parsed
			}
		}

		// Actually schedule maintenance and backup jobs via the job system
		if svcs.jobsService != nil {
			scheduleMaintenanceJob := func(interval time.Duration, label string) {
				concurrent.SafeGo("maintenance-scheduler-"+label, func() {
					select {
					case <-ctx.Done():
						return
					case <-time.After(2 * time.Minute):
					}
					for {
						_, err := svcs.jobsService.Enqueue(ctx, &svcjobs.EnqueueRequest{
							Type:     models.JobTypeSystemCleanup,
							Priority: 5,
							Payload: map[string]any{
								"label":  label,
								"source": "scheduled",
							},
						})
						if err != nil {
							log.Warn("failed to enqueue maintenance job", zap.String("label", label), zap.Error(err))
						} else {
							log.Info("scheduled maintenance job enqueued", zap.String("label", label))
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(interval):
						}
					}
				})
			}
			scheduleMaintenanceJob(maintenanceInterval, "maintenance")
			scheduleMaintenanceJob(backupInterval, "backup")
			log.Info("scheduled maintenance and backup jobs via jobs system",
				zap.Duration("maintenance_interval", maintenanceInterval),
				zap.Duration("backup_interval", backupInterval))
		} else {
			log.Warn("jobs service not available, skipping scheduled maintenance/backup")
		}
	}

	if runScheduler && svcs.youtubeClipService != nil {
		// Warm the YouTube metadata cache L1 at startup
		concurrent.SafeGo("yt-cache-prewarm", func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			sCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := svcs.youtubeClipService.PrewarmHotVideoMetadataCache(sCtx); err != nil {
				log.Warn("Failed to pre-warm YouTube video metadata cache", zap.Error(err))
			}
		})

		// Schedule nightly pre-warming (every 24 hours)
		concurrent.SafeGo("yt-nightly-prewarm", func() {
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					log.Info("Running nightly pre-warming job for hot YouTube video metadata cache")
					sCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					if err := svcs.youtubeClipService.PrewarmHotVideoMetadataCache(sCtx); err != nil {
						log.Warn("Failed to run nightly pre-warming job for YouTube metadata", zap.Error(err))
					}
					cancel()
				}
			}
		})
	}

	// ── Maintenance sweepers (only in maintenance or all mode) ───────
	if runMaintenance {
		// Sweep stale research_cache rows once per 6 hours.
		if svcs.scriptsRepo != nil {
			concurrent.SafeGo("research-cache-sweeper", func() {
				startResearchCacheSweeper(ctx, svcs.scriptsRepo, log)
			})
		}

		// Sweep gemma memory tables.
		if svcs.memoryRepo != nil {
			concurrent.SafeGo("gemma-memory-sweeper", func() {
				startGemmaMemorySweeper(ctx, svcs.memoryRepo, log)
			})
		}

		// Qdrant stale points cleaner — every 12 hours.
		if svcs.vectorSvc != nil && svcs.driveUploader != nil {
			concurrent.SafeGo("qdrant-cleaner", func() {
				startQdrantCleaner(ctx, svcs.vectorSvc, svcs.driveUploader, log)
			})
		}

		// Clip dedup sweeper — every 30 minutes.
		if svcs.clipsOnlyRepo != nil {
			concurrent.SafeGo("clip-dedup-sweeper", func() {
				log.Info("clip dedup sweeper starting (interval=30m)")
				startClipDedupSweeper(ctx, svcs.clipsOnlyRepo, log)
			})
		}

		// VLM Auto-Tag sweeper — every 15 minutes.
		if svcs.autotagService != nil {
			concurrent.SafeGo("vlm-autotag-sweeper", func() {
				log.Info("VLM auto-tag sweeper starting (interval=15m)")
				startVLMAutoTagSweeper(ctx, svcs.autotagService, log)
			})
		}

		// Qdrant ghost-points sweeper — daily. Closes the
		// Qdrant↔SQLite drift that index_health logs as
		// orphan_in_qdrant. Pair with the existing 12-hour
		// startQdrantCleaner (Drive-link validity) for full
		// catalog-store convergence.
		if svcs.vectorSvc != nil && dbs.main != nil && dbs.main.DB != nil {
			concurrent.SafeGo("qdrant-ghost-sweeper", func() {
				db := dbs.main.DB
				log.Info("Qdrant ghost-points sweeper starting (interval=24h, initialDelay=10m)")
				startQdrantGhostSweeper(ctx, svcs.vectorSvc, db, log)
			})
		}
	}

	// ── Health monitoring (lightweight, always runs) ───────────────────
	// Qdrant health monitor — every 60s, updates Prometheus gauge.
	if svcs.vectorSvc != nil {
		concurrent.SafeGo("qdrant-health-monitor", func() {
			log.Info("Qdrant health monitor starting (interval=60s)")
			startQdrantHealthMonitor(ctx, svcs.vectorSvc, log)
		})
	}

	return &backgroundJobs{
		channelMonitor:    channelMon,
		driveSyncSchedule: driveSyncSched,
		indexingService:   svcs.indexingService,
		jobRunner:         jobRunner,
		jobScanner:        jobScanner,
		scriptsRepo:       svcs.scriptsRepo,
	}
}
