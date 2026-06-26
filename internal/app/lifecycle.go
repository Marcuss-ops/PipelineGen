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
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
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
//  8. Clip dedup sweeper (optional)
//  9. VLM auto-tag sweeper (optional)
//
// 10. Job runner (REQUIRED, always last)
//
// PG-034 (June 2026): three Qdrant-driven background steps were removed:
//   - qdrant-stale-cleaner
//   - qdrant-ghost-sweeper
//   - qdrant-health-monitor
//
// Qdrant is gone; its embeddings are now stored solely in SQLite
// (media_assets.embedding_json / transcript_embedding).
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

		// PG-034 (June 2026): Qdrant-cleaner step removed — Qdrant capability deleted.

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

		// PG-034 (June 2026): Qdrant-ghost-sweeper step removed — Qdrant capability deleted.
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
