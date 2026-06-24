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
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	_ "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// StartupStep defines a service that the server lifecycle manages.
// Steps are executed in declaration order by serverLifecycle.Start.
// Required steps that fail abort the sequence; optional failures are
// logged and exposed but do not block the remaining steps.
//
// Stop is invoked in reverse order during serverLifecycle.Stop.
// For goroutine-based services that listen on ctx.Done(), Stop is a
// no-op (context cancellation signals them). For services with explicit
// shutdown methods (channel monitor, outbox pool), Stop calls those.
type StartupStep struct {
	Name     string
	Required bool
	Start    func(ctx context.Context) error
	Stop     func(ctx context.Context) error
}

// backgroundJobs holds references to services started by startBackgroundJobs
// that need explicit Stop() during shutdown, plus the startup plan that
// defers ALL goroutine launches to serverLifecycle.Start.
//
// After lifecycle-runtime-ownership (June 2026), only channelMonitor needs
// explicit Stop (via buildCleanup in shutdown.go). All other services
// (job runner, scanner, sweepers) stop via context cancellation.
// The startupPlan field replaces the previous startJobRunner closure —
// ALL background workers, scanners, monitors, sweepers, and the job runner
// now flow through the plan so zero goroutines start during composition.
type backgroundJobs struct {
	channelMonitor *monitor.ChannelMonitor
	// startupPlan is the ordered list of services to start during
	// serverLifecycle.Start. The job runner is the last required step.
	// WireServices reads this field to construct the lifecycle.
	startupPlan []StartupStep
}

// startBackgroundJobs creates the per-mode background workers and returns a
// startup plan WITHOUT launching any goroutines. All runtime startups are
// deferred to serverLifecycle.Start via the returned StartupStep list.
//
// The startup plan ordering (left-to-right):
//  1. Job scanner (optional)
//  2. Metrics refresher (optional)
//  3. Channel monitor (optional)
//  4. Maintenance schedulers (optional)
//  5. YouTube cache prewarm + nightly (optional)
//  6. Research cache sweeper (optional)
//  7. Gemma memory sweeper (optional)
//  8. Qdrant stale cleaner (optional)
//  9. Clip dedup sweeper (optional)
//
// 10. VLM auto-tag sweeper (optional)
// 11. Qdrant ghost sweeper (optional)
// 12. Qdrant health monitor (optional)
// 13. Job runner (REQUIRED, always last)
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
	var steps []StartupStep

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

			// Job scanner: optional background service.
			sc := jobScanner
			steps = append(steps, StartupStep{
				Name: "job-scanner", Required: false,
				Start: func(startCtx context.Context) error {
					concurrent.SafeGo("job-scanner", func() { sc.Start(startCtx, 5*time.Minute) })
					log.Info("Job scanner started")
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})

			// Metrics refresher: optional background service.
			jr := jobsRepo
			steps = append(steps, StartupStep{
				Name: "metrics-refresher", Required: false,
				Start: func(startCtx context.Context) error {
					appjobs.StartMetricsRefresher(startCtx, jr, 30*time.Second, log)
					log.Info("Metrics refresher started")
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}
	}

	if runScheduler {
		if cfg.Jobs.EnableChannelMonitor {
			channelMon = monitor.NewChannelMonitor(cfg, root.Repos.ClipsRepo, log,
				root.Domains.YoutubeClipService, root.DB.DB, root.AI.OllamaClient)

			// Channel monitor uses the primary *sql.DB internally,
			// already exposed via root.DB. Repo wiring happens against
			// the same handle, no separate plumbing needed (PG-011).
			sqRepo := assets.NewSearchQueriesRepository(root.DB.DB)
			channelMon.SetSearchQueriesRepo(sqRepo)
			log.Info("Search queries repo wired to channel monitor")

			if cfg.Jobs.SearchRateLimit > 0 {
				channelMon.SetSearchRateLimit(cfg.Jobs.SearchRateLimit)
			}

			// Channel monitor: optional background service.
			cm := channelMon
			steps = append(steps, StartupStep{
				Name: "channel-monitor", Required: false,
				Start: func(startCtx context.Context) error {
					concurrent.SafeGo("channel-monitor", func() { cm.Start(startCtx) })
					log.Info("Channel monitor started")
					return nil
				},
				Stop: func(_ context.Context) error { cm.Stop(); return nil },
			})
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
			jsvc := root.Jobs.Service
			makeSchedulerStep := func(interval time.Duration, label string) StartupStep {
				return StartupStep{
					Name: "maintenance-scheduler-" + label, Required: false,
					Start: func(startCtx context.Context) error {
						concurrent.SafeGo("maintenance-scheduler-"+label, func() {
							select {
							case <-startCtx.Done():
								return
							case <-time.After(2 * time.Minute):
							}
							for {
								_, err := jsvc.Enqueue(startCtx, &job.EnqueueRequest{
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
								case <-startCtx.Done():
									return
								case <-time.After(interval):
								}
							}
						})
						log.Info("scheduled maintenance job", zap.String("label", label), zap.Duration("interval", interval))
						return nil
					},
					Stop: func(_ context.Context) error { return nil },
				}
			}
			steps = append(steps, makeSchedulerStep(maintenanceInterval, "maintenance"))
			steps = append(steps, makeSchedulerStep(backupInterval, "backup"))
			log.Info("scheduled maintenance and backup jobs via jobs system",
				zap.Duration("maintenance_interval", maintenanceInterval),
				zap.Duration("backup_interval", backupInterval))
		} else {
			log.Warn("jobs service not available, skipping scheduled maintenance/backup")
		}
	}

	if runScheduler && root.Domains.YoutubeClipService != nil {
		ytSvc := root.Domains.YoutubeClipService
		_ = ytSvc // silence unused-var; late-bound to yt-cache-prewarm below
		steps = append(steps, StartupStep{
			Name: "yt-cache-prewarm", Required: false,
			Start: func(startCtx context.Context) error {
				// Phase 2 followup (June 2026): PrewarmHotVideoMetadataCache was removed
				// when the metadata flow moved to the ytmetadata capability service.
				// Logging the disabled prewarm is loud so operators see the gap;
				// restoring the cache warming requires wiring the metadata capability
				// service's cache loader (Phase 2+ follow-up).
				if ytSvc != nil {
					log.Info("yt-cache-prewarm: disabled pending Phase 2+ follow-up (ytmetadata capability cache loader not yet exposed to *youtube.Service)")
				}
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
		steps = append(steps, StartupStep{
			Name: "yt-nightly-prewarm", Required: false,
			Start: func(startCtx context.Context) error {
				// Phase 2 followup (June 2026): see note above in yt-cache-prewarm.
				if ytSvc != nil {
					log.Info("yt-nightly-prewarm: disabled pending Phase 2+ follow-up")
				}
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	if runMaintenance {
		if root.Repos.ScriptsRepo != nil {
			repo := root.Repos.ScriptsRepo
			steps = append(steps, StartupStep{
				Name: "research-cache-sweeper", Required: false,
				Start: func(startCtx context.Context) error {
					concurrent.SafeGo("research-cache-sweeper", func() {
						startResearchCacheSweeper(startCtx, repo, log)
					})
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}

		if root.AI != nil && root.AI.MemoryRepo != nil {
			mrepo := root.AI.MemoryRepo
			steps = append(steps, StartupStep{
				Name: "gemma-memory-sweeper", Required: false,
				Start: func(startCtx context.Context) error {
					concurrent.SafeGo("gemma-memory-sweeper", func() {
						startGemmaMemorySweeper(startCtx, mrepo, log)
					})
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}

		if root.Process.VectorSvc != nil && root.Drive.DriveUploader != nil {
			vs := root.Process.VectorSvc
			up := root.Drive.DriveUploader
			steps = append(steps, StartupStep{
				Name: "qdrant-cleaner", Required: false,
				Start: func(startCtx context.Context) error {
					concurrent.SafeGo("qdrant-cleaner", func() {
						startQdrantCleaner(startCtx, vs, up, log)
					})
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}

		if root.Repos.ClipsRepo != nil {
			cr := root.Repos.ClipsRepo
			steps = append(steps, StartupStep{
				Name: "clip-dedup-sweeper", Required: false,
				Start: func(startCtx context.Context) error {
					log.Info("clip dedup sweeper starting (interval=30m)")
					concurrent.SafeGo("clip-dedup-sweeper", func() {
						startClipDedupSweeper(startCtx, cr, log)
					})
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}

		if root.Domains.AutotagService != nil {
			at := root.Domains.AutotagService
			steps = append(steps, StartupStep{
				Name: "vlm-autotag-sweeper", Required: false,
				Start: func(startCtx context.Context) error {
					log.Info("VLM auto-tag sweeper starting (interval=15m)")
					concurrent.SafeGo("vlm-autotag-sweeper", func() {
						startVLMAutoTagSweeper(startCtx, at, log)
					})
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}

		if root.Process.VectorSvc != nil && root.Repos.ClipsRepo != nil {
			vs := root.Process.VectorSvc
			cr := root.Repos.ClipsRepo
			steps = append(steps, StartupStep{
				Name: "qdrant-ghost-sweeper", Required: false,
				Start: func(startCtx context.Context) error {
					log.Info("Qdrant ghost-points sweeper starting (interval=24h, initialDelay=10m)")
					concurrent.SafeGo("qdrant-ghost-sweeper", func() {
						startQdrantGhostSweeper(startCtx, vs, cr, log)
					})
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}
	}

	if root.Process.VectorSvc != nil {
		vs := root.Process.VectorSvc
		steps = append(steps, StartupStep{
			Name: "qdrant-health-monitor", Required: false,
			Start: func(startCtx context.Context) error {
				log.Info("Qdrant health monitor starting (interval=60s)")
				concurrent.SafeGo("qdrant-health-monitor", func() {
					startQdrantHealthMonitor(startCtx, vs, log)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Job runner: REQUIRED, always LAST in the plan.
	// The closure freezes the Dispatcher so no further handlers can
	// register once the runner starts claiming jobs. It is invoked
	// AFTER all other services are up and WireRegistry has completed.
	if jobRunner != nil && root.Jobs.Dispatcher != nil {
		jr := jobRunner
		disp := root.Jobs.Dispatcher
		steps = append(steps, StartupStep{
			Name: "job-runner", Required: true,
			Start: func(startCtx context.Context) error {
				disp.Freeze()
				concurrent.SafeGo("job-runner", func() { jr.Start(startCtx) })
				log.Info("Job runner started after full wiring",
					zap.Int("workers", cfg.Jobs.MaxParallelPerProject))
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	return &backgroundJobs{
		channelMonitor: channelMon,
		startupPlan:    steps,
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

func startQdrantGhostSweeper(ctx context.Context, vectorSvc *qdrant.Service, clipsRepo *assets.ClipsRepository, log *zap.Logger) {
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
		deleted, err := runGhostSweep(sCtx, vectorSvc, clipsRepo, scrollBatchSize, sqlitePageSize, log)
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

// runGhostSweep loads `clipsRepo`'s media_assets ids (handled via the
// typed StreamAssetIDs port) into a set, scrolls Qdrant, deletes any
// ghost points that no longer have a corresponding SQLite row. The
// pagination, ctx honoring, and per-batch error semantics mirror the
// previous raw-SQL implementation exactly.
func runGhostSweep(ctx context.Context, qdrant ghostSweepable, clipsRepo *assets.ClipsRepository, scrollBatchSize, sqlitePageSize int, log *zap.Logger) (int, error) {
	if qdrant == nil {
		return 0, fmt.Errorf("qdrant store is nil")
	}
	if clipsRepo == nil {
		return 0, fmt.Errorf("clips repo is nil")
	}
	if scrollBatchSize <= 0 {
		scrollBatchSize = 500
	}
	if sqlitePageSize <= 0 {
		sqlitePageSize = 1000
	}

	sqliteIDs := make(map[string]struct{}, 8192)
	if err := clipsRepo.StreamAssetIDs(ctx, sqlitePageSize, func(batch []string) error {
		for _, id := range batch {
			sqliteIDs[id] = struct{}{}
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("stream asset ids via clipsRepo: %w", err)
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
