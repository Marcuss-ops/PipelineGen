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
// PR2 (June 2026) extracted BuildProcessBundle + the pgvector
// compile-time port assertions to
//   - internal/app/build_process_bundle.go (BuildProcessBundle +
//     clipindexer/jobsoutbox port-conformance assertions — the
//     vector-derivable media-processing bundle + the typed-port
//     conformance gates).
//
// July 2026 sub-section extraction: the inline blocks of
// BuildOutboxBundle were extracted to sibling files of this package
// per the documented layout in build_process_bundle.go:
//   - internal/app/build_outbox_handlers.go (buildOutboxDeps +
//     assertSingleMediaIndexOwner + registerOutboxWorkers +
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
package buildbundle

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	capindexing "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// FASE 2.B PR1 (June 2026): BuildDriveBundle + startDriveBackgroundFolders
// moved to build_bundles_drive.go + build_drive_startup.go respectively.
// FASE 2.B PR2 (June 2026): BuildProcessBundle + the pgvector compile-time
// port assertions moved to build_process_bundle.go. Both are referenced
// by composition.go::NewComposition via `package app`-level visibility
// (cross-file within the same package). This file no longer needs the
// `assetsearch` / `clipindexer` / `driver` / `pgvector` / `sqassets` /
// `vlm` imports — every bundle left here is outbox-path-only.

// BuildOutboxBundle constructs the canonical ingestion outbox + outbox_events.Pool.
//
// PR9-B (June 2026): BuildOutboxBundle returns an IOpaqueStartFunc closure
// that defers the outbox events pool goroutines (Start + shutdown) to the
//
//	The bundle itself is fully populated on return.
//
// PR 8 (June 2026, codex/pgvector-app-writers-fail-closed): the previous
// `process *ProcessBundle` arg was replaced with `qd *IndexDeps`, the
// tiny pre-phase bundle that composition.go::buildIndexDeps populates
// BEFORE BuildOutboxBundle runs. The ring between ProcessBundle and
// OutboxBundle is broken: BuildOutboxBundle now reads ONLY
// `qd.VectorPointDeleter` + `qd.ClipIndexerService` (NOT the full ProcessBundle),
// so BuildOutboxBundle can run BEFORE BuildProcessBundle. Composition graph:
//
//	indexDeps(no deps) -> outbox(reads qd) -> process(reads outbox+qd)
//
// Fail-closed: a nil qd fails composition immediately (composition forgot
// to call buildIndexDeps first?).
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
func BuildOutboxBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, repos *RepoBundle, qd *IndexDeps, jobs *JobsBundle, voiceoverDriver jobsoutbox.VoiceoverCleanupDriver, stagingSvc staging.Store, repo detail.ArtifactStageRepository, drivePublisher delivery.Publisher, driveDeleter jobsoutbox.DriveDeleter, mediaPostgres *sql.DB) (*OutboxBundle, IOpaqueStartFunc, error) {
	if err := validateOutboxBundleInputs(qd, stagingSvc, repo, drivePublisher); err != nil {
		return nil, nil, err
	}

	eventsRegistry, jobsRegistry := newOutboxRegistries(dbs)
	outboxEventsRepo := outboxevents.NewRepository(dbs.DualPool.Writer)

	// POSTGRES-MEDIA-CUTOVER: the canonical pgvector index worker and its two
	// post-translation ports exist only in media-SSOT mode; degraded mode
	// leaves them nil and the outbox keeps the legacy path.
	mediaIndex, err := buildOutboxMediaIndex(ctx, cfg, mediaPostgres, log)
	if err != nil {
		return nil, nil, err
	}

	// Deps + handler registration sub-blocks (extracted July 2026 to
	// build_outbox_handlers.go). Same order as the pre-split flat body:
	// deps construction → core handlers → optional/worker handlers.
	outboxDeps, metadataExportHandler := buildOutboxDeps(dbs, cfg, repos, jobs, qd, voiceoverDriver, mediaPostgres, log)
	// POSTGRES-MEDIA-CUTOVER: fail-closed single-owner assertion for the media
	// index plane (replaces the retired registerOutboxCoreHandlers no-op).
	if err := assertSingleMediaIndexOwner(cfg, eventsRegistry, mediaIndex.worker, log); err != nil {
		return nil, nil, err
	}

	dispatcher, canonicalCommitter, err := newOutboxDispatcher(dbs, repos, mediaPostgres, log, outboxEventsRepo, mediaIndex, drivePublisher)
	if err != nil {
		return nil, nil, err
	}

	// Blocco 3.1 commit 2/3 (June 2026): DriveDeleteHandler deps
	// (asset.drive.delete_requested.v1 → Drive Trash/Delete → atomic
	// AdvanceAndEmit to DRIVE_DELETED + emit index.delete_requested).
	outboxDeps.DriveDelete = newOutboxDriveDeleteDeps(repos, dispatcher, canonicalCommitter, driveDeleter, log)
	// MEDIA-SSOT (September 2026, audit finding #3): the canonical PG committer
	// emits asset.drive.delete_requested into the PostgreSQL outbox, so the
	// PostgreSQL worker must own a consumer for it. Registering only on the
	// SQLite registry left every PG-emitted delete event to dead-letter.
	if err := registerPostgresDeleteSagaHandlers(mediaIndex.worker, canonicalCommitter, outboxDeps.DriveDelete, log); err != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register PostgreSQL delete-saga handler: %w", err)
	}

	publisherHandler, driveUploadHandler, err := registerOutboxWorkers(eventsRegistry, log, outboxDeps, metadataExportHandler, jobs, stagingSvc, repo, drivePublisher)
	if err != nil {
		return nil, nil, err
	}

	if err := registerSplitJobsQueuedHandler(dbs, jobsRegistry, jobs); err != nil {
		return nil, nil, err
	}
	// Derived performance projection: job.completed events → performance_runs/
	// performance_steps. Best-effort (see registerPerformanceProjectionHandler).
	// The overlayEvents repo is the split media plane's outbox: the job.completed
	// handler uses it to wake the overlay-drive publications deferred during the
	// run so they become due at the terminal commit.
	if dbs.Jobs != nil {
		registerPerformanceProjectionHandler(jobsRegistry, dbs, dbs.Jobs, dbs.Main, log, outboxEventsRepo)
	} else {
		registerPerformanceProjectionHandler(eventsRegistry, dbs, dbs.Main, dbs.Main, log, outboxEventsRepo)
	}

	eventsPool, jobsEventsPool, poolCfg := newOutboxPools(cfg, dbs, outboxEventsRepo, eventsRegistry, jobsRegistry, log)
	startClosure := newOutboxStartClosure(ctx, eventsPool, jobsEventsPool, mediaIndex.worker, poolCfg, log)

	return &OutboxBundle{
		CanonicalWriter:    canonicalCommitter,
		Dispatcher:         dispatcher,
		EventsRepo:         outboxEventsRepo,
		EventsRegistry:     eventsRegistry,
		EventsPool:         eventsPool,
		JobsEventsRegistry: jobsRegistry,
		JobsEventsPool:     jobsEventsPool,
		MediaIndexWorker:   mediaIndex.worker,
		Publisher:          publisherHandler,
		DriveUploader:      driveUploadHandler,

		MediaIndexRequester:      mediaIndex.requester,
		MediaSearchTextRebuilder: mediaIndex.rebuilder,
	}, startClosure, nil
}

// zapLoggerAsMediaLogger adapts the composition zap logger to the
// pgmedia.Logger port (Info/Error). Field shape is untyped so the media
// package stays infrastructure-agnostic.
type zapMediaLogger struct{ l *zap.Logger }

func (z zapMediaLogger) Info(msg string, fields ...any) {
	if z.l != nil {
		z.l.Info(msg, zap.Any("fields", fields))
	}
}

func (z zapMediaLogger) Error(msg string, fields ...any) {
	if z.l != nil {
		z.l.Error(msg, zap.Any("fields", fields))
	}
}

// outboxDepsSagaPorts selects the delete/restore saga state ports.
// MEDIA-SSOT P0-2 (September 2026): when the canonical writer is the
// PostgreSQL media committer, the saga's lifecycle read/write and
// advance+emit hop route through the PG media SSOT; otherwise the legacy
// SQLite dispatcher ports are used (non-PG legacy path only).
func outboxDepsSagaPorts(
	clipsRepo jobsoutbox.LifecycleStateReader,
	dispatcher jobsoutbox.StateAdvancer,
	canonicalWriter assetspersistence.CanonicalAssetWriter,
) (jobsoutbox.LifecycleStateReader, jobsoutbox.ClipsLifecycleStateWriter, jobsoutbox.StateAdvancer) {
	if pgCommitter, ok := canonicalWriter.(*pgmedia.PostgresMediaCommitter); ok {
		return pgCommitter, pgCommitter, pgCommitter
	}
	return clipsRepo, clipsRepoAsLifecycleWriter(clipsRepo), dispatcher
}

// clipsRepoAsLifecycleWriter downcasts the shared ClipsRepository to the
// narrow lifecycle-writer port (legacy non-PG path only).
func clipsRepoAsLifecycleWriter(r jobsoutbox.LifecycleStateReader) jobsoutbox.ClipsLifecycleStateWriter {
	if w, ok := r.(jobsoutbox.ClipsLifecycleStateWriter); ok {
		return w
	}
	return nil
}

func zapLoggerAsMediaLogger(l *zap.Logger) pgmedia.Logger { return zapMediaLogger{l: l} }

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

// ── build_process_bundle.go (merged to respect the package file cap) ──
// Package app — ProcessBundle orchestration.
//
// BuildProcessBundle assembles the media-processing adapters. It owns the
// composition-time nil check on the pre-phase IndexDeps bundle and the
// ProcessBundle field-assembly; the helpers live in build_media_processor.go
// (wireMediaProcessor + newVLMClient). The former legacy vector runtime subsystem
// was deleted with the legacy vector engine on 2026-11-17; ClipIndexerService now
// carries only the PostgreSQL reindex requester.

// BuildProcessBundle builds the media-processing adapters and assembles
// the canonical *ProcessBundle. publisher is passed in directly —
// the Publisher is the canonical Drive upload canal, and
// processor.NewProcessor panics on nil publisher so a wiring gap is
// loud at boot.
//
// F2.8 (June 2026): the trailing arg swaps from `*drive.Uploader` to
// `delivery.Publisher`. The Publisher routes every Drive write from
// the processor through the DestinationRegistry + RequireSubpath +
// ConflictPolicy belt; the legacy direct-uploader bypass is closed.
// See DriveBundle.Publisher doc for the canonical one-owner-per-fact
// story (godlike/06).
//
// The body is intentionally thin: it calls the cross-file helpers
// (wireMediaProcessor, newVLMClient) and maps their outputs to the
// ProcessBundle field set. A nil indexDeps fails composition immediately,
// so a composition that forgot buildIndexDeps surfaces as a typed error.
// Fail-closed at the composition root: a nil outbox.Dispatcher leaves
// MediaProcessor=nil so worker / reprocess / ingest paths surface the
// missing dep rather than silently defaulting to a legacy path. A nil
// publisher surfaces in processor.NewProcessor as a typed panic at
// composition time (loud in the operator log) rather than a silent
// nil-deref on first upload.
func BuildProcessBundle(
	ctx context.Context,
	cfg *config.Config,
	dbs *Databases,
	log *zap.Logger,
	repos *RepoBundle,
	publisher delivery.Publisher,
	outbox *OutboxBundle,
	deps *IndexDeps,
	mediaPG *sql.DB,
	mediaConfig mediaexec.ExecutionConfig,
) (*ProcessBundle, error) {
	_ = ctx

	if deps == nil {
		return nil, fmt.Errorf("BuildProcessBundle: indexDeps is nil (composition forgot to call buildIndexDeps first?)")
	}

	mediaProcessor, err := wireMediaProcessor(outbox, repos, dbs, cfg, publisher, log, mediaPG, mediaConfig)
	if err != nil {
		return nil, err
	}

	vlmClient := newVLMClient(cfg)

	return &ProcessBundle{
		MediaProcessor:     mediaProcessor,
		ClipIndexerService: deps.ClipIndexerService,
		VLMClient:          vlmClient,
	}, nil
}

func buildIndexDeps(cfg *config.Config) *IndexDeps {
	return &IndexDeps{ClipIndexerService: capindexing.NewReindexRequesterHolder(cfg.ClipIndexer.Enabled)}
}

// newOutboxRegistries returns the media-plane registry and, when the jobs plane
// is split, its own registry: a split jobs plane must receive execution-plane
// completion events on its own outbox, never on the media one.
func newOutboxRegistries(dbs *Databases) (*outboxevents.HandlerRegistry, *outboxevents.HandlerRegistry) {
	eventsRegistry := outboxevents.NewHandlerRegistry()
	jobsRegistry := eventsRegistry
	if dbs.Jobs != nil {
		jobsRegistry = outboxevents.NewHandlerRegistry()
	}
	return eventsRegistry, jobsRegistry
}

// outboxMediaIndex groups the three PostgreSQL media-index ports. They are zero
// in degraded mode (the outbox keeps the legacy path); the requester and the
// rebuilder feed the TextTrackMaterializer AFTER translation.
type outboxMediaIndex struct {
	worker    *pgmedia.PostgresIndexWorker
	requester texttracks.IndexRequester
	rebuilder texttracks.SearchTextRebuilder
}

// buildOutboxMediaIndex builds the canonical pgvector index worker and its two
// post-translation ports. Fail-closed: in media-SSOT mode an empty sidecar URL
// aborts composition (no embedder → no vectors → no fake availability).
func buildOutboxMediaIndex(ctx context.Context, cfg *config.Config, mediaPostgres *sql.DB, log *zap.Logger) (outboxMediaIndex, error) {
	var index outboxMediaIndex
	if !cfg.MediaPostgreSQL.Enabled {
		return index, nil
	}
	if mediaPostgres == nil {
		return index, fmt.Errorf("BuildOutboxBundle: media PostgreSQL is enabled but mediaPostgres handle is nil (composition must call RequireMediaPostgres first)")
	}
	sidecarURL := strings.TrimSpace(cfg.ClipIndexer.ServerURL)
	if sidecarURL == "" {
		return index, fmt.Errorf("BuildOutboxBundle: media PostgreSQL is enabled but ClipIndexer.ServerURL is empty (the pgvector index worker requires the canonical E5 embedding sidecar)")
	}
	pgOutboxRepo := pgmedia.NewOutboxRepository(mediaPostgres)
	pgVectors := pgmedia.NewVectorSurfaceWriter(mediaPostgres)
	embedder := pgmedia.NewEmbedAssetTextAdapter(mediaPostgres, embeddings.NewHTTPTextEmbedderWithTimeout(sidecarURL, cfg.ClipIndexer.EmbedTimeout()))
	if err := pgVectors.EnsureEmbeddingFamily(ctx, "text", cfg.MediaPostgreSQL.EmbeddingModel, 768); err != nil {
		return index, fmt.Errorf("BuildOutboxBundle: pgvector embedding family bootstrap: %w", err)
	}
	index.worker = pgmedia.NewPostgresIndexWorker(pgOutboxRepo, pgVectors, embedder, cfg.MediaPostgreSQL.EmbeddingModel)
	// The materializer's post-translation reindex must land on this same
	// PostgreSQL outbox (the ONLY media index owner). ReindexRequester owns the
	// canonical envelope + terminal-conflict handling.
	index.requester = pgmedia.NewReindexRequester(mediaPostgres)
	// Rebuild media_assets.search_text from the metadata + READY transcripts
	// before that reindex, so the translations enter the E5 embedding. The
	// configured multilingual set filters the transcript tracks exactly as the
	// indexing path does.
	langsCSV, csvErr := BuildMultilingualLanguageCSV(ActiveMultilingualConfig(cfg), nil)
	if csvErr != nil {
		log.Warn("BuildOutboxBundle: multilingual index languages unavailable; search_text rebuild runs without a language filter", zap.Error(csvErr))
		langsCSV = ""
	}
	index.rebuilder = pgmedia.NewSearchTextRebuilder(mediaPostgres, langsCSV).WithLogger(log)
	return index, nil
}

// newOutboxDispatcher builds the canonical dispatcher AFTER the fail-closed
// media-index-owner assertion, so a nil ClipsRepo surfaces the typed error the
// contract requires instead of an internal panic in NewDispatcher. It also
// registers the PostgreSQL media handlers and hands the single canonical writer
// to the clips repository.
//
// The canonical media writer lives on PostgreSQL while this dispatcher's
// TxManager is the SQLite outbox pool: the two cannot share a transaction, so
// NewDispatcher receives the committer directly and has NO tx-bound media path.
// Every media commit goes through canonicalCommitter.CommitAndIndex (one
// PostgreSQL tx for media_assets + the outbox event).
func newOutboxDispatcher(dbs *Databases, repos *RepoBundle, mediaPostgres *sql.DB, log *zap.Logger, eventsRepo *outboxevents.Repository, index outboxMediaIndex, drivePublisher delivery.Publisher) (*outbox.Dispatcher, assetspersistence.CanonicalAssetWriter, error) {
	outboxTxMgr := outbox.NewManager(dbs.DualPool.Writer, log)
	canonicalCommitter, err := newCanonicalAssetCommitter(mediaPostgres, log)
	if err != nil {
		return nil, nil, fmt.Errorf("compose process: canonical media writer: %w", err)
	}
	if canonicalCommitter == nil {
		log.Warn("POSTGRES-MEDIA-CUTOVER: media PostgreSQL unavailable — canonical media writer degraded; media outbox events will dead-letter (graceful degrade, godlike/07)")
	}
	if index.worker != nil {
		if err := registerPostgresMediaOutboxHandlers(index.worker, drivePublisher, canonicalCommitter, repos.SubtitleArtifactRepo, log); err != nil {
			return nil, nil, fmt.Errorf("BuildOutboxBundle: register PostgreSQL media outbox handlers: %w", err)
		}
	}
	// The production dispatcher receives the single canonical writer directly.
	// Legacy ClipsUpserter/ClipsStateWriter arguments remain accepted by
	// NewDispatcher only for compatibility with older tests and adapters.
	if repos.ClipsRepo != nil {
		repos.ClipsRepo.SetCanonicalWriter(canonicalCommitter)
	}
	dispatcher := outbox.NewDispatcher(eventsRepo, outboxTxMgr, log, canonicalCommitter)
	log.Info("outbox dispatcher instantiated: canonical upsert+outbox_events enqueue path AND canonical delete+outbox_events enqueue path (VECTOR-002 PR7)")
	// POSTGRES-MEDIA-CUTOVER: in portable mode SaveDiscoveredAsset also routes
	// through the committer's own transaction, so the canonical writer must
	// satisfy the self-owned-transaction discovery port too.
	var _ outbox.DiscoveryCommitAndIndexCommitter = (*pgmedia.PostgresMediaCommitter)(nil)
	return dispatcher, canonicalCommitter, nil
}

// newOutboxDriveDeleteDeps populates the four narrow ports of the Drive delete
// saga. RegisterOptionalHandlers registers the handler only when ALL four are
// non-nil, so partial dev wiring skips at Info and never aborts boot; a nil
// driveDeleter in production is a dead-letter regression, hence the loud Warn.
func newOutboxDriveDeleteDeps(repos *RepoBundle, dispatcher *outbox.Dispatcher, canonicalCommitter assetspersistence.CanonicalAssetWriter, driveDeleter jobsoutbox.DriveDeleter, log *zap.Logger) jobsoutbox.DriveDeleteDeps {
	if driveDeleter == nil || repos.ClipsRepo == nil {
		log.Warn("outbox DriveDeleteHandler deps NOT wired (driveDeleter or ClipsRepo nil) — asset.drive.delete_requested.v1 events will dead-letter with 'no handler registered'")
		return jobsoutbox.DriveDeleteDeps{}
	}
	lifecycleReader, lifecycleWriter, stateAdvancer := outboxDepsSagaPorts(repos.ClipsRepo, dispatcher, canonicalCommitter)
	log.Info("outbox DriveDeleteHandler deps wired: asset.drive.delete_requested.v1 → Drive Trash/Delete → AdvanceAndEmit (Blocco 3.1 commit 2/3)")
	return jobsoutbox.DriveDeleteDeps{
		DrivePatchLifecycle:  lifecycleReader,
		DrivePatchLifecycleW: lifecycleWriter,
		DrivePatchStateAdv:   stateAdvancer,
		DriveDeleteHandler:   driveDeleter,
	}
}

// registerSplitJobsQueuedHandler registers the validation-only consumer of
// script.generate.queued on the execution plane. Generation submission writes
// that event to the execution-plane outbox, so the handler must exist there or
// the event is dead-lettered as "no handler registered".
func registerSplitJobsQueuedHandler(dbs *Databases, jobsRegistry *outboxevents.HandlerRegistry, jobs *JobsBundle) error {
	if dbs.Jobs == nil {
		return nil
	}
	queuedHandler, err := jobsoutbox.NewScriptGenerateQueuedHandler(jobs.Repo)
	if err != nil {
		return fmt.Errorf("BuildOutboxBundle: split jobs script.generate.queued handler: %w", err)
	}
	if err := jobsRegistry.Register(queuedHandler); err != nil {
		return fmt.Errorf("BuildOutboxBundle: register split jobs script.generate.queued handler: %w", err)
	}
	return nil
}

// newOutboxPools builds the media pool and, in split mode, the jobs pool that
// owns job.completed on the execution DB. It returns the shared poll config so
// the start closure drains both with the same cadence.
func newOutboxPools(cfg *config.Config, dbs *Databases, eventsRepo *outboxevents.Repository, eventsRegistry, jobsRegistry *outboxevents.HandlerRegistry, log *zap.Logger) (*outboxevents.Pool, *outboxevents.Pool, outboxevents.WorkerPollConfig) {
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
	eventsPool := outboxevents.NewPool("outbox-events", eventsRepo, eventsRegistry, log, outboxEventsCfg)
	var jobsEventsPool *outboxevents.Pool
	if dbs.Jobs != nil {
		jobsEventsPool = outboxevents.NewPool("jobs-outbox-events", outboxevents.NewRepository(dbs.Jobs.DB), jobsRegistry, log, outboxEventsCfg)
	}
	return eventsPool, jobsEventsPool, outboxEventsCfg
}

// newOutboxStartClosure returns the opaque start function the composition root
// runs after boot: both pools drain on ctx.Done(), and in media-SSOT mode the
// pgvector worker's loop owns the canonical asset.index.requested consumption.
func newOutboxStartClosure(ctx context.Context, eventsPool, jobsEventsPool *outboxevents.Pool, worker *pgmedia.PostgresIndexWorker, cfg outboxevents.WorkerPollConfig, log *zap.Logger) IOpaqueStartFunc {
	return func() error {
		if err := startOutboxEventsPool(ctx, eventsPool, cfg, log); err != nil {
			return err
		}
		if err := startOutboxEventsPool(ctx, jobsEventsPool, cfg, log); err != nil {
			return err
		}
		if worker != nil {
			concurrent.SafeGo("pg-media-index-worker", func() {
				worker.Run(ctx, pgmedia.DefaultPollInterval, pgmedia.DefaultLeaseTTL, zapLoggerAsMediaLogger(log))
			})
			log.Info("POSTGRES-MEDIA-CUTOVER: pgvector index worker drain loop started (asset.index.requested -> embed -> media_embeddings -> INDEXED -> completed)")
		}
		return nil
	}
}

// validateOutboxBundleInputs is the fail-closed gate of the outbox bundle. Each
// missing port names the composition call that must precede this one, so the
// operator sees the ordering mistake instead of a nil dereference later.
func validateOutboxBundleInputs(qd *IndexDeps, stagingSvc staging.Store, repo detail.ArtifactStageRepository, drivePublisher delivery.Publisher) error {
	if qd == nil {
		return fmt.Errorf("BuildOutboxBundle: indexDeps is nil (VECTOR-002 PR8 fail-closed; composition forgot to call buildIndexDeps first?)")
	}
	if stagingSvc == nil {
		// FASE 3 Push 3.1c: the Publisher worker is the canonical
		// outbox→staging adapter. It MUST be wired here (otherwise
		// publish_requested events dead-letter on the first emission).
		return fmt.Errorf("BuildOutboxBundle: stagingSvc is required (FASE 3 Push 3.1c; composition must call BuildStagingBundle before BuildOutboxBundle)")
	}
	if repo == nil {
		// FASE 3 Push 3.1e: the DriveUploader worker consumes
		// artifact.Repository for the MarkPublished fenced-CAS. A nil repo is
		// fail-closed — composition must inject the same artifact.Repository
		// port that StagingBundle uses (single-writer SSOT).
		return fmt.Errorf("BuildOutboxBundle: repo is required (FASE 3 Push 3.1e; composition must inject StagingBundle.Repository)")
	}
	if drivePublisher == nil {
		// FASE 3 Push 3.1e: the DriveUploader worker is the canonical
		// outbox→Drive adapter. It MUST be wired here (otherwise
		// artifact.staged.v1 events dead-letter the moment the saga's first
		// Publish step).
		return fmt.Errorf("BuildOutboxBundle: drivePublisher is required (FASE 3 Push 3.1e; composition must inject DriveBundle.Publisher)")
	}
	return nil
}
