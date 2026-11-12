// Package lifecycle builds maintenance schedulers, sweepers, and recovery
// steps from explicit service and storage dependencies.
package lifecycle

import (
	"context"
	"database/sql"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/autotag"
	deletionreconciler "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/deletion/reconciler"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	driveapi "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// MaintenanceDeps is the narrow set of already-composed services used to
// build the maintenance-mode startup steps. It keeps ComposeRoot out of this
// subsystem and makes storage ownership explicit at the composition boundary.
type MaintenanceDeps struct {
	Config    *config.Config
	Services  MaintenanceServices
	Databases MaintenanceDatabases
	Media     MaintenanceMedia
	Log       *zap.Logger
}

// MaintenanceServices groups capability-owned services used by maintenance.
type MaintenanceServices struct {
	Jobs                       MaintenanceJobService
	StaleJobs                  StaleJobSweeperStore
	EntityImageRecertification *entitycatalog.RecertificationService
	MemoryRepo                 scriptports.MemoryGate
	OutboxDispatcher           *outbox.Dispatcher
	DriveLifecycle             driveapi.FileLifecycle
	Autotag                    *autotag.Service
}

// MaintenanceDatabases keeps the distinct control, cache, and observability
// SQLite handles explicit at the lifecycle boundary.
type MaintenanceDatabases struct {
	ResearchCache *sql.DB
	Observability *sql.DB
	Operational   *sql.DB
}

// MaintenanceMedia carries both halves of the duplicate sweep from the same
// canonical committer, plus any failure resolving that SSOT.
type MaintenanceMedia struct {
	DuplicateReader MediaYouTubeIDDuplicateReader
	Retirer         persistence.AssetSoftDeleter
	ResolutionError error
}

// MaintenanceJobService is the small jobs surface used by scheduled
// maintenance and script-run reconciliation.
type MaintenanceJobService interface {
	Enqueue(context.Context, *job.EnqueueRequest) (*job.Job, error)
	Get(context.Context, string) (*job.Job, error)
	List(context.Context, job.Filter) ([]job.Job, error)
}

// BuildMaintenanceSteps returns the maintenance-mode StartupStep list
// (schedulers + sweepers + deletion-reconciler + orphan-sweeper).
//
// godlike/07 minimum-blast-radius: each step has its own wiring gate
// (JobsService != nil, OperationalDB != nil, DriveLifecycle != nil,
// etc.) — partial deploys log a Warn + skip the step instead of
// aborting startup. Mirrors the pre-PR-LIFECYCLE-SPLIT-BY-CAPABILITY
// inline behaviour.
func BuildMaintenanceSteps(deps MaintenanceDeps) []StartupStep {
	var steps []StartupStep

	// Parse maintenance + backup intervals with cfg-driven defaults.
	maintenanceInterval := 24 * time.Hour
	if deps.Config.Jobs.MaintenanceInterval != "" {
		if parsed, err := time.ParseDuration(deps.Config.Jobs.MaintenanceInterval); err == nil {
			maintenanceInterval = parsed
		}
	}
	backupInterval := 6 * time.Hour
	if deps.Config.Jobs.BackupInterval != "" {
		if parsed, err := time.ParseDuration(deps.Config.Jobs.BackupInterval); err == nil {
			backupInterval = parsed
		}
	}

	// 2 maintenance-scheduler steps: "maintenance" + "backup".
	if deps.Services.Jobs != nil {
		jsvc := deps.Services.Jobs
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
								deps.Log.Warn("failed to enqueue maintenance job", zap.String("label", label), zap.Error(err))
							} else {
								deps.Log.Info("scheduled maintenance job enqueued", zap.String("label", label))
							}
							select {
							case <-startCtx.Done():
								return
							case <-time.After(interval):
							}
						}
					})
					deps.Log.Info("scheduled maintenance job", zap.String("label", label), zap.Duration("interval", interval))
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			}
		}
		steps = append(steps, makeSchedulerStep(maintenanceInterval, "maintenance"))
		steps = append(steps, makeSchedulerStep(backupInterval, "backup"))
		deps.Log.Info("scheduled maintenance and backup jobs via jobs system",
			zap.Duration("maintenance_interval", maintenanceInterval),
			zap.Duration("backup_interval", backupInterval))
	} else {
		deps.Log.Warn("jobs service not available, skipping scheduled maintenance/backup")
	}

	// PERSON image catalog recertification. The service validates only remote
	// candidate URLs; it never calls the materializer and therefore cannot
	// invalidate an already verified Drive asset.
	if deps.Services.EntityImageRecertification != nil {
		recertifier := deps.Services.EntityImageRecertification
		steps = append(steps, StartupStep{
			Name: "entity-image-catalog-recertification", Required: false,
			Start: func(startCtx context.Context) error {
				deps.Log.Info("entity image catalog recertification starting")
				concurrent.SafeGo("entity-image-catalog-recertification", func() {
					recertifier.Run(startCtx, func(report entitycatalog.RecertificationReport, err error) {
						if err != nil {
							deps.Log.Warn("entity image catalog recertification failed", zap.Error(err))
							return
						}
						deps.Log.Info("entity image catalog recertification completed",
							zap.Int("selected", report.Selected), zap.Int("succeeded", report.Succeeded),
							zap.Int("failed", report.Failed))
					})
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// 4 sweepers — call into lifecycle_sweepers.go helpers per
	// AGENTS.md "Code Reuse: Always reuse helper functions" rule.
	if deps.Databases.ResearchCache != nil {
		repo := sqlitescripts.NewScriptRepository(deps.Databases.ResearchCache)
		steps = append(steps, StartupStep{
			Name: "research-cache-sweeper", Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("research-cache-sweeper", func() {
					StartResearchCacheSweeper(startCtx, repo, deps.Log)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	if deps.Services.MemoryRepo != nil {
		mrepo := deps.Services.MemoryRepo
		steps = append(steps, StartupStep{
			Name: "gemma-memory-sweeper", Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("gemma-memory-sweeper", func() {
					StartGemmaMemorySweeper(startCtx, mrepo, deps.Log)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	}

	// Reconcile durable generation submissions against the authoritative jobs
	// plane. This closes the crash window after a job enqueue committed but
	// before the producer persisted its returned job ID, and fails stale run
	// intents only after their idempotency key is confirmed absent from the
	// broker.
	if deps.Databases.Observability != nil && deps.Services.Jobs != nil {
		runRepo, err := sqlitescripts.NewSQLiteRunRepository(deps.Databases.Observability, deps.Log)
		if err != nil {
			deps.Log.Warn("script run reconciliation not registered: run repository unavailable", zap.Error(err))
		} else {
			steps = append(steps, StartupStep{
				Name: "script-run-reconciliation", Required: false,
				Start: func(startCtx context.Context) error {
					concurrent.SafeGo("script-run-reconciliation", func() {
						StartScriptRunReconciliationSweeper(startCtx, runRepo, deps.Services.Jobs, deps.Log)
					})
					return nil
				},
				Stop: func(_ context.Context) error { return nil },
			})
		}
	}

	// Fail jobs whose worker lease expired well past the grace window. This
	// is the automatic counterpart to the operator-only zombie-sweep CLI.
	if deps.Services.StaleJobs != nil {
		steps = append(steps, StartupStep{
			Name: "stale-job-sweeper", Required: false,
			Start: func(startCtx context.Context) error {
				concurrent.SafeGo("stale-job-sweeper", func() {
					StartStaleJobSweeper(startCtx, deps.Services.StaleJobs, deps.Log)
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
	// canonical scope pinned to composition.go::ProcessBundle).

	// clip-dedup-sweeper. MEDIA-SSOT (P2-9 read side, final entry): BOTH halves
	// of this sweep are media_assets operations — the duplicate scan reads it and
	// the retirement writes it — so the step is gated on the MEDIA SSOT, not on
	// Repos.ClipsRepo (the operational SQLite store). Gating on the SQLite
	// repository is what produced the original defect: the scan enumerated
	// duplicates on a mirror and the retirement landed there too, so a duplicate
	// was re-counted on every tick while staying live on the SSOT.
	//
	// reader and retirer are resolved from ONE committer on purpose. Deriving
	// them independently would allow a scan on the SSOT with a retirement on the
	// mirror, which is the same permanent-duplicate loop wearing a new shape.
	//
	// Degradation is honest: with the media plane closed the step is not
	// registered at all (rather than registered and failing every tick).
	if deps.Media.ResolutionError != nil {
		deps.Log.Warn("clip dedup sweeper not registered: canonical media committer unavailable", zap.Error(deps.Media.ResolutionError))
	} else if dedupReader, dedupRetirer := deps.Media.DuplicateReader, deps.Media.Retirer; dedupReader != nil && dedupRetirer != nil {
		steps = append(steps, StartupStep{
			Name: "clip-dedup-sweeper", Required: false,
			Start: func(startCtx context.Context) error {
				deps.Log.Info("clip dedup sweeper starting (interval=30m)")
				concurrent.SafeGo("clip-dedup-sweeper", func() {
					StartClipDedupSweeper(startCtx, dedupReader, dedupRetirer, deps.Log)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	} else {
		deps.Log.Warn("clip dedup sweeper not registered: media SSOT closed (dedup sweeps media_assets on PostgreSQL only)")
	}

	if deps.Services.Autotag != nil {
		at := deps.Services.Autotag
		steps = append(steps, StartupStep{
			Name: "vlm-autotag-sweeper", Required: false,
			Start: func(startCtx context.Context) error {
				deps.Log.Info("VLM auto-tag sweeper starting (interval=15m)")
				concurrent.SafeGo("vlm-autotag-sweeper", func() {
					StartVLMAutoTagSweeper(startCtx, at, deps.Log)
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
	if deps.Databases.Operational != nil {
		reconcilerInterval := 15 * time.Minute
		if deps.Config.Jobs.DeletionReconcilerInterval != "" {
			if parsed, err := time.ParseDuration(deps.Config.Jobs.DeletionReconcilerInterval); err == nil && parsed > 0 {
				reconcilerInterval = parsed
			}
		}
		reconcilerThreshold := 30 * time.Minute
		if deps.Config.Jobs.DeletionReconcilerStuckThreshold != "" {
			if parsed, err := time.ParseDuration(deps.Config.Jobs.DeletionReconcilerStuckThreshold); err == nil && parsed > 0 {
				reconcilerThreshold = parsed
			}
		}

		// Look up the outbox.Dispatcher from typed deps; if absent
		// (e.g. partial deploy), log WARN + skip.
		if deps.Services.OutboxDispatcher != nil {
			disp := deps.Services.OutboxDispatcher
			stuckScanner := deletion.NewScanner(deps.Databases.Operational, 100)
			recSvc := deletionreconciler.NewServiceFromDeps(deletionreconciler.ServiceDeps{
				Scanner:          stuckScanner,
				OutboxEnqueuer:   disp,
				Metrics:          deletion.ReconcilerMetricsAdapter{},
				DefaultInterval:  reconcilerInterval,
				DefaultThreshold: reconcilerThreshold,
				Log:              deps.Log,
			})

			steps = append(steps, StartupStep{
				Name: "deletion-reconciler", Required: false,
				Start: func(startCtx context.Context) error {
					deps.Log.Info("deletion-reconciler starting (interval=15m)",
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
			deps.Log.Warn("deletion-reconciler skipped: outbox.Dispatcher not wired (composition-root partial deploy)")
		}
	} else {
		deps.Log.Warn("deletion-reconciler skipped: operational database not wired")
	}

	// ── Orphan Sweeper (P0 #4 commit B/2, July 2026) ──
	// Periodic ticker that compensates for partial-failures in the
	// upload_intents table. Compensates stale 'uploaded' rows via
	// Drive.Trash + MarkFailed; stale 'pending' rows via MarkFailed
	// only. Wiring gates: OperationalDB != nil + DriveLifecycle != nil.
	if deps.Databases.Operational != nil && deps.Services.DriveLifecycle != nil {
		uploadRepo := sqlitescripts.NewUploadIntentsRepository(deps.Databases.Operational)
		driveLifecycle := deps.Services.DriveLifecycle
		orphanSweeper := voiceover.NewOrphanSweeper(voiceover.OrphanSweeperDeps{
			Repo:         NewUploadIntentsAdapter(uploadRepo),
			DriveDeleter: driveLifecycle,
			Logger:       deps.Log,
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
				deps.Log.Info("orphan-sweeper starting (interval=10m, pendingTTL=30m, uploadedTTL=60m)")
				concurrent.SafeGo("orphan-sweeper", func() {
					sw.Run(startCtx)
				})
				return nil
			},
			Stop: func(_ context.Context) error { return nil },
		})
	} else {
		deps.Log.Warn("orphan-sweeper skipped: operational database or Drive lifecycle not wired")
	}

	// ── projection-reconciler step REMOVED (POSTGRES-MEDIA-CUTOVER) ──
	//
	// The periodic parity ticker compared the canonical eligible SQLite
	// asset set (SearchIndexEligibilitySQL) against the ACTIVE Qdrant media
	// projection and emitted projection_coverage_ratio /
	// projection_orphan_count. With the PostgreSQL + pgvector media SSOT the
	// Qdrant media collection is no longer a projection of SQLite, so the
	// ticker could only report drift against a retired plane (false alarms)
	// while reading quarantined SQLite media state. Media parity is now
	// enforced transactionally inside PostgreSQL. The non-media Qdrant
	// projections (mediamemory frame concepts) own their own pipelines.

	// Qdrant-ghost-sweeper step removed (see note above re:
	// qdrant-cleaner + PR-QDRANT-FINAL-DECISION).

	return steps
}
