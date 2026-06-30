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
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	metadataexport "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox/metadataexport"
	sqmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	filesmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/metadataexport"
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
// wiring: the new voiceoverCleanup arg threads the *voiceoverDriveAdapter
// (production concrete for jobsoutbox.VoiceoverCleanupDriver, satisfied
// structurally by the same struct that already satisfies
// voiceover.DriveUploaderPort.DeleteFile) into the outbox Deps so
// VoiceoverCleanupHandler.register runs with a non-nil Drive delete
// surface. nil voiceoverCleanup is tolerated — the handler still
// handles local file removal (stdlib os.Remove, no port ceremony)
// and logs+skips the Drive delete branch with an operator-visible
// warning. Production wiring always supplies a non-nil adapter.
func BuildOutboxBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, qd *QdrantDeps, jobs *JobsBundle, voiceoverDriver jobsoutbox.VoiceoverCleanupDriver) (*OutboxBundle, IOpaqueStartFunc, error) {
	if qd == nil {
		return nil, nil, fmt.Errorf("BuildOutboxBundle: qdrantDeps is nil (QDRANT-002 PR8 fail-closed; composition forgot to call buildQdrantDeps first?)")
	}
	outboxEventsRepo := outboxevents.NewRepository(dbs.main.DB)

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

	httpClient := &http.Client{Timeout: 30 * time.Second}

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
	// `interface{}(repos.ClipsRepo).(jobsoutbox.AssetSourceChecker)`
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
	metadataExportResolver := sqmetadataexport.NewSQLiteAdapter(dbs.main.DB)
	metadataExportWriter := &filesmetadataexport.FileWriter{}
	metadataExportDeps := metadataexport.HandlerDeps{
		Resolver:  metadataExportResolver,
		Writer:    metadataExportWriter,
		OutputDir: cfg.Storage.FullPath("asset_metadata"),
		Log:       log,
	}
	metadataExportHandler := metadataexport.NewMetadataExportHandler(metadataExportDeps)

	outboxDeps := &jobsoutbox.Deps{
		DB:                   dbs.main.DB,
		HTTPClient:           httpClient,
		HMACSecrets:          hmacSecrets,
		InsecureDev:          cfg.Security.DeliveryInsecureDev,
		Jobs:                 jobs.Service,
		SourceVersionQuerier: sourceQuerier,
	}
	// PR 4 (June 2026, refactor/single-qdrant-runtime): wire
	// qd.QdrantDeleter (outbox.VectorPointDeleter; == qd.Runtime.Writer
	// when Qdrant is enabled) directly into outbox.Deps.VectorPointDeleter.
	// The previous `interface{}` cast `qd.QdrantDeleter.(jobsoutbox.QdrantDeleter)`
	// is gone: the compile-time assertion at
	// internal/infrastructure/qdrant/index_writer.go pins the
	// conformance (`_ jobsoutbox.VectorPointDeleter = (*qdrant.IndexWriter)(nil)`),
	// and qd.QdrantDeleter's field type is already
	// jobsoutbox.VectorPointDeleter so direct assignment is type-safe.
	if qd.QdrantDeleter != nil {
		outboxDeps.VectorPointDeleter = qd.QdrantDeleter
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
		outboxDeps.AssetDeleter = repos.ClipsRepo
	}
	// P0.7 Wave 21 Step 10/12 (June 2026): voiceover orphan cleanup
	// driver (production concrete = *voiceoverDriveAdapter from
	// adapters_voiceover_use_case.go, which saturates the narrow
	// VoiceoverCleanupDriver port via its DeleteFile method). nil is
	// tolerated — RegisterOptionalHandlers unconditionally registers
	// the handler, and the handler's driver==nil branch logs+skips
	// the Drive delete step (local file removal still runs via
	// stdlib os.Remove, no port ceremony). Production wiring always
	// supplies a non-nil adapter via composition.go (built from
	// driveBundle.Admin).
	if voiceoverDriver != nil {
		outboxDeps.VoiceoverCleanupDriver = voiceoverDriver
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
	outboxTxMgr := outbox.NewManager(dbs.main.DB, log)
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
		eventsPool.Start(ctx, 1)
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
		Enabled:   cfg.VLM.Enabled,
		Endpoint:  cfg.VLM.URL,
		Model:     cfg.VLM.Model,
		TimeoutMs: cfg.VLM.TimeoutMs,
		Weight:    cfg.VLM.Weight,
	})
}
