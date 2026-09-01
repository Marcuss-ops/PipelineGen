// Package app — Outbox bundle construction (FASE 2.B PR2, June 2026).
//
// Originally two siblings + this file co-owned the per-concept bundle
// constructors (BuildDriveBundle + startDriveBackgroundFolders were
// also inline here originally). PR1 + PR2 successively extracted the
// per-concept bundles to dedicated files, leaving this file with ONLY
// the canonical ingestion-path outbox concern.
//
// PR1 (June 2026) extracted the Drive bundle construction
// (BuildDriveBundle + startDriveBackgroundFolders) to
//   - internal/app/build_bundles_drive.go   (BuildDriveBundle — Drive
//     client + folder resolver init, MediaStore derivation, StyleRegistry load)
//   - internal/app/build_drive_startup.go  (startDriveBackgroundFolders —
//     Drive folder bootstrap, AC validation, retry warmup)
//
// PR2 (June 2026) extracted BuildProcessBundle + the Qdrant
// compile-time port assertions to
//   - internal/app/build_process_qdrant.go (BuildProcessBundle +
//     clipindexer/jobsoutbox port-conformance assertions — the
//     Qdrant-derivable media-processing bundle + the typed-port
//     conformance gates).
//
// July 2026 sub-section extraction: the inline blocks of
// BuildOutboxBundle were extracted to sibling files of this package
// per the documented layout in build_process_qdrant.go:
//   - internal/app/build_outbox_handlers.go (buildOutboxDeps +
//     registerOutboxCoreHandlers + registerOutboxWorkers +
//     noopIndexClipper — the outbox deps + handler-registration
//     sub-blocks)
//   - internal/app/build_media_processor.go (wireMediaProcessor +
//     newVLMClient — the media-processor + VLM-client construction)
//
// This file now owns ONLY:
//   - BuildOutboxBundle (canonical ingestion-path outbox.Dispatcher +
//     outbox_events.Pool, registration of core + optional handlers)
//   - startOutboxEventsPool (SafeGo launchers: pool Start + drain on
//     ctx.Done())
//
// Each bundle constructor corresponds to ONE bundle concept per
// AGENTS.md Pattern 5 (no half-bundles, no `Build*And*` composites).
// PR2 is MOVE-only: zero logic changes in this file, zero call-site
// changes anywhere in the codebase.
package wiring

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// FASE 2.B PR1 (June 2026): BuildDriveBundle + startDriveBackgroundFolders
// moved to build_bundles_drive.go + build_drive_startup.go respectively.
// FASE 2.B PR2 (June 2026): BuildProcessBundle + the Qdrant compile-time
// port assertions moved to build_process_qdrant.go. Both are referenced
// by composition.go::NewComposition via `package app`-level visibility
// (cross-file within the same package). This file no longer needs the
// `assetsearch` / `clipindexer` / `driver` / `qdrant` / `sqassets` /
// `vlm` imports — every bundle left here is outbox-path-only.

// BuildOutboxBundle constructs the canonical ingestion outbox + outbox_events.Pool.
//
// PR9-B (June 2026): BuildOutboxBundle returns an IOpaqueStartFunc closure
// that defers the outbox events pool goroutines (Start + shutdown) to the
//
//	The bundle itself is fully populated on return.
//
// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed): the previous
// `process *ProcessBundle` arg was replaced with `qd *QdrantDeps`, the
// tiny pre-phase bundle that composition.go::buildQdrantDeps populates
// BEFORE BuildOutboxBundle runs. The ring between ProcessBundle and
// OutboxBundle is broken: BuildOutboxBundle now reads ONLY
// `qd.QdrantDeleter` + `qd.ClipIndexerService` (NOT the full ProcessBundle),
// so BuildOutboxBundle can run BEFORE BuildProcessBundle. Composition graph:
//
//	qdrantDeps(no deps) -> outbox(reads qd) -> process(reads outbox+qd)
//
// Fail-closed: a nil qd fails composition immediately (composition forgot
// to call buildQdrantDeps first?).
//
// P0.7 Wave 21 Step 10/12 (June 2026) — voiceover cleanup handler
// wiring: the voiceoverCleanup arg passes drive.Admin directly (it
// satisfies jobsoutbox.VoiceoverCleanupDriver structurally via its
// DeleteFile method) into the outbox Deps so
// VoiceoverCleanupHandler.register runs with a non-nil Drive delete
// surface. nil voiceoverCleanup is tolerated — the handler still
// handles local file removal (stdlib os.Remove, no port ceremony)
// and logs+skips the Drive delete branch with an operator-visible
// warning. Production wiring always supplies a non-nil adapter.
//
// Blocco 3.1 commit 2/3 (June 2026) — driveDeleter wiring: the
// driveDeleter arg passes drive.FileLifecycle (from
// DriveBundle.Lifecycle) into the outbox Deps so
// DriveDeleteHandler (asset.drive.delete_requested.v1) can trash /
// permanently-delete the Drive file and atomically advance the
// deletion state machine. nil driveDeleter is tolerated —
// RegisterOptionalHandlers skips the handler at Info — but in
// production it is a dead-letter regression, so the wiring logs a
// loud Warn when it is missing. Production wiring always supplies
// the canonical drive.FileLifecycle adapter.
func BuildOutboxBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, repos *RepoBundle, qd *QdrantDeps, jobs *JobsBundle, voiceoverDriver jobsoutbox.VoiceoverCleanupDriver, stagingSvc staging.Store, repo detail.ArtifactStageRepository, drivePublisher delivery.Publisher, driveDeleter jobsoutbox.DriveDeleter) (*OutboxBundle, IOpaqueStartFunc, error) {
	if qd == nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: qdrantDeps is nil (QDRANT-002 PR8 fail-closed; composition forgot to call buildQdrantDeps first?)")
	}
	if stagingSvc == nil {
		// FASE 3 Push 3.1c: the Publisher worker is the canonical
		// outbox→staging adapter. It MUST be wired here (otherwise
		// publish_requested events dead-letter on the first
		// emission). Composition must call BuildStagingBundle
		// BEFORE BuildOutboxBundle so the typed port is available.
		return nil, nil, fmt.Errorf("BuildOutboxBundle: stagingSvc is required (FASE 3 Push 3.1c; composition must call BuildStagingBundle before BuildOutboxBundle)")
	}
	if repo == nil {
		// FASE 3 Push 3.1e: the DriveUploader worker consumes
		// artifact.Repository for the MarkPublished fenced-CAS. A
		// nil repo is fail-closed — composition must inject the
		// same artifact.Repository port that StagingBundle uses
		// (single-writer SSOT; no second DB wrapper).
		return nil, nil, fmt.Errorf("BuildOutboxBundle: repo is required (FASE 3 Push 3.1e; composition must inject StagingBundle.Repository)")
	}
	if drivePublisher == nil {
		// FASE 3 Push 3.1e: the DriveUploader worker is the
		// canonical outbox→Drive adapter. It MUST be wired here
		// (otherwise artifact.staged.v1 events dead-letter the
		// moment the saga's first Publish step). A nil
		// drivePublisher is fail-closed — composition must
		// inject the canonical delivery.Publisher from
		// DriveBundle (built in BuildDriveBundle).
		return nil, nil, fmt.Errorf("BuildOutboxBundle: drivePublisher is required (FASE 3 Push 3.1e; composition must inject DriveBundle.Publisher)")
	}

	// PR-QDRANT-CONFIG-MISMATCH-GATE (July 2026): defense-in-depth
	// gate at all 4 Qdrant composition sites. THIRD wire site (the
	// outbox is the PRIMARY consumer of the Qdrant stack — it registers
	// IndexingHandler + IndexDeleteHandler when cfg.Qdrant.Enabled=true).
	// godlike/07 no-fake-availability: any operator misconfiguration
	// is caught BEFORE the outbox handler registry wires up malformed
	// mandatory deps. Cross-ref:
	// internal/app/build_bundles_qdrant_gates.go::validateQdrantIndexerCompatibility.
	if err := validateQdrantIndexerCompatibility(cfg); err != nil {
		return nil, nil, err
	}
	outboxEventsRepo := outboxevents.NewRepository(dbs.DualPool.Writer)

	// PR 3 fix/qdrant-outbox-fail-closed BL-1 fix: dispatcher
	// construction happens AFTER the fail-closed CORE handler
	// registration (registerOutboxCoreHandlers above), so when
	// repos.ClipsRepo was nil the call returns the typed error the
	// fail-closed contract requires instead of an internal panic
	// (NewDispatcher / NewMultiClipsUpserter panic on nil inputs).
	// The dispatcher is additionally built BEFORE
	// RegisterOptionalHandlers so the DriveDeleteDeps StateAdvancer
	// port can be populated (Blocco 3.1 commit 2/3 wiring, fixed in
	// production July 2026 — previously the DriveDeleteDeps were
	// never populated and every asset.drive.delete_requested.v1
	// event dead-lettered).

	eventsRegistry := outboxevents.NewHandlerRegistry()

	// Deps + handler registration sub-blocks (extracted July 2026 to
	// build_outbox_handlers.go). Same order as the pre-split flat
	// body: deps construction → core handlers → optional/worker
	// handlers.
	outboxDeps, metadataExportHandler := buildOutboxDeps(dbs, cfg, repos, jobs, qd, voiceoverDriver, log)
	if err := registerOutboxCoreHandlers(eventsRegistry, cfg, repos, qd, outboxDeps, log); err != nil {
		return nil, nil, err
	}

	// ── Dispatcher construction (post core fail-closed gate). ─────
	// Moved BEFORE RegisterOptionalHandlers (previously after) so the
	// DriveDeleteDeps.StateAdvancer port below can be populated with
	// the concrete *outbox.Dispatcher. The PR 3 fail-closed contract
	// is preserved: the core handlers registered above abort boot on a
	// nil mandatory dep before this block ever runs.
	outboxTxMgr := outbox.NewManager(dbs.DualPool.Writer, log)
	canonicalCommitter := newCanonicalAssetCommitter(dbs.DualPool.Writer, outboxEventsRepo, log)
	// The production dispatcher receives the single canonical writer directly.
	// Legacy ClipsUpserter/ClipsStateWriter arguments remain accepted by
	// NewDispatcher only for compatibility with older tests and adapters.
	if repos.ClipsRepo != nil {
		repos.ClipsRepo.SetCanonicalWriter(canonicalCommitter)
	}
	dispatcher := outbox.NewDispatcher(nil, nil, outboxEventsRepo, outboxTxMgr, log,
		canonicalCommitter)
	log.Info("outbox dispatcher instantiated: canonical upsert+outbox_events enqueue path AND canonical delete+outbox_events enqueue path (QDRANT-002 PR7)")

	// Blocco 3.1 commit 2/3 (June 2026): DriveDeleteHandler deps
	// (asset.drive.delete_requested.v1 → Drive Trash/Delete → atomic
	// AdvanceAndEmit to DRIVE_DELETED + emit index.delete_requested).
	// All 4 narrow ports are populated from production wiring;
	// RegisterOptionalHandlers registers the handler only when ALL
	// four are non-nil (partial dev wiring skips at Info, never aborts
	// boot). A nil driveDeleter in production is a silent
	// dead-letter regression, so it is surfaced as a loud Warn here.
	if driveDeleter != nil && repos.ClipsRepo != nil {
		outboxDeps.DriveDelete = jobsoutbox.DriveDeleteDeps{
			DrivePatchLifecycle:  repos.ClipsRepo,
			DrivePatchLifecycleW: repos.ClipsRepo,
			DrivePatchStateAdv:   dispatcher,
			DriveDeleteHandler:   driveDeleter,
		}
		log.Info("outbox DriveDeleteHandler deps wired: asset.drive.delete_requested.v1 → Drive Trash/Delete → AdvanceAndEmit (Blocco 3.1 commit 2/3)")
	} else {
		log.Warn("outbox DriveDeleteHandler deps NOT wired (driveDeleter or ClipsRepo nil) — asset.drive.delete_requested.v1 events will dead-letter with 'no handler registered'")
	}

	publisherHandler, driveUploadHandler, err := registerOutboxWorkers(eventsRegistry, log, outboxDeps, metadataExportHandler, jobs, stagingSvc, repo, repos.ImageRepo, drivePublisher)
	if err != nil {
		return nil, nil, err
	}

	// Derived performance projection: job.completed events → performance_runs/
	// performance_steps. Best-effort (see registerPerformanceProjectionHandler).
	registerPerformanceProjectionHandler(eventsRegistry, dbs, dbs.Main, log)

	// ── Pool construction (post fail-closed). ─────────────────────
	cfgPoll := 500 * time.Millisecond
	if cfg.Outbox.PollIntervalMs > 0 {
		cfgPoll = time.Duration(cfg.Outbox.PollIntervalMs) * time.Millisecond
	}
	cfgReclaim := 60 * time.Second
	if cfg.Outbox.ReclaimIntervalSeconds > 0 {
		cfgReclaim = time.Duration(cfg.Outbox.ReclaimIntervalSeconds) * time.Second
	}
	cfgProcess := 30 * time.Second
	if cfg.Outbox.ProcessTimeoutSeconds > 0 {
		cfgProcess = time.Duration(cfg.Outbox.ProcessTimeoutSeconds) * time.Second
	}
	outboxEventsCfg := outboxevents.WorkerPollConfig{
		Workers:         cfg.Outbox.Workers,
		PollInterval:    cfgPoll,
		ProcessTimeout:  cfgProcess,
		ReclaimInterval: cfgReclaim,
	}
	eventsPool := outboxevents.NewPool("outbox-events", outboxEventsRepo, eventsRegistry, log, outboxEventsCfg)

	// In split mode job.completed is committed to the execution DB's local
	// outbox. It gets its own registry/pool so media outbox events remain
	// transactionally owned and consumed by media.db.sqlite.
	var jobsEventsPool *outboxevents.Pool
	if dbs.Jobs != nil {
		jobsRegistry := outboxevents.NewHandlerRegistry()
		registerPerformanceProjectionHandler(jobsRegistry, dbs, dbs.Jobs, log)
		jobsEventsPool = outboxevents.NewPool("jobs-outbox-events", outboxevents.NewRepository(dbs.Jobs.DB), jobsRegistry, log, outboxEventsCfg)
	}

	startClosure := func() error {
		if err := startOutboxEventsPool(ctx, eventsPool, outboxEventsCfg, log); err != nil {
			return err
		}
		return startOutboxEventsPool(ctx, jobsEventsPool, outboxEventsCfg, log)
	}

	return &OutboxBundle{
		CanonicalWriter: canonicalCommitter,
		Dispatcher:      dispatcher,
		EventsRepo:      outboxEventsRepo,
		EventsRegistry:  eventsRegistry,
		EventsPool:      eventsPool,
		JobsEventsPool:  jobsEventsPool,
		Publisher:       publisherHandler,
		DriveUploader:   driveUploadHandler,
	}, startClosure, nil
}

// startOutboxEventsPool performs the side-effecting outbox events pool
// initialisation.
//
// Lifecycle-runtime-ownership (June 2026): Pool.Start is void-returning
// so the goroutine is launched via SafeGo (panic-recovery). The shutdown
// goroutine drains the pool on ctx.Done(). The caller treats this as a
// required step — if the goroutine panics, SafeGo recovers and logs the
// panic without crashing the server.
func startOutboxEventsPool(
	ctx context.Context,
	eventsPool *outboxevents.Pool,
	cfg outboxevents.WorkerPollConfig,
	log *zap.Logger,
) error {
	if eventsPool == nil {
		return nil
	}
	concurrent.SafeGo("outbox-events-pool", func() {
		workers := cfg.Workers
		if workers <= 0 {
			workers = 1
		}
		eventsPool.Start(ctx, workers)
	})
	concurrent.SafeGo("outbox-events-shutdown", func() {
		<-ctx.Done()
		if err := eventsPool.Stop(15 * time.Second); err != nil {
			log.Warn("outbox events pool stop returned error", zap.Error(err))
		}
	})
	log.Info("outbox events pool started", zap.Duration("poll_interval", cfg.PollInterval))
	return nil
}
