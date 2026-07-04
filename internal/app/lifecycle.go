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
	"errors"
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

// ErrCapabilityDisabled is the typed sentinel surface for startup
// steps that are intentionally NOT running per operator-facing policy
// (e.g. capability pending a future phase, feature-flag gated off,
// dependency not yet wired). Per godlike/07 no-fake-availability:
// a step returning nil while loading NOTHING is a fake success —
// the operator's view of the system would otherwise omit the
// suppressed capability. Returning ErrCapabilityDisabled from a
// Required:false step preserves the "startup survives" semantics
// (server_lifecycle.Start's optional-failure branch in
// server_lifecycle.go log+continues on any non-nil error) while
// making the disabled state typed-typed-queryable via
// `errors.Is(step.Err(), ErrCapabilityDisabled)` from any caller
// wanting to enumerate disabled-at-startup capabilities.
//
// Wire shape: errors.New (godlike/07 typed-error contract —
// composable via fmt.Errorf("%w"), reachable via errors.Is from
// any wrapping context). Single surface, no typed-data envelope:
// the actionable context lives in the wrap message (e.g.
// "yt-cache-prewarm: capability disabled pending Phase 2+").
var ErrCapabilityDisabled = errors.New("lifecycle: capability disabled (operator-facing policy decision; the surrounding step is intentionally not running)")

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
				// Commit 1/6 (PR-C-YouTube-Cutover, June 2026) — wiring CLOSED
				// in Commit 2 (2026-07-04). The per-video discovery ledger
				// (TryReserve + MarkEnqueued + MarkRejected + MaxDiscoveredAt)
				// is wrapped in monitorDiscoveriesAdapter (struct-embeds
				// *assets.YoutubeDiscoveriesRepository + overrides DrainPending
				// / DrainDispatched + MarkEnqueued + MarkRejected + CommitEnqueue
				// for translations: []assets.OutboxEntry → []monitor.OutboxEntry
				// + assets.ErrStateConflict → monitor.ErrLedgerStateConflict
				// multi-%w wrap). Without the adapter wrap the raw repo's
				// DrainDispatched signature returns []assets.OutboxEntry which
				// does NOT match monitor.YoutubeDiscoveriesPort's
				// []monitor.OutboxEntry — vet surfaces this as a build error.
				// NewChannelMonitor panics on nil Discoveries when Cfg is
				// wired (per the fail-fast guard in monitor.go), so a wiring
				// gap surfaces at boot rather than at first scheduler tick.
				Discoveries: newMonitorDiscoveriesAdapter(assets.NewYoutubeDiscoveriesRepository(root.DB.DB)),
				// FASE 3.7 Commit 2 (2026-07-04): wire the canonical
				// *observability.ObservabilityMetricsRecorder so analyzer +
				// discovery call sites (m.metrics.IncVideosChecked etc.)
				// invoke the package-level Prometheus counters declared in
				// internal/infrastructure/observability/metrics_workers.go
				// WITHOUT a direct `internal/infrastructure/observability`
				// import in the monitor package — the adapter is the
				// composition-root bridge that keeps the layering
				// (application → infra) intact.
				MetricsRecorder: observability.NewObservabilityMetricsRecorder(
					observability.ChannelMonitorVideosChecked,
					observability.ChannelMonitorVideosWithSegments,
					observability.ChannelMonitorSegmentsFound,
					observability.ChannelMonitorSegmentsPerVideo,
				),
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
				// Per godlike/07 no-fake-availability: the disabled prewarm MUST surface
				// as the typed ErrCapabilityDisabled sentinel — NOT as `return nil` —
				// so server_lifecycle.Start's optional-failure branch (log+continue
				// on Required:false + err != nil) Warn-logs the typed error via
				// zap.Error with the canonical typed-error name. The server_lifecycle
				// log line carries both the step name (zap.String("step", step.Name))
				// AND the typed error message (zap.Error(err)) which IS the
				// diagnostic — the inner `log.Info` was redundant and removed per
				// godlike/07 minimum-blast-radius (one diagnostic surface per fact).
				// Restoring the cache warming requires wiring the metadata
				// capability service's cache loader (Phase 2+ follow-up).
				return fmt.Errorf("yt-cache-prewarm: %w", ErrCapabilityDisabled)
			},
			Stop: func(_ context.Context) error { return nil },
		})
		steps = append(steps, StartupStep{
			Name: "yt-nightly-prewarm", Required: false,
			Start: func(startCtx context.Context) error {
				// Phase 2 followup (June 2026): see note above in yt-cache-prewarm.
				// Per godlike/07 no-fake-availability: same typed-error contract as
				// yt-cache-prewarm above. Required:false keeps startup alive; the
				// typed ErrCapabilityDisabled surfaces the disabled state to the
				// server_lifecycle.Start Warn log (one surface per fact).
				return fmt.Errorf("yt-nightly-prewarm: %w", ErrCapabilityDisabled)
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
// satisfied. The mismatches are:
//   - request shape:  `downloader.ListChannelVideosRequest` (infra)
//     → `monitor.ListChannelVideosQuery` (domain)
//   - return slice:   `[]downloader.VideoInfo` → `[]monitor.VideoInfo`
//
// FASE 3.7 Commit 1b (2026-07-04): the request-shape translation is
// added because `monitor.MonitorDownloaderPort.ListChannelVideos`
// was migrated to `monitor.ListChannelVideosQuery` to drop the
// downloader import from `internal/application/assets/monitor/
// ports_downloader.go` (and the call sites in monitor/discovery.go).
// The composition root is the canonical bridge between the two
// request shapes — no monitor-side caller now needs to import
// `internal/infrastructure/downloader`.
//
// Field-level projection for the response is unchanged from the
// pre-FASE-3.7 shape: ID + Title + Views + Duration forwarded
// verbatim. New infra fields are dropped on the floor — intentional:
// the monitor port surface is the canonical SSOT for "what the
// channel-monitor needs from the yt-dlp layer", and the channel-
// monitor port determines which fields are surfaced.
type monitorYtdlpAdapter struct {
	inner *downloader.YTDLPDownloader
}

// ListChannelVideos satisfies monitor.MonitorDownloaderPort. The
// request shape is translated verbatim (struct field names are
// stable across both infra / domain shapes); the response-slice
// element is projected field-by-field as before.
func (a *monitorYtdlpAdapter) ListChannelVideos(ctx context.Context, query monitor.ListChannelVideosQuery) ([]monitor.VideoInfo, error) {
	if a == nil || a.inner == nil {
		return nil, nil
	}
	req := downloader.ListChannelVideosRequest{
		ChannelURL:  query.ChannelURL,
		DateAfter:   query.DateAfter,
		PlaylistEnd: query.PlaylistEnd,
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

// monitorDiscoveriesAdapter wraps *assets.YoutubeDiscoveriesRepository
// (the infra-side ledger DB producer) so the channel-monitor's typed
// port `monitor.YoutubeDiscoveriesPort` (the domain DTO / sentinel
// consumer) is satisfied.
//
// FASE 3.7 Commit 1b (2026-07-04): the mismatch is two-fold:
//   - Return slice (DrainPendingOutbox/DrainDispatched): infra returns
//     `[]assets.OutboxEntry` (the row-as-scanned shape from
//     `monitor_enqueue_outbox`); the monitor port expects
//     `[]monitor.OutboxEntry` (the monitor-canonical projection
//     declared in `internal/application/assets/monitor/types_dto.go`).
//   - Sentinel error (`MarkEnqueued` / `MarkRejected` /
//     `CommitEnqueueOutbox`): infra wraps `assets.ErrStateConflict`
//     (the infra-side state-precondition sentinel from
//     `youtube_discoveries_repository.go`); the monitor port's
//     callers pattern-match against `monitor.ErrLedgerStateConflict`
//     (the canonical application-layer sentinel).
//
// Per godlike/06 (one owner per fact) + godlike/07 (no-fake-
// availability) + the FASE 3.7 commitment (zero infra imports in
// `internal/application/assets/monitor/`), the canonical resolution
// is the composition-root adapter pattern: monitor owns canonical
// sentinels + DTOs locally; infra owns its own sentinels + row
// shapes; the ONLY point where both come together is the composition
// root where this adapter translates between them. This mirrors the
// `monitorYtdlpAdapter` precedent above for the downloader surface.
//
// Struct embedding (`*assets.YoutubeDiscoveriesRepository`) is used
// for methods that do NOT require translation (TryReserve,
// MaxDiscoveredAt, MarkOutboxDispatched, MarkOutboxFailed, and the
// outbox-pass-through case of CommitEnqueueOutbox — none of those
// return types or sentinels need re-mapping). Methods that DO require
// translation (DrainPendingOutbox, DrainDispatched, MarkEnqueued,
// MarkRejected) override the embedded methods explicitly. The
// compile-time assertion at the bottom of this block pins the
// port-surface coverage.
type monitorDiscoveriesAdapter struct {
	*assets.YoutubeDiscoveriesRepository
}

// DrainPendingOutbox translates `[]assets.OutboxEntry` →
// `[]monitor.OutboxEntry` (struct fields are stable: ID,
// DiscoveryID, IdempotencyKey, PayloadJSON, State, RetryCount,
// NextRetryAt — element-wise copy preserves order).
func (a *monitorDiscoveriesAdapter) DrainPendingOutbox(ctx context.Context, limit int, leaseID, leaseUntil string) ([]monitor.OutboxEntry, error) {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil, nil
	}
	rows, err := a.YoutubeDiscoveriesRepository.DrainPendingOutbox(ctx, limit, leaseID, leaseUntil)
	if err != nil {
		return nil, mapDiscoveriesErr(err)
	}
	out := make([]monitor.OutboxEntry, len(rows))
	for i, e := range rows {
		out[i] = monitor.OutboxEntry{
			ID:             e.ID,
			DiscoveryID:    e.DiscoveryID,
			IdempotencyKey: e.IdempotencyKey,
			PayloadJSON:    e.PayloadJSON,
			State:          e.State,
			RetryCount:     e.RetryCount,
			NextRetryAt:    e.NextRetryAt,
		}
	}
	return out, nil
}

// DrainDispatched translates `[]assets.OutboxEntry` →
// `[]monitor.OutboxEntry` (same element-wise copy as
// DrainPendingOutbox — both paths read the same `monitor_enqueue_outbox`
// row shape).
func (a *monitorDiscoveriesAdapter) DrainDispatched(ctx context.Context, limit int, leaseID, leaseUntil string) ([]monitor.OutboxEntry, error) {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil, nil
	}
	rows, err := a.YoutubeDiscoveriesRepository.DrainDispatched(ctx, limit, leaseID, leaseUntil)
	if err != nil {
		return nil, mapDiscoveriesErr(err)
	}
	out := make([]monitor.OutboxEntry, len(rows))
	for i, e := range rows {
		out[i] = monitor.OutboxEntry{
			ID:             e.ID,
			DiscoveryID:    e.DiscoveryID,
			IdempotencyKey: e.IdempotencyKey,
			PayloadJSON:    e.PayloadJSON,
			State:          e.State,
			RetryCount:     e.RetryCount,
			NextRetryAt:    e.NextRetryAt,
		}
	}
	return out, nil
}

// MarkEnqueued translates `assets.ErrStateConflict` →
// `monitor.ErrLedgerStateConflict` (multi-%w wrap chain — Go 1.20+).
// The original error's message is preserved as context so a
// downstream operator inspecting the error chain still sees the
// full message (e.g. "MarkEnqueued expected state IN
// ('pending','analyzing'), got 'rejected_terminal' for id=abc").
//
// The new structure is:
//
//	errors.Is(err, monitor.ErrLedgerStateConflict) == true
//	          (monitor-side pattern-match resolves correctly)
//
// AND for any future caller that wants to test BOTH the infra and
// monitor sentinels (e.g. a sub-packaged diagnostic tool), the
// original wrap chain is preserved via the second %w:
//
//	errors.Is(err, assets.ErrStateConflict) == true
//	          (infra-side SSOT still reachable through the chain)
func (a *monitorDiscoveriesAdapter) MarkEnqueued(ctx context.Context, id, enqueuedAt string) error {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil
	}
	return mapDiscoveriesErr(a.YoutubeDiscoveriesRepository.MarkEnqueued(ctx, id, enqueuedAt))
}

// MarkRejected translates `assets.ErrStateConflict` →
// `monitor.ErrLedgerStateConflict` (same multi-%w wrap shape as
// MarkEnqueued above).
func (a *monitorDiscoveriesAdapter) MarkRejected(ctx context.Context, id, rejectionReason string, retryable bool) error {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil
	}
	return mapDiscoveriesErr(a.YoutubeDiscoveriesRepository.MarkRejected(ctx, id, rejectionReason, retryable))
}

// CommitEnqueueOutbox translates `assets.ErrStateConflict` and
// `monitor_outbox.ErrDuplicateOutboxKey` (the latter is infra-side
// idempotency sentinel, not yet re-exported in monitor — the
// adapter just passes the error through with the SSOT wrap). The
// commit method is the only place that wraps both state-conflict
// errors AND duplicate-key errors, so the multi-%w chain handles
// both infra sentinels uniformly. Duplicate-key errors do NOT
// match `monitor.ErrLedgerStateConflict` (they are not state
// preconditions), so callers continue to treat them as
// idem­potent and not as terminal failures.
func (a *monitorDiscoveriesAdapter) CommitEnqueueOutbox(ctx context.Context, discoveryID, enqueuedAt, idempotencyKey, payloadJSON string) error {
	if a == nil || a.YoutubeDiscoveriesRepository == nil {
		return nil
	}
	return mapDiscoveriesErr(a.YoutubeDiscoveriesRepository.CommitEnqueueOutbox(ctx, discoveryID, enqueuedAt, idempotencyKey, payloadJSON))
}

// mapDiscoveriesErr is the canonical sentinel-translator between
// the infra-side `assets.ErrStateConflict` (the SQLite-row
// state-precondition failure SSOT in
// `internal/infrastructure/database/sqlite/assets`) and the
// monitor-side `monitor.ErrLedgerStateConflict` (the
// application-layer canonical sentinel in
// `internal/application/assets/monitor/types_dto.go`).
//
// nil → nil. errors.Is(err, assets.ErrStateConflict) → delegates
// to `monitor.TranslateLedgerSentinel` (the public monitor-package
// helper that does the actual multi-%w wrap). Any other error →
// passed through unchanged (so transient SQLite I/O errors and
// other non-state-conflict errors remain distinguishable in the
// caller-supplied retry taxonomy).
//
// The split between "detect" (this function, knows about
// assets.ErrStateConflict because only lifecycle.go has the infra
// import per FASE 3.7) and "wrap" (monitor.TranslateLedgerSentinel,
// knows nothing about infra, only the multi-%w semantic) keeps the
// adapter trivial: gate + delegate. The wrap semantic itself is
// unit-testable from the monitor package without any infra import.
func mapDiscoveriesErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, assets.ErrStateConflict) {
		return monitor.TranslateLedgerSentinel(err)
	}
	return err
}

// newMonitorDiscoveriesAdapter constructs the inline
// assets→monitor bridge. The composition root is the canonical
// caller; the adapter is private to this file so it cannot be
// reused outside the lifecycle path. Returns a valid
// monitor.YoutubeDiscoveriesPort even when repo is nil
// (no-op: every method surfaces nil-or-empty so a missing wire
// silently fails-soft in the same way the pre-adapter wiring
// did). nil repo is NOT a panic — it matches the partial-deploy
// safety pattern of the other composition-root adapters.
func newMonitorDiscoveriesAdapter(repo *assets.YoutubeDiscoveriesRepository) monitor.YoutubeDiscoveriesPort {
	if repo == nil {
		return nil
	}
	return &monitorDiscoveriesAdapter{YoutubeDiscoveriesRepository: repo}
}

// Compile-time assertion: monitorDiscoveriesAdapter satisfies the
// canonical monitor YoutubeDiscoveriesPort. Drift between the
// inline adapter and the canonical port surface is a build-time
// failure rather than a runtime panic.
var _ monitor.YoutubeDiscoveriesPort = (*monitorDiscoveriesAdapter)(nil)

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

// Compile-time assertion (FASE 3.7 Commit 2, 2026-07-04):
// *observability.ObservabilityMetricsRecorder satisfies the
// canonical monitor.MetricsRecorder port. The structural identity
// between the observability-side adapter and the application-side
// port is pinned HERE (and only here) because lifecycle.go imports
// both monitor + observability without creating an import cycle —
// the production-time pinning location. Drift between adapter
// methods and port methods is a build-time failure at this line.
//
// (Alternative pin locations and why they're wrong:
//   - metrics_adapter.go: production import of monitor from
//     observability would create an infra→app circular import.
//   - metrics_adapter_test.go: an earlier draft of the test file
//     imported monitor for the assertion + tests, but pulled in
//     the monitor → channels → assets → outbox → observability
//     chain, creating a Go package cycle in TEST scope. The test
//     file was simplified to drop the monitor import; the
//     assertion lives here at the composition root instead.)
var _ monitor.MetricsRecorder = (*observability.ObservabilityMetricsRecorder)(nil)
