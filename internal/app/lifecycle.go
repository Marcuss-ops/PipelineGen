// Package app — background lifecycle (PR4: takes *ComposeRoot).
//
// Before PR4 this file took the legacy `*services` struct. After PR4 it
// takes *ComposeRoot (the per-bundle decomposition). The body is the same
// `startBackgroundJobs(ctx, cfg, dbs, root, log, mode) (*backgroundJobs)`
// pattern as before, but reads from root.Domains, root.Repos, root.Process,
// root.Outbox, root.Jobs, root.Domains.RealtimeService, etc.
//
// PR4.8 (June 2026): the typed job-runner lifecycle (construction +
// START/STOP closure) was extracted to internal/app/lifecycle_job_runner.go
// (buildJobRunner + buildJobRunnerStep). startBackgroundJobs is now
// orchestrator-only — the "var jobRunner := appjobs.NewRunner(...) +
// inline StartupStep literal" pattern is replaced by a single typed
// append at the end of the plan. See Wave 15 pending #2 in
// architecture/current.yaml for the canonical record.
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
	deletionreconciler "github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion/reconciler"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	semantic "github.com/Marcuss-ops/PipelineGen/internal/application/semantic"
	transcripts "github.com/Marcuss-ops/PipelineGen/internal/application/transcripts"
	monitoradapter "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/adapters/monitoradapter"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/deletion"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
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
//
// The startupPlan field encodes every background worker, scanner, monitor,
// sweeper, and the job runner as a StartupStep so zero goroutines start
// during composition. The job-runner StartupStep is built by
// internal/app/lifecycle_job_runner.go::buildJobRunnerStep (PR4.8) and
// is appended LAST in the plan (asserted by TestLifecycle_JobRunnerLast
// in internal/app/lifecycle_test.go).
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
// Three Qdrant-driven background steps were removed:
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

	var jobScanner *sqlitejobs.Scanner
	var channelMon *monitor.ChannelMonitor
	var steps []StartupStep

	if runWorker {
		// Jobs system - Runner and Scanner. Reads from root.Jobs (PR4a).
		// PR4.8 (June 2026): the jobRunner construction was extracted
		// to internal/app/lifecycle_job_runner.go::buildJobRunner, and
		// the "job-runner" StartupStep is appended at the end of
		// startBackgroundJobs via buildJobRunnerStep. The scanner +
		// metrics refresher stay here as before — they only need the
		// jobs.Store (*sqljobs.SQLiteStore), so the gate collapses to
		// `jobsRepo != nil`.
		jobsRepo := root.Jobs.Repo
		if jobsRepo != nil {
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

		// Voiceover parent aggregator (Step 4 / micro-commit #5, June 2026):
		// re-finalises parent jobs once all children have reached terminal
		// status. MUST live under runWorker (NOT runScheduler) because the
		// child job's terminal status only transitions when the job runner
		// processes it — placing the aggregator under runScheduler would
		// orphan parents on mode=worker machines (no aggregator ticks).
		if root.Jobs.Service != nil {
			voAgg := voiceoverjobs.NewParentAggregator(voiceoverjobs.AggregatorDeps{
				JobsSvc:      root.Jobs.Service,
				Logger:       log,
				PollInterval: 30 * time.Second,
			})
			steps = append(steps, StartupStep{
				Name: "voiceover-parent-aggregator", Required: false,
				Start: func(startCtx context.Context) error {
					voAgg.Start(startCtx)
					log.Info("Voiceover parent aggregator started (interval=30s)")
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}

		// Script parent aggregator (Commit 4 P0 #4 audit, July 2026):
		// lifecycle-owns the script.generate parent aggregator with the
		// server's runtime context (signal.NotifyContext). Previously
		// started during composition with context.Background() — the
		// goroutine had no shutdown signal and leaked on re-composition.
		// Mirrors the voiceover-parent-aggregator pattern above.
		if root.Jobs.Service != nil {
			scriptAgg := scriptjobs.NewScriptParentAggregator(scriptjobs.ScriptAggregatorDeps{
				JobsSvc:      root.Jobs.Service,
				Logger:       log,
				PollInterval: 30 * time.Second,
			})
			steps = append(steps, StartupStep{
				Name: "script-parent-aggregator", Required: false,
				Start: func(startCtx context.Context) error {
					scriptAgg.Start(startCtx)
					log.Info("Script parent aggregator started (interval=30s)")
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}
	}

	if runScheduler {

		if cfg.Jobs.EnableChannelMonitor {
			// PR 2 (June 2026): channels are loaded exclusively from
			// category_channels via channels.Service. The raw *sql.DB is
			// replaced by the canonical channels service which is the
			// single source of truth for channel configuration.
			channelsSvc := channels.NewService(
				channels.NewRepositoryAdapter(assets.NewChannelsRepository(root.DB.DB)),
				log,
			)
			// Step 9 commit 2 (June 2026): wire the concrete YTDLPSubtitleAdapter
			// (os/exec + VTT regex) and OllamaAnalyzer (Score + Classify +
			// FindSegments) as the monitor's Transcript + Analyzer ports.
			//
			// Step 9 follow-up (commit pending, June 2026): wire the concrete
			// ExtractionEnqueuer (jobs.Service + channels.Service binding) as
			// the monitor's Enqueuer port. Port placements match the Blocco 6
			// "external concern packages stay siblings" rule — YTDLPSubtitle
			// (yt-dlp subprocess) and OllamaAnalyzer (Ollama HTTP) live as
			// siblings; ExtractionEnqueuer (internal-to-internal binding) lives
			// inside monitor to avoid the monitor↔jobs import cycle. See
			// internal/application/assets/monitor/extraction_enqueuer.go for the
			// architectural note.
			ytdlpForSubtitles := downloader.NewYTDLP(cfg)
			ytdlpSubtitleAdapter := transcripts.NewYTDLPSubtitleAdapter(transcripts.Deps{
				Ytdlp: ytdlpForSubtitles,
				Log:   log,
			})
			ollamaAnalyzer := semantic.NewOllamaAnalyzer(semantic.Deps{
				OllamaClient:    root.AI.OllamaClient,
				Subtitles:       ytdlpSubtitleAdapter,
				Log:             log,
				Model:           cfg.External.OllamaModel,
				DataDir:         cfg.Storage.DataDir,
				DefaultCategory: "general",
			})

			channelMon = monitor.NewChannelMonitor(monitor.CompositionDeps{
				Cfg:         cfg,
				ChannelsSvc: channelsSvc,
				Log:         log,
				// Ytdlp wires the concrete *downloader.YTDLPDownloader so
				// monitor/discovery.go::discoverChannelVideos can call
				// ListChannel per scheduler tick. Same instance is re-used in
				// transcripts/YTDLPSubtitleAdapter for the subtitle
				// subprocess, keeping a single downloader binary+cookies
				// config across the two adapters.
				Ytdlp:      newMonitorYtdlpAdapter(ytdlpForSubtitles),
				Transcript: ytdlpSubtitleAdapter,
				Analyzer:   ollamaAnalyzer,
				Enqueuer:   monitoradapter.NewExtractionIntentAdapter(root.Jobs.Service, channelsSvc, log),
				// Commit 1/6 (PR-C-YouTube-Cutover, June 2026): the per-video
				// discovery ledger (TryReserve + MarkEnqueued + MarkRejected +
				// MaxDiscoveredAt) is now wired from the canonical
				// *assets.YoutubeDiscoveriesRepository. NewChannelMonitor
				// panics on nil Discoveries when Cfg is wired (per the fail-
				// fast guard added in scheduler.go alongside this commit),
				// so a wiring gap surfaces at boot rather than at first
				// scheduler tick.
				Discoveries: assets.NewYoutubeDiscoveriesRepository(root.DB.DB),
			})

			// Channel monitor: optional background service.
			cm := channelMon
			steps = append(steps, StartupStep{
				Name: "channel-monitor", Required: false,
				Start: func(startCtx context.Context) error {
					concurrent.SafeGo("channel-monitor", func() { cm.Start(startCtx) })
					log.Info("Channel monitor started")
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
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

		// Qdrant-cleaner step removed.

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

		// ── Deletion Reconciler (Blocco 3.2 commit 2/2, June 2026) ──
		// Periodic ticker that re-emits the canonical outbox event
		// for any media_assets row stuck in {DELETE_REQUESTED,
		// DRIVE_DELETE_PENDING, INDEX_DELETE_PENDING} past the
		// configurable stuck threshold. Configured via
		// cfg.Jobs.DeletionReconcilerInterval + DeletionReconcilerStuckThreshold
		// (default 15m tick + 30min threshold).
		//
		// Wire condition: requires (a) the SQLite database handle +
		// (b) the outbox.Dispatcher + (c) the deletion-scanner adapter.
		// Production wiring always supplies all three; partial wires
		// log a WARN + skip the step (mirrors the clip-dedup-sweeper
		// above).
		if root.DB != nil && root.DB.DB != nil {
			reconcilerInterval := 15 * time.Minute
			if cfg.Jobs.DeletionReconcilerInterval != "" {
				if parsed, err := time.ParseDuration(cfg.Jobs.DeletionReconcilerInterval); err == nil && parsed > 0 {
					reconcilerInterval = parsed
				}
			}
			reconcilerThreshold := 30 * time.Minute
			if cfg.Jobs.DeletionReconcilerStuckThreshold != "" {
				if parsed, err := time.ParseDuration(cfg.Jobs.DeletionReconcilerStuckThreshold); err == nil && parsed > 0 {
					reconcilerThreshold = parsed
				}
			}

			// Look up the outbox.Dispatcher via root.Outbox; if absent
			// (e.g. partial deploy), log WARN + skip.
			if root.Outbox != nil && root.Outbox.Dispatcher != nil {
				disp := root.Outbox.Dispatcher
				stuckScanner := deletion.NewScanner(root.DB.DB, 100)
				// Metrics adapter: deletion.ReconcilerMetricsAdapter
				// (defined in internal/infrastructure/database/sqlite/
				// deletion/metrics_adapter.go) is the canonical Pattern
				// 0 concrete for the application-layer
				// reconciler.Metrics port. The observability package's
				// package-level Prometheus counters are referenced
				// indirectly through this adapter.
				recSvc := deletionreconciler.NewServiceFromDeps(deletionreconciler.ServiceDeps{
					Scanner:          stuckScanner,
					OutboxEnqueuer:   disp,
					Metrics:          deletion.ReconcilerMetricsAdapter{},
					DefaultInterval:  reconcilerInterval,
					DefaultThreshold: reconcilerThreshold,
					Log:              log,
				})

				steps = append(steps, StartupStep{
					Name: "deletion-reconciler", Required: false,
					Start: func(startCtx context.Context) error {
						log.Info("deletion-reconciler starting (interval=15m)",
							zap.Duration("interval", reconcilerInterval),
							zap.Duration("threshold", reconcilerThreshold),
						)
						concurrent.SafeGo("deletion-reconciler", func() {
							recSvc.Run(startCtx)
						})
						return nil
					},
					Stop: func(_ context.Context) error { return nil },
				})
			} else {
				log.Warn("deletion-reconciler skipped: outbox.Dispatcher not wired (composition-root partial deploy)")
			}
		} else {
			log.Warn("deletion-reconciler skipped: root.DB.DB not wired")
		}

		// ── Orphan Sweeper (P0 #4 commit B/2, July 2026) ──
		// Periodic ticker that compensates for partial-failures in
		// the upload_intents table. Per the A/2 use case contract,
		// Steps 4 (ProjectFinalizer) + 4.5 (MarkFinalized) leave the
		// row at 'uploaded' on failure so this sweeper can detect +
		// recover Drive-side orphans on the next tick.
		//
		// Compensates stale 'uploaded' rows via Drive.Trash +
		// MarkFailed (CONSERVATIVE: trash, NOT permanent delete —
		// operators can recover within Drive's 30-day trash retention);
		// stale 'pending' rows via MarkFailed only (no Drive file
		// existed yet, no Drive action).
		//
		// Wiring gates: (a) root.DB.DB != nil — *scripts.UploadIntentsRepository
		// construction requires a *sql.DB handle; (b) root.Drive.Lifecycle != nil —
		// the DriveTrash port (Pattern 0 narrow interface in
		// internal/application/voiceover/orphan_sweeper.go) is satisfied
		// by *drive.FileLifecycleAdapter via structural conformance.
		// Either gate absent → log warn + skip the step (mirrors the
		// deletion-reconciler's partial-deploy safety net).
		if root.DB != nil && root.DB.DB != nil && root.Drive != nil && root.Drive.Lifecycle != nil {
			uploadRepo := scripts.NewUploadIntentsRepository(root.DB.DB)
			driveLifecycle := root.Drive.Lifecycle
			orphanSweeper := voiceover.NewOrphanSweeper(voiceover.OrphanSweeperDeps{
				// Repo: scripts.UploadIntentsRepository emits
				// *scripts.InsertUploadIntentOptions on InsertTx, but
				// voiceover.UploadIntentsRepository expects
				// *voiceover.UploadIntentInsertOptions (same field
				// shape, different package name). Inline adapter
				// converts between the two without leaking the
				// infra option type into the voiceover package.
				Repo:         newUploadIntentsAdapter(uploadRepo),
				DriveDeleter: driveLifecycle, // *drive.FileLifecycleAdapter satisfies OrphanDriveDeleter (Trash method)
				Logger:       log,
				Metrics: &voiceover.Metrics{
					Runs:       observability.OrphanSweeperRunsTotal,
					Reconciled: observability.OrphanSweeperReconciledTotal,
				},
				Tick:        10 * time.Minute, // per user-spec "default 10 min via config"
				PendingTTL:  30 * time.Minute, // per spec: pending rows timed out after 30m
				UploadedTTL: 60 * time.Minute, // per spec: Drive-backed orphans recovered after 60m
			})
			sw := orphanSweeper
			steps = append(steps, StartupStep{
				Name:     "orphan-sweeper",
				Required: false,
				Start: func(startCtx context.Context) error {
					log.Info("orphan-sweeper starting (interval=10m, pendingTTL=30m, uploadedTTL=60m)")
					concurrent.SafeGo("orphan-sweeper", func() {
						sw.Run(startCtx)
					})
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		} else {
			log.Warn("orphan-sweeper skipped: root.DB.DB or root.Drive.Lifecycle not wired (composition-root partial deploy)")
		}

		// Qdrant-ghost-sweeper step removed.
	}

	// Job runner: REQUIRED, always LAST in the plan.
	// Construction + step closure extracted to buildJobRunnerStep
	// (internal/app/lifecycle_job_runner.go, PR4.8). The frozen dispatcher
	// guarantees no further handlers can register once Start is invoked;
	// the runner exits via context cancellation in serverLifecycle.Stop.
	// Returns nil when the jobs bundle lacks Service / Dispatcher / Repo
	// (partial-deploy safety net): the runner is then skipped, not failed.
	if step := buildJobRunnerStep(jobRunnerDeps{root: root, cfg: cfg, log: log}); step != nil {
		steps = append(steps, *step)
	}

	return &backgroundJobs{
		channelMonitor: channelMon,
		startupPlan:    steps,
	}
}

// LifecycleDeps holds the dependencies needed to create a lifecycle service.
//
// FASE 9 Step 7 (June 2026): DriveUploader (*drive.Uploader) migrated to
// DriveAdmin (drive.Admin port). Callers pass *drive.Uploader which satisfies
// drive.Admin structurally. NewLifecycleFromDeps extracts a drive.Reader via
// safe type-assertion for the verifier + reconcile services.
//
// F2.7 (June 2026): DriveAdmin REMOVED. Publisher (delivery.Publisher) is
// the canonical Pattern 0 port for Drive writes; DriveReader
// (drive.Reader) is the canonical read-side port for the reconcile
// service's DriveIsNotTrashed check. The composition root threads
// both directly — no unsafe type-assertion needed.
type LifecycleDeps struct {
	Registry      artifacts.Registry
	Publisher     delivery.Publisher
	AssetIndex    *assetindex.Service
	DriveVerifier artifacts.DriveVerifier
	DriveReader   drive.Reader
	Finalizer     *artifacts.Finalizer
	Store         lifecycle.AssetRecordStore
}

// NewLifecycleFromDeps creates a lifecycle Service using the provided dependencies.
//
// FASE 9 Step 7: DriveAdmin (drive.Admin) replaces the former *drive.Uploader.
// A drive.Reader is extracted via safe type-assertion for verifier + reconcile.
// All production callers pass *drive.Uploader which satisfies both interfaces.
//
// F2.7 (June 2026): DriveAdmin REMOVED. NewLifecycleFromDeps now takes
// Publisher (delivery.Publisher) + DriveReader (drive.Reader) — the
// application-layer holds ZERO references to the legacy drive.Admin
// port. lifecycle.Service uses Publisher for Drive writes (closes P0
// #7) and DriveReader for the read-only reconcile/verify surface.
func NewLifecycleFromDeps(
	deps *LifecycleDeps,
	log *zap.Logger,
) *lifecycle.Service {
	driveReader := deps.DriveReader

	if deps.DriveVerifier == nil && driveReader != nil {
		deps.DriveVerifier = drive.NewDriveVerifierAdapter(driveReader)
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
		deps.Publisher,
		driveReader,
		deps.Registry,
		deps.AssetIndex,
		deps.Finalizer,
		lifecycle.DefaultConfig(),
		log,
	)
}

// ── OutboxMonitorAdapter (moved from outbox_monitor_adapter.go, Phase 5 consolidation, June 2026) ──

type outboxMonitorAdapter struct {
	repo *outboxevents.Repository
}

var _ outbox.MonitorPort = (*outboxMonitorAdapter)(nil)

func newOutboxMonitorAdapter(repo *outboxevents.Repository) outbox.MonitorPort {
	if repo == nil {
		return nil
	}
	return &outboxMonitorAdapter{repo: repo}
}

func (a *outboxMonitorAdapter) CountByStatus(ctx context.Context, status string) (int64, error) {
	if a == nil || a.repo == nil {
		return 0, nil
	}
	return a.repo.CountByStatus(ctx, status)
}

func (a *outboxMonitorAdapter) ListPending(ctx context.Context) ([]outbox.EventDTO, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	events, err := a.repo.ListPending(ctx)
	if err != nil {
		return nil, err
	}
	dtos := make([]outbox.EventDTO, len(events))
	for i, e := range events {
		dtos[i] = outbox.EventDTO{
			ID:            e.ID,
			EventType:     e.EventType,
			AggregateID:   e.AggregateID,
			AggregateType: e.AggregateType,
			PayloadJSON:   e.PayloadJSON,
			Status:        e.Status,
			AttemptCount:  e.AttemptCount,
			MaxAttempts:   e.MaxAttempts,
			LastError:     e.LastError,
			EventKey:      e.EventKey,
			WorkerID:      e.WorkerID,
			LeaseID:       e.LeaseID,
			LeaseExpiry:   e.LeaseExpiry,
			CompletedAt:   e.CompletedAt,
			CreatedAt:     e.CreatedAt,
			UpdatedAt:     e.UpdatedAt,
		}
	}
	return dtos, nil
}

// ── inline adapters ─────────────────────────────────────────────
//
// The adapters below exist ONLY because the build has a few
// mid-refactoring type-shape mismatches between ports (declared in
// canonical cross-package locations) and the concrete adapters
// in the composition root. Each adapter:
//
//   - is defined inline at the composition root (no new shared
//     package surface, no leak into the application-layer domain);
//   - uses field-level assignment rather than any reflection or
//     unsafe casts (Go-strict, easy to audit);
//   - is documented with the upstream contract it satisfies so a
//     future port evolution (e.g. renaming InsertTx signatures)
//     can be tracked here as the canonical Bridge layer.

// monitorYtdlpAdapter wraps *downloader.YTDLPDownloader (the infra
// DTO producer) so the channel-monitor's typed port
// `monitor.MonitorDownloaderPort` (the domain DTO consumer) is
// satisfied. The only mismatch is the return-slice element type:
//   - downloader.VideoInfo  (infra DTO, includes downloader-internal fields)
//   - monitor.VideoInfo     (domain DTO, the monitor's canonical projection)
//
// Field names are stable; the wrapper maps ID + Title + Views +
// Duration verbatim. New infra fields are dropped on the floor —
// intentional: the monitor port surface is the canonical SSOT for
// "what the channel-monitor needs from the yt-dlp layer".
type monitorYtdlpAdapter struct {
	inner *downloader.YTDLPDownloader
}

// ListChannelVideos satisfies monitor.MonitorDownloaderPort. The
// request shape (downloader.ListChannelVideosRequest) is
// structurally identical across infra / domain so it is passed
// through verbatim.
func (a *monitorYtdlpAdapter) ListChannelVideos(ctx context.Context, req downloader.ListChannelVideosRequest) ([]monitor.VideoInfo, error) {
	if a == nil || a.inner == nil {
		return nil, nil
	}
	rawList, err := a.inner.ListChannelVideos(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]monitor.VideoInfo, len(rawList))
	for i, v := range rawList {
		out[i] = monitor.VideoInfo{
			ID:       v.ID,
			Title:    v.Title,
			Views:    v.Views,
			Duration: v.Duration,
		}
	}
	return out, nil
}

// Path satisfies monitor.MonitorDownloaderPort. Forwarded verbatim.
func (a *monitorYtdlpAdapter) Path() string {
	if a == nil || a.inner == nil {
		return ""
	}
	return a.inner.Path()
}

// newMonitorYtdlpAdapter constructs the inline ytdlp→monitor bridge.
// The composition root is the canonical caller; the adapter is
// private to this file so it cannot be reused outside the lifecycle
// path. Returns a valid monitor.MonitorDownloaderPort even when
// ytdlp is nil (no-op: ListChannelVideos returns nil, Path returns ""),
// matching the grace-no-crash contract of the pre-FASE ports.
func newMonitorYtdlpAdapter(ytdlp *downloader.YTDLPDownloader) monitor.MonitorDownloaderPort {
	return &monitorYtdlpAdapter{inner: ytdlp}
}

// Compile-time assertion: monitorYtdlpAdapter satisfies the
// canonical monitor MonitorDownloaderPort. Drift between the
// inline adapter and the canonical port surface is a build-time
// failure rather than a runtime panic.
var _ monitor.MonitorDownloaderPort = (*monitorYtdlpAdapter)(nil)

// uploadIntentsAdapter wraps *scripts.UploadIntentsRepository so the
// voiceover.OrphanSweeper's typed port
// `voiceover.UploadIntentsRepository` is satisfied. The only
// mismatch is InsertTx's options type:
//
//   - scripts.InsertUploadIntentOptions{ VoiceoverID, Attempts }
//   - voiceover.UploadIntentInsertOptions{ VoiceoverID, Attempts }
//
// Struct fields are stable and identical; the adapter re-binds
// them inline. No other Repository methods differ — all other
// methods (MarkUploaded / MarkFinalized / MarkCompleted /
// MarkFailed / ListPending / BeginTx) are inherited from the
// embedded *scripts.UploadIntentsRepository via Go's struct
// embedding promotion.
type uploadIntentsAdapter struct {
	*scripts.UploadIntentsRepository
}

// InsertTx satisfies voiceover.UploadIntentsRepository. The option
// struct is re-bound with identical field values so the SQLite
// repository sees the canonical infra type it expects.
func (a *uploadIntentsAdapter) InsertTx(ctx context.Context, tx *sql.Tx, opts *voiceover.UploadIntentInsertOptions) (int64, error) {
	if a == nil || a.UploadIntentsRepository == nil {
		return 0, fmt.Errorf("uploadIntentsAdapter: nil repository")
	}
	return a.UploadIntentsRepository.InsertTx(ctx, tx, &scripts.InsertUploadIntentOptions{
		VoiceoverID: opts.VoiceoverID,
		Attempts:    opts.Attempts,
	})
}

// ListPending satisfies voiceover.UploadIntentsRepository. The
// scripts.UploadIntent row type is converted element-wise to the
// voiceover.UploadIntent domain shape. UpdatedUnix is computed from
// UpdatedAt so the wire stays a single int64 (avoiding leaking
// time.Time into the application-layer port).
func (a *uploadIntentsAdapter) ListPending(ctx context.Context, olderThan time.Time) ([]voiceover.UploadIntent, error) {
	if a == nil || a.UploadIntentsRepository == nil {
		return nil, nil
	}
	rows, err := a.UploadIntentsRepository.ListPending(ctx, olderThan)
	if err != nil {
		return nil, err
	}
	out := make([]voiceover.UploadIntent, 0, len(rows))
	for _, r := range rows {
		out = append(out, voiceover.UploadIntent{
			ID:          r.ID,
			VoiceoverID: r.VoiceoverID,
			DriveFileID: r.DriveFileID,
			Status:      r.Status,
			Reason:      r.Reason,
			Attempts:    r.Attempts,
			UpdatedUnix: r.UpdatedAt.Unix(),
		})
	}
	return out, nil
}

// newUploadIntentsAdapter constructs the inline scripts→voiceover
// bridge. Returns a valid voiceover.UploadIntentsRepository even
// when repo is nil so partial-deploy paths log+skip cleanly.
func newUploadIntentsAdapter(repo *scripts.UploadIntentsRepository) voiceover.UploadIntentsRepository {
	if repo == nil {
		return nil
	}
	return &uploadIntentsAdapter{UploadIntentsRepository: repo}
}

// Compile-time assertion: uploadIntentsAdapter satisfies the
// canonical voiceover UploadIntentsRepository. Drift between the
// inline adapter and the canonical port surface is a build-time
// failure rather than a runtime panic.
var _ voiceover.UploadIntentsRepository = (*uploadIntentsAdapter)(nil)
