// Package app — lifecycle maintenance capability (PR-LIFECYCLE-SPLIT-BY-CAPABILITY, July 2026).
//
// Extracted from internal/app/lifecycle.go per AGENTS.md Pattern 5.
// Owns the maintenance-mode startup steps:
//
//   - maintenance-scheduler-maintenance  (24h interval, enqueues system.cleanup)
//   - maintenance-scheduler-backup       (6h interval, enqueues system.cleanup)
//   - research-cache-sweeper             (calls into lifecycle_sweepers.go)
//   - gemma-memory-sweeper               (calls into lifecycle_sweepers.go)
//   - clip-dedup-sweeper                 (calls into lifecycle_sweepers.go)
//   - vlm-autotag-sweeper                (calls into lifecycle_sweepers.go)
//   - deletion-reconciler                (deletionreconciler.NewServiceFromDeps)
//   - orphan-sweeper                     (voiceover.NewOrphanSweeper)
//
// Sister file to lifecycle_worker.go + lifecycle_scheduler.go (the 3
// capability files) + lifecycle_adapters.go (composition-root adapters).
// The 4 sweeper helpers (startResearchCacheSweeper etc.) continue to
// live in lifecycle_sweepers.go per AGENTS.md "Code Reuse: Always
// reuse helper functions" — this file just wraps each with a
// StartupStep + concurrent.SafeGo call.
//
// Qdrant audit-pins (the 2 "step removed" comments that mark the
// historical qdrant-cleaner + qdrant-ghost-sweeper removals) are
// preserved here per godlike/06 SSOT + godlike/07 no-fake-availability.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"context"
	"time"

	deletionreconciler "github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion/reconciler"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// maintenanceDeps holds the composition-root dependencies required to
// build the maintenance-mode startup steps. Typed, not any:
// mirrors the jobRunnerDeps + workerDeps + schedulerDeps pattern.
type maintenanceDeps struct {
	cfg  *config.Config
	root *wiring.ComposeRoot
	log  *zap.Logger
}

// buildMaintenanceSteps returns the maintenance-mode StartupStep list
// (schedulers + sweepers + deletion-reconciler + orphan-sweeper).
//
// godlike/07 minimum-blast-radius: each step has its own wiring gate
// (jobs.Service != nil, root.DB.DB != nil, root.Drive.Lifecycle != nil,
// etc.) — partial deploys log a Warn + skip the step instead of
// aborting startup. Mirrors the pre-PR-LIFECYCLE-SPLIT-BY-CAPABILITY
// inline behaviour.
func buildMaintenanceSteps(deps maintenanceDeps) []StartupStep {
	var steps []StartupStep

	// Parse maintenance + backup intervals with cfg-driven defaults.
	maintenanceInterval := 24 * time.Hour
	if deps.cfg.Jobs.MaintenanceInterval != "" {
		if parsed, err := time.ParseDuration(deps.cfg.Jobs.MaintenanceInterval); err == nil {
			maintenanceInterval = parsed
		}
	}
	backupInterval := 6 * time.Hour
	if deps.cfg.Jobs.BackupInterval != "" {
		if parsed, err := time.ParseDuration(deps.cfg.Jobs.BackupInterval); err == nil {
			backupInterval = parsed
		}
	}

	// 2 maintenance-scheduler steps: "maintenance" + "backup".
	if deps.root.Jobs.Service != nil {
		jsvc := deps.root.Jobs.Service
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
								deps.log.Warn("failed to enqueue maintenance job", zap.String("label", label), zap.Error(err))
							} else {
								deps.log.Info("scheduled maintenance job enqueued", zap.String("label", label))
							}
							select {
							case <-startCtx.Done():
								return
							case <-time.After(interval):
							}
						}
					})
					deps.log.Info("scheduled maintenance job", zap.String("label", label), zap.Duration("interval", interval))
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			}
		}
		steps = append(steps, makeSchedulerStep(maintenanceInterval, "maintenance"))
		steps = append(steps, makeSchedulerStep(backupInterval, "backup"))
		deps.log.Info("scheduled maintenance and backup jobs via jobs system",
			zap.Duration("maintenance_interval", maintenanceInterval),
			zap.Duration("backup_interval", backupInterval))
	} else {
		deps.log.Warn("jobs service not available, skipping scheduled maintenance/backup")
	}

	// 4 sweepers — call into lifecycle_sweepers.go helpers per
	// AGENTS.md "Code Reuse: Always reuse helper functions" rule.
	if deps.root.Repos.ScriptsRepo != nil {
		repo := deps.root.Repos.ScriptsRepo
		steps = append(steps, StartupStep{
			Name: "research-cache-sweeper", Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("research-cache-sweeper", func() {
					startResearchCacheSweeper(startCtx, repo, deps.log)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	if deps.root.AI != nil && deps.root.AI.MemoryRepo != nil {
		mrepo := deps.root.AI.MemoryRepo
		steps = append(steps, StartupStep{
			Name: "gemma-memory-sweeper", Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("gemma-memory-sweeper", func() {
					startGemmaMemorySweeper(startCtx, mrepo, deps.log)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Qdrant-cleaner step removed (godlike/07 no-fake-availability
	// + PR-QDRANT-FINAL-DECISION 2026-07-04: Qdrant is the canonical
	// data-path vector store; the 3 background-cleanup steps retired
	// earlier will be re-introduced by Wave 30 BACKFILL with the
	// canonical scope pinned to composition.go::wiring.ProcessBundle).

	if deps.root.Repos.ClipsRepo != nil {
		cr := deps.root.Repos.ClipsRepo
		steps = append(steps, StartupStep{
			Name: "clip-dedup-sweeper", Required: false,
			Start: func(startCtx context.Context) error {
				deps.log.Info("clip dedup sweeper starting (interval=30m)")
				concurrent.SafeGo("clip-dedup-sweeper", func() {
					startClipDedupSweeper(startCtx, cr, deps.log)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	if deps.root.Domains.AutotagService != nil {
		at := deps.root.Domains.AutotagService
		steps = append(steps, StartupStep{
			Name: "vlm-autotag-sweeper", Required: false,
			Start: func(startCtx context.Context) error {
				deps.log.Info("VLM auto-tag sweeper starting (interval=15m)")
				concurrent.SafeGo("vlm-autotag-sweeper", func() {
					startVLMAutoTagSweeper(startCtx, at, deps.log)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// ── Deletion Reconciler (Blocco 3.2 commit 2/2, June 2026) ──
	// Periodic ticker that re-emits the canonical outbox event for any
	// media_assets row stuck in {DELETE_REQUESTED, DRIVE_DELETE_PENDING,
	// INDEX_DELETE_PENDING} past the configurable stuck threshold.
	if deps.root.DB != nil && deps.root.DB.DB != nil {
		reconcilerInterval := 15 * time.Minute
		if deps.cfg.Jobs.DeletionReconcilerInterval != "" {
			if parsed, err := time.ParseDuration(deps.cfg.Jobs.DeletionReconcilerInterval); err == nil && parsed > 0 {
				reconcilerInterval = parsed
			}
		}
		reconcilerThreshold := 30 * time.Minute
		if deps.cfg.Jobs.DeletionReconcilerStuckThreshold != "" {
			if parsed, err := time.ParseDuration(deps.cfg.Jobs.DeletionReconcilerStuckThreshold); err == nil && parsed > 0 {
				reconcilerThreshold = parsed
			}
		}

		// Look up the outbox.Dispatcher via root.Outbox; if absent
		// (e.g. partial deploy), log WARN + skip.
		if deps.root.Outbox != nil && deps.root.Outbox.Dispatcher != nil {
			disp := deps.root.Outbox.Dispatcher
			stuckScanner := deletion.NewScanner(deps.root.DB.DB, 100)
			recSvc := deletionreconciler.NewServiceFromDeps(deletionreconciler.ServiceDeps{
				Scanner:          stuckScanner,
				OutboxEnqueuer:   disp,
				Metrics:          deletion.ReconcilerMetricsAdapter{},
				DefaultInterval:  reconcilerInterval,
				DefaultThreshold: reconcilerThreshold,
				Log:              deps.log,
			})

			steps = append(steps, StartupStep{
				Name: "deletion-reconciler", Required: false,
				Start: func(startCtx context.Context) error {
					deps.log.Info("deletion-reconciler starting (interval=15m)",
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
			deps.log.Warn("deletion-reconciler skipped: outbox.Dispatcher not wired (composition-root partial deploy)")
		}
	} else {
		deps.log.Warn("deletion-reconciler skipped: root.DB.DB not wired")
	}

	// ── Orphan Sweeper (P0 #4 commit B/2, July 2026) ──
	// Periodic ticker that compensates for partial-failures in the
	// upload_intents table. Compensates stale 'uploaded' rows via
	// Drive.Trash + MarkFailed; stale 'pending' rows via MarkFailed
	// only. Wiring gates: root.DB.DB != nil + root.Drive.Lifecycle != nil.
	if deps.root.DB != nil && deps.root.DB.DB != nil && deps.root.Drive != nil && deps.root.Drive.Lifecycle != nil {
		uploadRepo := scripts.NewUploadIntentsRepository(deps.root.DB.DB)
		driveLifecycle := deps.root.Drive.Lifecycle
		orphanSweeper := voiceover.NewOrphanSweeper(voiceover.OrphanSweeperDeps{
			Repo:         newUploadIntentsAdapter(uploadRepo),
			DriveDeleter: driveLifecycle,
			Logger:       deps.log,
			Metrics: &voiceover.Metrics{
				Runs:       observability.OrphanSweeperRunsTotal,
				Reconciled: observability.OrphanSweeperReconciledTotal,
			},
			Tick:        10 * time.Minute,
			PendingTTL:  30 * time.Minute,
			UploadedTTL: 60 * time.Minute,
		})
		sw := orphanSweeper
		steps = append(steps, StartupStep{
			Name:     "orphan-sweeper",
			Required: false,
			Start: func(startCtx context.Context) error {
				deps.log.Info("orphan-sweeper starting (interval=10m, pendingTTL=30m, uploadedTTL=60m)")
				concurrent.SafeGo("orphan-sweeper", func() {
					sw.Run(startCtx)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	} else {
		deps.log.Warn("orphan-sweeper skipped: root.DB.DB or root.Drive.Lifecycle not wired (composition-root partial deploy)")
	}

	// Qdrant-ghost-sweeper step removed (see note above re:
	// qdrant-cleaner + PR-QDRANT-FINAL-DECISION).

	return steps
}
