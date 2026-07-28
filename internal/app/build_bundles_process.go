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
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	metadataexport "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox/metadataexport"
	publishdrive "github.com/Marcuss-ops/PipelineGen/internal/application/publish_drive"
	publishoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/publish_outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/application/staging"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/domain/artifact"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	sqmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	filesmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/httpclient"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
// lifecycle. The bundle itself is fully populated on return.
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
func BuildOutboxBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, qd *QdrantDeps, jobs *JobsBundle, voiceoverDriver jobsoutbox.VoiceoverCleanupDriver, stagingSvc staging.Store, repo artifact.Repository, drivePublisher delivery.Publisher) (*OutboxBundle, IOpaqueStartFunc, error) {
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
	outboxEventsRepo := outboxevents.NewRepository(dbs.dualPool.Writer)

	// PR 3 fix/qdrant-outbox-fail-closed BL-1 fix: dispatcher
	// construction moved to AFTER the fail-closed handler
	// registration (see below). The previous order constructed the
	// dispatcher + ClipsStateWriter BEFORE the fail-closed check, so
	// when repos.ClipsRepo was nil the call returned an internal
	// panic (NewDispatcher / NewMultiClipsUpserter panic on nil
	// inputs) instead of the typed error the fail-closed contract
	// requires. The dispatcher block is now anchored after
	// RegisterOptionalHandlers.

	eventsRegistry := outboxevents.NewHandlerRegistry()

	// PR-REFACTOR-P0-IO-BINDER-HTTP (July 2026): route the outbox http.Client
	// construction through internal/infrastructure/httpclient.NewDefaultClient
	// (the canonical owner of *http.Client construction for the application
	// port surface). The result satisfies ports.Client, which is the
	// field type of InfraDeps.HTTPClient (consumed by the DeliveryHandler).
	httpClient := httpclient.NewDefaultClient(30 * time.Second)

	var hmacSecrets [][]byte
	if cur := strings.TrimSpace(cfg.Security.DeliveryHMACSecret); cur != "" {
		hmacSecrets = append(hmacSecrets, []byte(cur))
	}
	if prev := strings.TrimSpace(cfg.Security.DeliveryHMACSecretPrevious); prev != "" {
		hmacSecrets = append(hmacSecrets, []byte(prev))
	}

	// SourceVersionQuerier is the narrow port consumed by the
	// IndexingHandler source_version supersede gate (PR 11 follow-up,
	// June 2026). The production concrete is *assets.ClipsRepository
	// (already wired into the dispatcher's MultiClipsUpserter; same
	// instance also implements SourceVersionQuerier via a thin
	// delegating method). nil ClipsRepo → nil SourceVersionQuerier →
	// IndexingHandler skips the supersede gate (acceptable in test
	// dbs; production always wires non-nil).
	//
	// Wave 16 (June 2026): typed-port direct assignment per
	// AGENTS.md Pattern 0. The previous
	// `any(repos.ClipsRepo).(jobsoutbox.AssetSourceChecker)`
	// raw cast is replaced because *assets.ClipsRepository
	// statically implements the port (compile-time assertion at
	// internal/infrastructure/database/sqlite/assets/clips_repository.go).
	// Dropping the `, ok` form is safe: the assertion fails the build
	// if port drift ever breaks the static implementation contract.
	// PR 11 follow-up extends the assertion to SourceVersionQuerier
	// (single-method port) — the previous AssetSourceChecker port
	// (GetClip → walk Asset) is removed entirely.
	var sourceQuerier jobsoutbox.SourceVersionQuerier
	if repos.ClipsRepo != nil {
		sourceQuerier = repos.ClipsRepo
	}

	// Step 2 (June 2026): pre-build the canonical MetadataExportHandler
	// via the new typed-port adapters. The composition root is the ONLY
	// place infra concrete types meet application ports — the
	// outbox.Deps struct no longer needs MetadataDir because the
	// handler gets its output dir as part of HandlerDeps at wire time.
	metadataExportResolver := sqmetadataexport.NewSQLiteAdapter(dbs.dualPool.Writer)
	metadataExportWriter := &filesmetadataexport.FileWriter{}
	metadataExportDeps := metadataexport.HandlerDeps{
		Resolver:  metadataExportResolver,
		Writer:    metadataExportWriter,
		OutputDir: cfg.Storage.FullPath("asset_metadata"),
		Log:       log,
	}
	metadataExportHandler := metadataexport.NewMetadataExportHandler(metadataExportDeps)

	outboxDeps := &jobsoutbox.Deps{
		Infra: jobsoutbox.InfraDeps{
			DB:          dbs.dualPool.Writer,
			HTTPClient:  httpClient,
			HMACSecrets: hmacSecrets,
			InsecureDev: cfg.Security.DeliveryInsecureDev,
		},
		Jobs: jobsoutbox.JobDeps{
			Jobs:                 jobs.Service,
			SourceVersionQuerier: sourceQuerier,
		},
	}
	// PR 4 (June 2026, refactor/single-qdrant-runtime): wire
	// qd.QdrantDeleter (outbox.VectorPointDeleter; == qd.Runtime.Writer
	// when Qdrant is enabled) directly into outbox.Deps.Jobs.VectorPointDeleter.
	// The previous `any` cast `qd.QdrantDeleter.(jobsoutbox.QdrantDeleter)`
	// is gone: the compile-time assertion at
	// internal/infrastructure/qdrant/index_writer.go pins the
	// conformance (`_ jobsoutbox.VectorPointDeleter = (*qdrant.IndexWriter)(nil)`),
	// and qd.QdrantDeleter's field type is already
	// jobsoutbox.VectorPointDeleter so direct assignment is type-safe.
	if qd.QdrantDeleter != nil {
		outboxDeps.Jobs.VectorPointDeleter = qd.QdrantDeleter
	}
	// PR 3 fix/qdrant-outbox-fail-closed (#4): wire the canonical
	// AssetDeleter so IndexDeleteHandler has BOTH its dep slots
	// populated. *assets.ClipsRepository statically implements the
	// local outbox.AssetDeleter port (compile-time assertion at the
	// top of this file pins GetClip + SoftDelete + SetIndexState
	// conformance). Before this wiring, IndexDeleteHandler
	// registered in a partially-wired state whenever
	// Qdrant.Enabled=true but composer's ClipsRepo wiring failed —
	// every asset.index.delete_requested event then dead-lettered
	// with "no handler for event type X". Fail-closed wiring: only
	// when cfg.Qdrant.Enabled AND ClipsRepo is present.
	if cfg.Qdrant.Enabled && repos.ClipsRepo != nil {
		outboxDeps.Jobs.AssetDeleter = repos.ClipsRepo
	}
	// P0.7 Wave 21 Step 10/12 (June 2026): voiceover orphan cleanup
	// driver (production concrete = drive.Admin, which saturates the
	// narrow VoiceoverCleanupDriver port via its DeleteFile method
	// — structural conformance, no wrapper needed). nil is
	// tolerated — RegisterOptionalHandlers unconditionally registers
	// the handler, and the handler's driver==nil branch logs+skips
	// the Drive delete step (local file removal still runs via
	// stdlib os.Remove, no port ceremony). Production wiring always
	// supplies a non-nil adapter via composition.go (built from
	// driveBundle.Admin).
	if voiceoverDriver != nil {
		outboxDeps.Jobs.VoiceoverCleanupDriver = voiceoverDriver
	}
	// PR 3 fix/qdrant-outbox-fail-closed (#4 + #5): core handlers are
	// fail-closed when Qdrant is enabled. The previous
	// `log.Warn("failed to register outbox events handlers", err)`
	// silently downgraded a wiring bug to a runtime dead-letter on
	// the first asset.index.requested event. Now: cfg.Qdrant.Enabled
	// AND any core dep missing → return err which BuildOutboxBundle
	// propagates up to NewComposition so an operator
	// misconfiguration aborts boot rather than running with a broken
	// outbox.
	if cfg.Qdrant.Enabled {
		if err := jobsoutbox.RegisterCoreHandlers(eventsRegistry, log, qd.ClipIndexerService, outboxDeps); err != nil {
			return nil, nil, fmt.Errorf("BuildOutboxBundle: register core outbox handlers (fail-closed): %w", err)
		}
	} else {
		// Dev / qdrant-off mode: still register a no-op asset.index.requested
		// consumer so image-generation jobs do not dead-letter their indexing
		// event. The handler preserves the envelope validation + supersede
		// checks but routes the final IndexClip call to a no-op concrete.
		sourceQuerier := jobsoutbox.SourceVersionQuerier(nil)
		if repos != nil && repos.ClipsRepo != nil {
			sourceQuerier = repos.ClipsRepo
		}
		if err := eventsRegistry.Register(jobsoutbox.NewIndexingHandler(noopIndexClipper{}, sourceQuerier, log)); err != nil {
			return nil, nil, fmt.Errorf("BuildOutboxBundle: register qdrant-off indexing handler: %w", err)
		}
		log.Info("outbox indexing handler registered in no-op mode because qdrant is disabled")
	}
	// Optional handlers: best-effort. Missing deps here are logged
	// and skipped; missing deps do NOT abort boot (delivery,
	// metadata_export, provider_sync are non-essential at boot).
	// Step 2 (June 2026): the pre-built metadataexport.MetadataExportHandler
	// (composition-root owned) is passed to RegisterOptionalHandlers via
	// a new metadataExportHandler arg.
	if err := jobsoutbox.RegisterOptionalHandlers(eventsRegistry, log, outboxDeps, metadataExportHandler); err != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register optional outbox handlers: %w", err)
	}

	// FASE 3 Push 3.1c (July 2026): register the canonical
	// Promote→Publisher worker. Drains
	// `artifact.publish_requested.v1` events from outbox_events
	// and forwards them to staging.Store.Stage (which then
	// co-emits `artifact.staged.v1` via
	// Repository.InsertWithOutbox — the canonical atomic
	// primitive). Fail-closed: a nil/errored handler
	// registration aborts boot — a half-wired publisher would
	// dead-letter every publish_requested event on the first
	// emission, which is a worse failure mode than a clean
	// compose-time abort.
	publisherHandler, pubErr := publishoutbox.NewHandler(stagingSvc, log)
	if pubErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: publish_outbox.NewHandler (fail-fast at construction): %w", pubErr)
	}
	if regErr := eventsRegistry.Register(publisherHandler); regErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register publish_outbox handler (fail-closed): %w", regErr)
	}
	log.Info("outbox publish handler registered: artifact.publish_requested.v1 → staging.Store.Stage (FASE 3 Push 3.1c)")

	// FASE 3 Push 3.1e (July 2026): register the canonical
	// Stage→Publish worker. Drains `artifact.staged.v1` events
	// (atomically co-emitted by Repository.InsertWithOutbox in
	// Push 3.1c) and forwards each event to
	// delivery.Publisher.Publish (the canonical Drive upload
	// canal) + Repository.MarkPublished with a canonical JSON
	// PublishedLocation payload. Fail-closed: a nil/errored
	// handler registration aborts boot — a half-wired
	// DriveUploader would dead-letter every staged.v1 event on
	// the first emission, which is a worse failure mode than a
	// clean compose-time abort.
	//
	// The handler consumes the SAME artifact.Repository port
	// that staging.StoreService.Stage uses (canonical single-
	// writer; the Repository is the typed cursor to the same
	// underlying *artifactstages.Repository concrete — godlike/06
	// SSOT per FASE 3 Spina Dorsale). Threading the Repository
	// explicitly into BuildOutboxBundle (rather than re-fetching
	// from a downstream service) keeps the wiring fail-closed:
	// a NULL repo at compose-time is a typed-error abort, not a
	// silent runtime nil-deref.
	driveUploadHandler, driveErr := publishdrive.NewHandler(repo, drivePublisher, log)
	if driveErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: publish_drive.NewHandler (fail-fast at construction): %w", driveErr)
	}
	if regErr := eventsRegistry.Register(driveUploadHandler); regErr != nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: register publish_drive handler (fail-closed): %w", regErr)
	}
	log.Info("outbox publish_drive handler registered: artifact.staged.v1 → delivery.Publisher.Publish + Repository.MarkPublished (FASE 3 Push 3.1e)")

	// ── Dispatcher + pool construction (post fail-closed). ────────
	multiClipsUp := outbox.NewMultiClipsUpserter(
		map[string]outbox.ClipsUpserter{
			"youtube": repos.ClipsRepo,
			"stock":   repos.ClipsRepo,
			"artlist": repos.ClipsRepo,
		},
		repos.ClipsRepo,
		log,
	)
	stateWriter := outbox.ClipsStateWriter(repos.ClipsRepo)
	outboxTxMgr := outbox.NewManager(dbs.dualPool.Writer, log)
	dispatcher := outbox.NewDispatcher(multiClipsUp, stateWriter, outboxEventsRepo, outboxTxMgr, log)
	log.Info("outbox dispatcher instantiated: canonical upsert+outbox_events enqueue path AND canonical delete+outbox_events enqueue path (QDRANT-002 PR7)")

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

	startClosure := func() error {
		return startOutboxEventsPool(ctx, eventsPool, outboxEventsCfg, log)
	}

	return &OutboxBundle{
		Dispatcher:     dispatcher,
		EventsRepo:     outboxEventsRepo,
		EventsRegistry: eventsRegistry,
		EventsPool:     eventsPool,
		Publisher:      publisherHandler,
		DriveUploader:  driveUploadHandler,
	}, startClosure, nil
}

type noopIndexClipper struct{}

func (noopIndexClipper) IndexClip(context.Context, string) error { return nil }

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

// ── Media-processor wiring (moved from build_media_processor.go, Phase 5 consolidation, June 2026) ──

// F2.8 (June 2026): the trailing arg swaps from `*drive.Uploader`
// to `delivery.Publisher`. The Publisher is the canonical canal for
// every Drive write from the processor; the legacy direct-uploader
// bypass is closed. Compile-time assertion
// `var _ delivery.Publisher = (*drive.Uploader)(nil)` lives in
// internal/infrastructure/drive/publisher.go (already pinned there)
// so this wiring is type-safe.
func wireMediaProcessor(
	outbox *OutboxBundle,
	repos *RepoBundle,
	dbs *databases,
	cfg *config.Config,
	publisher delivery.Publisher,
	log *zap.Logger,
) (asset.Processor, error) {
	if outbox == nil || outbox.Dispatcher == nil {
		log.Warn("BuildProcessBundle: outbox.Dispatcher is nil — MediaProcessor left nil (QDRANT-002 PR8 fail-closed)")
		return nil, nil
	}
	mutationsDisp, err := newMutationsDispatcherAdapter(outbox.Dispatcher)
	if err != nil {
		return nil, fmt.Errorf("wireMediaProcessor: mutations dispatcher adapter: %w", err)
	}
	mp := initMediaProcessor(
		cfg,
		dbs.main,
		repos.Assets.Repository(),
		repos.Assets,
		repos.Assets.LocationRepository(),
		repos.Assets.ProcessingRepository(),
		mutationsDisp,
		log,
		publisher,
	)
	log.Info("PR 8: MediaProcessor constructed inline with canonical mutations.AssetMutationDispatcher (F2.8: publisher wired)")
	return mp, nil
}

func newVLMClient(cfg *config.Config) *vlm.Client {
	return vlm.NewClient(vlm.Config{
		Enabled:      cfg.VLM.Enabled,
		Endpoint:     cfg.VLM.URL,
		Model:        cfg.VLM.Model,
		ModelVersion: cfg.VLM.ModelVersion,
		TimeoutMs:    cfg.VLM.TimeoutMs,
		Weight:       cfg.VLM.Weight,
	})
}
