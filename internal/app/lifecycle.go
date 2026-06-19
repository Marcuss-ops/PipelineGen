package app

import (
	"context"
	"database/sql"
	"time"
	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	svcjobs "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/monitor"
	scriptrepo "github.com/Marcuss-ops/PipelineGen/internal/scripts"
	searchqueriesrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/scheduler"
	concurrent "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/media/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	clipsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	urlutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
	gdrive "google.golang.org/api/drive/v3"
	"github.com/Marcuss-ops/PipelineGen/internal/core/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
)

type backgroundJobs struct {
	channelMonitor    *monitor.ChannelMonitor
	driveSyncSchedule *scheduler.DriveSyncScheduler
	jobRunner         *svcjobs.Runner
	jobScanner        *svcjobs.Scanner
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
	var jobScanner *svcjobs.Scanner
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
			}		// The concrete repo now directly implements job.Repository (PR4).
		jobRunner = svcjobs.NewRunner(svcs.jobsRepo, svcs.jobsDispatcher, log, runnerConfig)
			// Job runner is NOT started here — it will be started in WireServices
			// after WireRegistry completes and all job handlers are registered.
			// See startJobRunner() for the actual start call.
			log.Info("Job runner created", zap.Int("workers", runnerConfig.Workers))

			jobScanner = svcjobs.NewScanner(svcs.jobsRepo, log)
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

			channelMon = monitor.NewChannelMonitor(cfg, svcs.clipsRepo, log, svcs.youtubeClipService, dbForChannels, svcs.ollamaClient)

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
					Type:     svcjobs.JobTypeSystemCleanup,
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
		if svcs.clipsRepo != nil {
			concurrent.SafeGo("clip-dedup-sweeper", func() {
				log.Info("clip dedup sweeper starting (interval=30m)")
				startClipDedupSweeper(ctx, svcs.clipsRepo, log)
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

	// ── Delivery runner (always runs if deliveries service exists) ────
	if svcs.DeliveryService != nil && svcs.DeliveryRunner != nil {
		concurrent.SafeGo("delivery-runner", func() {
			log.Info("Delivery runner starting")
			svcs.DeliveryRunner.Start(ctx)
		})
	}

	return &backgroundJobs{
		channelMonitor:    channelMon,
		driveSyncSchedule: driveSyncSched,
		jobRunner:         jobRunner,
		jobScanner:        jobScanner,
		scriptsRepo:       svcs.scriptsRepo,
	}
}
// startResearchCacheSweeper deletes research_cache rows whose last_used is
// older than 30 days. Runs every 6 hours; logs a warning on error. The
// short initial delay avoids contention during server bootstrap.
func startResearchCacheSweeper(ctx context.Context, repo *scriptrepo.ScriptRepository, log *zap.Logger) {
	const (
		initialDelay = 30 * time.Second
		interval     = 6 * time.Hour
		maxAgeDays   = 30
	)
	// Wait for initial delay, but exit immediately if context is cancelled.
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Run once immediately after the initial delay, then on each tick.
	sweep := func() {
		sCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		deleted, err := repo.SweepStaleResearchCache(sCtx, maxAgeDays)
		if err != nil {
			log.Warn("research_cache sweep failed", zap.Error(err))
			return
		}
		if deleted > 0 {
			log.Info("research_cache swept", zap.Int64("deleted", deleted), zap.Int("max_age_days", maxAgeDays))
		}
	}
	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// startQdrantHealthMonitor polls Qdrant's health endpoint on a tight
// interval (default 60s) and updates the QdrantHealthStatus Prometheus
// gauge, logging a warning the first time the status flips from
// healthy → unhealthy. The first run fires after a short initial delay
// so the server has time to finish bootstrapping.
func startQdrantHealthMonitor(ctx context.Context, vectorSvc *vectorstore.Service, log *zap.Logger) {
	const (
		initialDelay = 15 * time.Second
		interval     = 60 * time.Second
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var wasHealthy *bool
	check := func() {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		err := vectorSvc.Health(checkCtx)
		healthy := err == nil
		if wasHealthy == nil || *wasHealthy != healthy {
			// State change — log so operators notice.
			if healthy {
				log.Info("Qdrant is healthy", zap.String("check_interval", interval.String()))
			} else {
				log.Warn("Qdrant health check FAILED — semantic search/upserts may degrade",
					zap.Error(err))
			}
			wasHealthy = &healthy
		}
	}

	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

// startClipDedupSweeper scans the clips repo for groups of clips that
// share the same youtube_video_id (and matching start/end if present).
// For each group with >1 entries, the most-recently-created clip is kept
// and the rest are soft-deleted. Runs every 30 minutes by default.
func startClipDedupSweeper(ctx context.Context, clipsRepo *clipsrepo.Repository, log *zap.Logger) {
	const (
		initialDelay = 2 * time.Minute
		interval     = 30 * time.Minute
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sweep := func() {
		sCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		swept, err := runDedupSweep(sCtx, clipsRepo, log)
		if err != nil {
			log.Warn("clip dedup sweep failed", zap.Error(err))
			return
		}
		if swept > 0 {
			log.Info("clip dedup sweep completed", zap.Int("soft_deleted", swept))
		}
	}

	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// runDedupSweep finds clip groups that share a youtube_video_id and
// soft-deletes all but the newest entry. Returns the number of clips
// soft-deleted. Safe to call concurrently — it uses soft-delete
// (metadata_json.deleted_at), not HARD DELETE.
func runDedupSweep(ctx context.Context, clipsRepo *clipsrepo.Repository, log *zap.Logger) (int, error) {
	// Pull the list of distinct youtube_video_ids with duplicates.
	rows, err := clipsRepo.DB().QueryContext(ctx, `
		SELECT json_extract(metadata_json, '$.youtube_video_id') AS vid, COUNT(*) AS n
		FROM media_assets
		WHERE `+clipsRepo.SoftDeleteFilter()+`
		  AND json_extract(COALESCE(metadata_json,'{}'), '$.youtube_video_id') IS NOT NULL
		  AND json_extract(COALESCE(metadata_json,'{}'), '$.youtube_video_id') != ''
		GROUP BY vid
		HAVING n > 1
		LIMIT 500`)
	if err != nil {
		return 0, fmt.Errorf("dedup sweep query: %w", err)
	}
	defer rows.Close()

	type groupRow struct {
		vid string
		n   int
	}
	var groups []groupRow
	for rows.Next() {
		var g groupRow
		if err := rows.Scan(&g.vid, &g.n); err != nil {
			log.Warn("dedup sweep scan failed", zap.Error(err))
			continue
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("dedup sweep rows: %w", err)
	}

	swept := 0
	for _, g := range groups {
		dupIDs, err := clipsRepo.FindDuplicatesByYouTubeID(ctx, g.vid, "")
		if err != nil {
			log.Warn("FindDuplicatesByYouTubeID failed", zap.String("video_id", g.vid), zap.Error(err))
			continue
		}
		// dupIDs comes back ordered by created_at DESC, so [0] is the
		// newest. Soft-delete the rest.
		for i := 1; i < len(dupIDs); i++ {
			if err := clipsRepo.DeleteClip(ctx, dupIDs[i]); err != nil {
				log.Warn("dedup sweep soft-delete failed",
					zap.String("clip_id", dupIDs[i]), zap.Error(err))
				continue
			}
			swept++
			metrics.DedupMerged.WithLabelValues("youtube-manual", "sweeper").Inc()
		}
	}
	return swept, nil
}

// startQdrantCleaner periodically validates Drive links in Qdrant points.
// Points whose Drive files have been trashed/deleted are removed so that
// semantic search never returns dead links. Runs every 12 hours.
func startQdrantCleaner(ctx context.Context, vectorSvc *vectorstore.Service, driveUploader *drive.Uploader, log *zap.Logger) {
	const (
		initialDelay = 5 * time.Minute
		interval     = 12 * time.Hour
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	clean := func() {
		sCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		validator := func(assetID, driveFileID, driveLink string) (bool, error) {
			// Prefer drive_file_id over URL-based link for validation
			fileID := driveFileID
			if fileID == "" {
				var err error
				fileID, err = urlutil.FileIDFromDriveLink(driveLink)
				if err != nil || fileID == "" {
					return false, fmt.Errorf("cannot extract file ID from link %q: %w", driveLink, err)
				}
			}
			return driveUploader.FileIsNotTrashed(sCtx, fileID)
		}

		deleted, err := vectorSvc.CleanupStalePoints(sCtx, validator)
		if err != nil {
			log.Warn("Qdrant stale point cleanup failed", zap.Error(err))
			return
		}
		if deleted > 0 {
			log.Info("Qdrant stale points removed", zap.Int("deleted", deleted))
		}
	}

	clean()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clean()
		}
	}
}

// startGemmaMemorySweeper periodically prunes gemma memory tables using
// SweepAll which combines decay, TTL deletion, per-channel capping, and
// chunk cleanup into a single transactional sweep.
func startGemmaMemorySweeper(ctx context.Context, repo *gemmamemory.Repository, log *zap.Logger) {
	const (
		initialDelay = 60 * time.Second
		interval     = 6 * time.Hour
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sweep := func() {
		sCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		deleted, err := repo.SweepAll(sCtx)
		if err != nil {
			log.Warn("gemma memory sweep failed", zap.Error(err))
			return
		}
		if deleted > 0 {
			log.Info("gemma memory sweep completed", zap.Int64("total_deleted", deleted))
		}
	}
	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// startVLMAutoTagSweeper scans the database for untagged assets and triggers
// the VLM autotag service. Runs every 15 minutes by default.
func startVLMAutoTagSweeper(ctx context.Context, autotagSvc *autotag.Service, log *zap.Logger) {
	const (
		initialDelay = 1 * time.Minute
		interval     = 15 * time.Minute
		batchSize    = 10
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	process := func() {
		sCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		processed, err := autotagSvc.ProcessUntagged(sCtx, batchSize)
		if err != nil {
			log.Warn("vlm auto-tag sweep failed", zap.Error(err))
			return
		}
		if processed > 0 {
			log.Info("vlm auto-tag sweep completed", zap.Int("processed", processed))
		}
	}

	process()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			process()
		}
	}
}
// ghostSweepable is the minimal Store subset the ghost sweeper needs.
// Inlined as a tiny interface (instead of importing vectorstore.Store
// with its 17 methods) so unit tests can mock just this much.
//
// Production callers pass *vectorstore.Service which satisfies it.
type ghostSweepable interface {
	ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error
	DeletePoints(ctx context.Context, assetIDs []string) error
}

// startQdrantGhostSweeper runs daily and removes "ghost" Qdrant points
// whose asset_id has NO matching row in the SQLite media_assets table.
//
// Why this matters: when the SQLite row is hard-deleted (manual purge,
// FK cascade, etc.) but the Qdrant point survives — usually because the
// outbox/cleanup path didn't reach that specific row — semantic search
// starts returning stale record_ids to handlers and Handlers.md §Indexer
// will cite ghost totals in realtime.Service.IndexHealth as
// orphan_in_qdrant. This sweeper closes the loop daily so the cross-check
// gap stays bounded by 24h instead of growing indefinitely.
//
// Flow:
//  1. Bulk-load every media_assets.id from SQLite into a hash set.
//  2. Stream ALL Qdrant asset_ids via ScrollAssetIDsPage.
//  3. Diff: ghosts = Qdrant IDs − SQLite IDs.
//  4. Delete ghosts via DeletePoints (filter on payload.asset_id).
//  5. Log total_deleted + tombstone sample for ops forensics.
//
// Idempotent: a partial run is fine — the next scheduled tick picks up
// where the previous one left off. Soft-deleted SQLite rows are NOT
// treated as missing (the live `id` is still present in the table;
// cleanup of those orphan points is the responsibility of the clip
// delete path, not this sweeper).
//
// Conservative on work-budget: a hard 30m ceiling per pass so a runaway
// Qdrant or stuck SELECT cannot starve other maintenance sweepers.
func startQdrantGhostSweeper(ctx context.Context, vectorSvc *vectorstore.Service, db *sql.DB, log *zap.Logger) {
	const (
		initialDelay    = 10 * time.Minute // out-of-phase with startQdrantCleaner (5m) and startClipDedupSweeper (2m)
		interval        = 24 * time.Hour   // daily per requirement
		scrollBatchSize = 500
		sqlitePageSize  = 1000
		maxWorkBudget   = 30 * time.Minute
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sweep := func() {
		sCtx, cancel := context.WithTimeout(ctx, maxWorkBudget)
		defer cancel()
		deleted, err := runGhostSweep(sCtx, vectorSvc, db, scrollBatchSize, sqlitePageSize, log)
		if err != nil {
			log.Warn("Qdrant ghost sweep failed", zap.Error(err))
			return
		}
		if deleted > 0 {
			log.Info("Qdrant ghost points removed", zap.Int("deleted", deleted))
		} else {
			log.Info("Qdrant ghost sweep clean (no drift detected)", zap.Int("deleted", 0))
		}
	}

	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// runGhostSweep performs a single ghost-sweep pass. Exported (lower-case)
// so sweepers_test.go can call it directly without the daily ticker.
// Returned int is the number of Qdrant points actually deleted.
func runGhostSweep(ctx context.Context, qdrant ghostSweepable, db *sql.DB, scrollBatchSize, sqlitePageSize int, log *zap.Logger) (int, error) {
	if qdrant == nil {
		return 0, fmt.Errorf("qdrant store is nil")
	}
	if db == nil {
		return 0, fmt.Errorf("sqlite db is nil")
	}
	if scrollBatchSize <= 0 {
		scrollBatchSize = 500
	}
	if sqlitePageSize <= 0 {
		sqlitePageSize = 1000
	}

	// 1. Bulk-fetch every media_assets.id from SQLite, paginated to keep
	// memory bounded. ALL rows count — soft-deletes are still "present"
	// from Qdrant's perspective, so soft-delete ghosts are not our job.
	sqliteIDs := make(map[string]struct{}, 8192)
	offset := 0
	for {
		rows, err := db.QueryContext(ctx, `SELECT id FROM media_assets LIMIT ? OFFSET ?`, sqlitePageSize, offset)
		if err != nil {
			return 0, fmt.Errorf("query sqlite asset ids (limit=%d, offset=%d): %w", sqlitePageSize, offset, err)
		}
		batchN := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, fmt.Errorf("scan asset id at offset %d: %w", offset, err)
			}
			sqliteIDs[id] = struct{}{}
			batchN++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("iterate sqlite asset ids: %w", err)
		}
		if batchN < sqlitePageSize {
			break
		}
		offset += sqlitePageSize
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
	}
	log.Debug("ghost sweep loaded sqlite ids", zap.Int("count", len(sqliteIDs)))

	// 2. Stream Qdrant and accumulate ghosts.
	var ghosts []string
	scrollErr := qdrant.ScrollAssetIDsPage(ctx, scrollBatchSize, func(batch []string) error {
		for _, id := range batch {
			if _, ok := sqliteIDs[id]; !ok {
				ghosts = append(ghosts, id)
			}
		}
		return nil
	})
	if scrollErr != nil {
		return 0, fmt.Errorf("scroll qdrant asset ids: %w", scrollErr)
	}

	log.Debug("ghost sweep scanned qdrant",
		zap.Int("sqlite", len(sqliteIDs)),
		zap.Int("ghosts", len(ghosts)))
	if len(ghosts) == 0 {
		return 0, nil
	}

	// 3. Delete ghosts. DeletePoints handles internal chunking at
	// ghostSweepDeleteBatch (100). For >=10k ghosts this becomes a
	// meaningful log+delete loop so we cap at 100/page to keep log noise
	// proportional to drift arrived in one tick.
	for i := 0; i < len(ghosts); i += 100 {
		end := i + 100
		if end > len(ghosts) {
			end = len(ghosts)
		}
		if err := qdrant.DeletePoints(ctx, ghosts[i:end]); err != nil {
			return i, fmt.Errorf("delete ghosts %d-%d: %w", i, end, err)
		}
	}

	// 4. Tombstone sample for ops forensics (debug level — operators
	// enable zap.Debug on the pipelinegen logger to see WHICH ghost_ids
	// were removed, critical for tracing the upstream cause).
	sample := ghosts
	if len(sample) > 20 {
		sample = sample[:20]
	}
	log.Debug("Qdrant ghost points deleted — sample",
		zap.Int("total_deleted", len(ghosts)),
		zap.Strings("sample_ids", sample))

	return len(ghosts), nil
}
// LifecycleDeps holds the dependencies needed to create a lifecycle service
type LifecycleDeps struct {
	Registry      artifacts.Registry
	DriveClient   *gdrive.Service
	AssetIndex    *assetindex.Service
	DriveVerifier artifacts.DriveVerifier
	Finalizer     *artifacts.Finalizer
	Store         lifecycle.AssetRecordStore
}

// NewLifecycleFromDeps creates a lifecycle Service using the provided dependencies.
// This eliminates the boilerplate of creating verifier, finalizer, store adapter, and lifecycle.
func NewLifecycleFromDeps(
	deps *LifecycleDeps,
	log *zap.Logger,
) *lifecycle.Service {
	// Create drive verifier if not provided
	if deps.DriveVerifier == nil && deps.DriveClient != nil {
		deps.DriveVerifier = artifacts.NewAPIDriveVerifier(deps.DriveClient)
	}

	// Create finalizer if not provided
	if deps.Finalizer == nil && deps.Registry != nil && deps.DriveVerifier != nil && deps.AssetIndex != nil {
		deps.Finalizer = artifacts.NewFinalizerWithAssetIndex(
			deps.Registry,
			deps.DriveVerifier,
			deps.AssetIndex,
			log,
		)
	}

	// Create store adapter if not provided
	if deps.Store == nil && deps.Registry != nil {
		deps.Store = lifecycle.NewRegistryStoreAdapter(deps.Registry)
	}

	// Create and return lifecycle service
	return lifecycle.NewService(
		deps.Store,
		deps.DriveClient,
		deps.Registry,
		deps.AssetIndex,
		deps.Finalizer,
		lifecycle.DefaultConfig(),
		log,
	)
}
