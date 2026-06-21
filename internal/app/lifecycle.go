// Package app — background lifecycle (PR4: takes *ComposeRoot).
//
// Before PR4 this file took the legacy `*services` struct. After PR4 it
// takes *ComposeRoot (the per-bundle decomposition). The body is the same
// `startBackgroundJobs(ctx, cfg, dbs, root, log, mode) (*backgroundJobs)`
// pattern as before, but reads from root.Domains, root.Repos, root.Process,
// root.Outbox, root.Jobs, root.Domains.RealtimeService, etc.
//
// The returned *backgroundJobs handle is consumed by shutdown.go for
// graceful teardown (channel-monitor.Stop, drive-sync-scheduler.Stop).
package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/scheduler"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	_ "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	_ "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/application/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// backgroundJobs holds references to goroutines and services started by
// startBackgroundJobs that need explicit Stop() during shutdown.
//
// In PR4, the Monitor reference is also published to root.LateBindings so
// other subsystems can read it after bootstrap.go returns. The handle here
// also powers shutdown.go's LIFO orchestration.
type backgroundJobs struct {
	channelMonitor    *monitor.ChannelMonitor
	driveSyncSchedule *scheduler.DriveSyncScheduler
	jobRunner         *appjobs.Runner
	jobScanner        *sqlitejobs.Scanner
	scriptsRepo       *sqlitescripts.ScriptRepository
	// startJobRunner is called by WireServices AFTER registry Freeze() so that
	// the JobRunner.Start loop begins claiming jobs only when no further
	// handlers can register. Captures ctx + root + log from
	// startBackgroundJobs' local scope via closure. PR4d-final (June 2026):
	// replaces CoreDeps.startJobRunner.
	startJobRunner func()
}

// startBackgroundJobs creates + starts the per-mode background workers.
// Takes the assembled *ComposeRoot (PR4a). Returns a handle for shutdown.
//
// Mode mapping (matches previous semantics):
//   - "all"        → runWorker + runScheduler + runMaintenance
//   - "worker"     → runWorker only
//   - "scheduler"  → runScheduler only
//   - "maintenance"→ runMaintenance only
//   - ""           → runWorker only (back-compat with InitCore callers)
func startBackgroundJobs(ctx context.Context, cfg *config.Config, dbs *databases, root *ComposeRoot, log *zap.Logger, mode string) *backgroundJobs {
	if root == nil {
		log.Warn("startBackgroundJobs called with nil ComposeRoot — skipping")
		return &backgroundJobs{}
	}

	if !cfg.Jobs.EnableBackgroundJobs {
		log.Info("Background jobs disabled via config")
		return &backgroundJobs{}
	}

	runWorker := mode == "all" || mode == "worker"
	runScheduler := mode == "all" || mode == "scheduler"
	runMaintenance := mode == "all" || mode == "maintenance"

	log.Info("Background jobs mode", zap.String("mode", mode),
		zap.Bool("worker", runWorker),
		zap.Bool("scheduler", runScheduler),
		zap.Bool("maintenance", runMaintenance))

	var jobRunner *appjobs.Runner
	var jobScanner *sqlitejobs.Scanner
	var channelMon *monitor.ChannelMonitor
	var driveSyncSched *scheduler.DriveSyncScheduler
	var startJobRunner func()

	if runWorker {
		// Jobs system - Runner and Scanner. Reads from root.Jobs (PR4a).
		jobsSvc := root.Jobs.Service
		jobsDispatcher := root.Jobs.Dispatcher
		jobsRepo := root.Jobs.Repo
		if jobsSvc != nil && jobsDispatcher != nil && jobsRepo != nil {
			workers := cfg.Jobs.MaxParallelPerProject
			if workers <= 0 {
				workers = 1
			}
			leaseTTL := time.Duration(cfg.Jobs.LeaseTTLSeconds) * time.Second
			if leaseTTL <= 0 {
				leaseTTL = 5 * time.Minute
			}
			runnerConfig := appjobs.RunnerConfig{
				Workers:   workers,
				PollEvery: 2 * time.Second,
				LeaseTTL:  leaseTTL,
				JobTypes:  nil,
			}
			jobRunner = appjobs.NewRunner(jobsRepo, jobsDispatcher, log, runnerConfig)
			log.Info("Job runner created", zap.Int("workers", runnerConfig.Workers))

			jobScanner = sqlitejobs.NewScanner(jobsRepo, log)
			concurrent.SafeGo("job-scanner", func() { jobScanner.Start(ctx, 5*time.Minute) })
			log.Info("Job scanner started")

			appjobs.StartMetricsRefresher(ctx, jobsRepo, 30*time.Second, log)

			// Closure stored on backgroundJobs.startJobRunner (assigned at the
			// bottom of startBackgroundJobs) so WireServices can trigger
			// Dispatcher.Freeze() + JobRunner.Start AFTER WireRegistry has
			// registered all handlers. PR4d-final (June 2026) replaces
			// CoreDeps.startJobRunner with this field; the closure captures
			// jobRunner + root + ctx + cfg + log by reference.
			startJobRunnerClosure := func() {
				if jobRunner == nil || root == nil || root.Jobs == nil || root.Jobs.Dispatcher == nil {
					return
				}
				root.Jobs.Dispatcher.Freeze()
				concurrent.SafeGo("job-runner", func() { jobRunner.Start(ctx) })
				log.Info("Job runner started after full wiring",
					zap.Int("workers", cfg.Jobs.MaxParallelPerProject))
			}
			startJobRunner = startJobRunnerClosure
		}
	}

	if runScheduler {
		if cfg.Jobs.EnableChannelMonitor {
			var dbForChannels *sql.DB
			if dbs.main != nil && dbs.main.DB != nil {
				dbForChannels = dbs.main.DB
			}

			channelMon = monitor.NewChannelMonitor(cfg, root.Repos.ClipsRepo, log,
				root.Domains.YoutubeClipService, dbForChannels, root.AI.OllamaClient)

			if dbForChannels != nil {
				sqRepo := assets.NewSearchQueriesRepository(dbForChannels)
				channelMon.SetSearchQueriesRepo(sqRepo)
				log.Info("Search queries repo wired to channel monitor")
			}

			if cfg.Jobs.SearchRateLimit > 0 {
				channelMon.SetSearchRateLimit(cfg.Jobs.SearchRateLimit)
			}

			concurrent.SafeGo("channel-monitor", func() { channelMon.Start(ctx) })
			log.Info("Channel monitor started")
		}

		// Periodic Drive sync scheduler — always enabled if sync services exist
		if root.Sync.CatalogSync != nil || root.Domains.VoiceoverSync != nil || root.Domains.ImageService != nil {
			syncInterval := 6 * time.Hour
			if cfg.Jobs.CatalogSyncInterval != "" {
				if parsed, err := time.ParseDuration(cfg.Jobs.CatalogSyncInterval); err == nil {
					syncInterval = parsed
				}
			}
			driveSyncSched = scheduler.NewDriveSyncScheduler(
				root.Sync.CatalogSync,
				root.Domains.VoiceoverSync,
				root.Domains.ImageService,
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

		if root.Jobs.Service != nil {
			scheduleMaintenanceJob := func(interval time.Duration, label string) {
				concurrent.SafeGo("maintenance-scheduler-"+label, func() {
					select {
					case <-ctx.Done():
						return
					case <-time.After(2 * time.Minute):
					}
					for {
						_, err := root.Jobs.Service.Enqueue(ctx, &appjobs.EnqueueRequest{
							Type:     "system.cleanup",
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

	if runScheduler && root.Domains.YoutubeClipService != nil {
		concurrent.SafeGo("yt-cache-prewarm", func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			sCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := root.Domains.YoutubeClipService.PrewarmHotVideoMetadataCache(sCtx); err != nil {
				log.Warn("Failed to pre-warm YouTube video metadata cache", zap.Error(err))
			}
		})

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
					if err := root.Domains.YoutubeClipService.PrewarmHotVideoMetadataCache(sCtx); err != nil {
						log.Warn("Failed to run nightly pre-warming job for YouTube metadata", zap.Error(err))
					}
					cancel()
				}
			}
		})
	}

	// PR4.E — outbox events pool lifecycle. Construction is pure
	// (composition.go::BuildOutboxBundle no longer starts goroutines);
	// Start() is invoked here. Stop() is invoked explicitly from
	// shutdown.go::buildCleanup so graceful teardown does NOT rely on
	// outboxevents.Pool's internal ctx.Done handling (which may not
	// gracefully drain in-flight work). Note the nil guard: EventsPool
	// is nil if BuildOutboxBundle failed mid-fashion or was skipped.
	if root.Outbox != nil && root.Outbox.EventsPool != nil {
		evPool := root.Outbox.EventsPool
		concurrent.SafeGo("outbox-events-pool", func() { evPool.Start(ctx, 1) })
		log.Info("outbox events pool started by lifecycle (per PR4 no-goroutine-in-constructor)")
	}

	if runMaintenance {
		if root.Repos.ScriptsRepo != nil {
			concurrent.SafeGo("research-cache-sweeper", func() {
				startResearchCacheSweeper(ctx, root.Repos.ScriptsRepo, log)
			})
		}

		// PR4.A (June 2026): MemoryRepo relocated RepoBundle → AIBundle.
		// The single consumer (startGemmaMemorySweeper) reads root.AI.MemoryRepo
		// instead of root.Repos.MemoryRepo, reflecting the new ownership.
		if root.AI != nil && root.AI.MemoryRepo != nil {
			concurrent.SafeGo("gemma-memory-sweeper", func() {
				startGemmaMemorySweeper(ctx, root.AI.MemoryRepo, log)
			})
		}

		if root.Process.VectorSvc != nil && root.Drive.DriveUploader != nil {
			concurrent.SafeGo("qdrant-cleaner", func() {
				startQdrantCleaner(ctx, root.Process.VectorSvc, root.Drive.DriveUploader, log)
			})
		}

		if root.Repos.ClipsRepo != nil {
			concurrent.SafeGo("clip-dedup-sweeper", func() {
				log.Info("clip dedup sweeper starting (interval=30m)")
				startClipDedupSweeper(ctx, root.Repos.ClipsRepo, log)
			})
		}

		if root.Domains.AutotagService != nil {
			concurrent.SafeGo("vlm-autotag-sweeper", func() {
				log.Info("VLM auto-tag sweeper starting (interval=15m)")
				startVLMAutoTagSweeper(ctx, root.Domains.AutotagService, log)
			})
		}

		if root.Process.VectorSvc != nil && dbs.main != nil && dbs.main.DB != nil {
			concurrent.SafeGo("qdrant-ghost-sweeper", func() {
				db := dbs.main.DB
				log.Info("Qdrant ghost-points sweeper starting (interval=24h, initialDelay=10m)")
				startQdrantGhostSweeper(ctx, root.Process.VectorSvc, db, log)
			})
		}
	}

	if root.Process.VectorSvc != nil {
		concurrent.SafeGo("qdrant-health-monitor", func() {
			log.Info("Qdrant health monitor starting (interval=60s)")
			startQdrantHealthMonitor(ctx, root.Process.VectorSvc, log)
		})
	}

	return &backgroundJobs{
		channelMonitor:    channelMon,
		driveSyncSchedule: driveSyncSched,
		jobRunner:         jobRunner,
		jobScanner:        jobScanner,
		scriptsRepo:       root.Repos.ScriptsRepo,
		startJobRunner:    startJobRunner,
	}
}

func startResearchCacheSweeper(ctx context.Context, repo *sqlitescripts.ScriptRepository, log *zap.Logger) {
	const (
		initialDelay = 30 * time.Second
		interval     = 6 * time.Hour
		maxAgeDays   = 30
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
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

func startQdrantHealthMonitor(ctx context.Context, vectorSvc *qdrant.Service, log *zap.Logger) {
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

func startClipDedupSweeper(ctx context.Context, clipsRepo *assets.ClipsRepository, log *zap.Logger) {
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

func runDedupSweep(ctx context.Context, clipsRepo *assets.ClipsRepository, log *zap.Logger) (int, error) {
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

func startQdrantCleaner(ctx context.Context, vectorSvc *qdrant.Service, driveUploader *drive.Uploader, log *zap.Logger) {
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

type ghostSweepable interface {
	ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error
	DeletePoints(ctx context.Context, assetIDs []string) error
}

func startQdrantGhostSweeper(ctx context.Context, vectorSvc *qdrant.Service, db *sql.DB, log *zap.Logger) {
	const (
		initialDelay    = 10 * time.Minute
		interval        = 24 * time.Hour
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

	for i := 0; i < len(ghosts); i += 100 {
		end := i + 100
		if end > len(ghosts) {
			end = len(ghosts)
		}
		if err := qdrant.DeletePoints(ctx, ghosts[i:end]); err != nil {
			return i, fmt.Errorf("delete ghosts %d-%d: %w", i, end, err)
		}
	}

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
func NewLifecycleFromDeps(
	deps *LifecycleDeps,
	log *zap.Logger,
) *lifecycle.Service {
	if deps.DriveVerifier == nil && deps.DriveClient != nil {
		deps.DriveVerifier = drive.NewDriveVerifierAdapter(deps.DriveClient)
	}

	if deps.Finalizer == nil && deps.Registry != nil && deps.DriveVerifier != nil && deps.AssetIndex != nil {
		deps.Finalizer = artifacts.NewFinalizerWithAssetIndex(
			deps.Registry,
			deps.DriveVerifier,
			deps.AssetIndex,
			log,
		)
	}

	if deps.Store == nil && deps.Registry != nil {
		deps.Store = lifecycle.NewRegistryStoreAdapter(deps.Registry)
	}

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
