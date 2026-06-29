// Package app — Process + Outbox bundle construction (FASE 2.B PR1, June 2026).
//
// Originally this file also owned the Drive bundle construction
// (BuildDriveBundle + startDriveBackgroundFolders), which PR1 extracted to:
//   - internal/app/build_bundles_drive.go   (BuildDriveBundle — Drive client
//     + folder resolver init, MediaStore derivation, StyleRegistry load)
//   - internal/app/build_drive_startup.go  (startDriveBackgroundFolders —
//     Drive folder bootstrap, AC validation, retry warmup)
//
// This file now owns ONLY:
//   - BuildProcessBundle (Qdrant-derivable media: MediaProcessor,
//     ClipIndexerService, VLMClient, Qdrant subsystems, search ports)
//   - BuildOutboxBundle (canonical ingestion-path outbox.Dispatcher +
//     outbox_events.Pool, registration of core + optional handlers)
//   - startOutboxEventsPool (SafeGo launchers: pool Start + drain on
//     ctx.Done())
//   - Qdrant compile-time assertions (clipindexer.VectorStoreIndexer +
//     jobsoutbox.AssetDeleter) — composition-time port conformance per
//     AGENTS.md Pattern 0.
//
// Each of these bundle constructors corresponds to ONE bundle concept
// per AGENTS.md Pattern 5 (no half-bundles, no `Build*And*` composites).
// PR1 is MOVE-only: zero logic changes in this file, zero call-site
// changes anywhere in the codebase.
package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	metadataexport "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	filesmetadataexport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// FASE 2.B PR1 (June 2026): BuildDriveBundle + startDriveBackgroundFolders
// moved to build_bundles_drive.go + build_drive_startup.go respectively.
// Both functions are referenced by composition.go::NewComposition via
// `package app`-level visibility (cross-file within the same package).
// The remaining bundle constructors below take a *drive.Uploader argument
// (BuildProcessBundle) — drive is therefore still imported in this file
// but only as a parameter type, no construction logic here.

// Compile-time assertions for QDRANT-003 wiring + PR 3
// (fix/qdrant-outbox-fail-closed). Per AGENTS.md Pattern 0 the
// composition root is where the typed-port contract is enforced: every
// port referenced from outbox.Deps must statically implement its
// concrete so a future refactor misses the compile, not the first
// outbox replay.
// Compile-time assertions for QDRANT-003 wiring + PR 3
// (fix/qdrant-outbox-fail-closed). Per AGENTS.md Pattern 0 the
// composition root is where the typed-port contract is enforced: every
// port referenced from outbox.Deps must statically implement its
// concrete so a future refactor misses the compile, not the first
// outbox replay.
//
// TODO #8 (June 2026) drift-fix: the canonical
// `internal/infrastructure/database/sqlite/assets` package is
// imported here as `sqassets` (matching the existing convention in
// build_bundles_core.go and the rest of the composition root). The
// previously-bare `assets.ClipsRepository` reference at line 205
// was an UNIMPORTED symbol — this file pre-existing-did-not-compile
// because no local `assets` or `sqassets` alias was set. Renaming
// the assertion to `sqassets.ClipsRepository` plus adding the
// import pins static conformance for jobsoutbox.AssetDeleter exactly
// like the previous code intended. The corresponding direct
// assignment at the AssetDeleter wiring site (`outboxDeps.AssetDeleter
// = repos.ClipsRepo`, gated on `cfg.Qdrant.Enabled && repos.ClipsRepo
// != nil`) is type-safe because the assertion below proves the
// static conformance.
var (
	_ clipindexer.VectorStoreIndexer = (*qdrant.IndexWriter)(nil)
	_ jobsoutbox.AssetDeleter        = (*sqassets.ClipsRepository)(nil)
)

// BuildProcessBundle builds media-processing adapters. driveUploader
// passed in directly.
//
// QDRANT-003 (June 2026): Qdrant vector-store capability reintroduced.
// IndexWriter + ClipIndexerService are constructed in the canonical
// pre-phase (composition.go::buildQdrantDeps) so BuildOutboxBundle can
// run BEFORE BuildProcessBundle, and threaded back here via the qd
// *QdrantDeps input. EnsureSchema is deferred to wire_services.go
// startup plan (startup-time).
//
// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed):
// BuildProcessBundle gains `outbox *OutboxBundle` + `qd *QdrantDeps` as
// the last 2 positional args. MediaProcessor is now constructed INLINE
// here — the previous PR-7 deferred-hydration strategy
// (`BuildProcessBundle.MediaProcessor=nil + hydrateMediaProcessor`) is
// gone. Composition graph is now a strict DAG:
//
//	qdrantDeps(no deps) -> outbox(reads qd) -> process(reads outbox+qd) ->
//	  domains(reads process+outbox)
//
// Fail-closed at the composition root: a nil outbox.Dispatcher leaves
// MediaProcessor=nil so worker / reprocess / ingest paths surface the
// missing dep rather than silently defaulting to the legacy path. A
// nil qd fails composition immediately (composition forgot to call
// buildQdrantDeps first?).
func BuildProcessBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, driveUploader *drive.Uploader, outbox *OutboxBundle, qd *QdrantDeps) (*ProcessBundle, error) {
	_ = ctx

	if qd == nil {
		return nil, fmt.Errorf("BuildProcessBundle: qdrantDeps is nil (QDRANT-002 PR8 fail-closed; composition forgot to call buildQdrantDeps first?)")
	}

	// PR 8 (June 2026): MediaProcessor constructed INLINE here. The
	// previous `MediaProcessor=nil + hydrateMediaProcessor` deferred-
	// hydration strategy (PR 7) is gone — composition order is
	// qd -> outbox -> process so outbox.Dispatcher is available at this
	// point in NewComposition's strict-DAG orchestration. Fail-closed:
	// a nil outbox.Dispatcher leaves MediaProcessor=nil.
	var mediaProcessor asset.Processor
	if outbox != nil && outbox.Dispatcher != nil {
		mutationsDisp, err := newMutationsDispatcherAdapter(outbox.Dispatcher)
		if err != nil {
			return nil, fmt.Errorf("BuildProcessBundle: mutations dispatcher adapter: %w", err)
		}
		mediaProcessor = initMediaProcessor(cfg, dbs.main, repos.Assets.Repository(), repos.Assets,
			repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(),
			mutationsDisp, log, driveUploader)
		log.Info("PR 8: MediaProcessor constructed inline with canonical mutations.AssetMutationDispatcher (clipsRegistry UPSERT routed through outbox+tx)")
	} else {
		log.Warn("BuildProcessBundle: outbox.Dispatcher is nil — MediaProcessor left nil (QDRANT-002 PR8 fail-closed; worker + reprocess + ingest paths will surface the missing dep)")
	}

	vlmClient := vlm.NewClient(vlm.Config{
		Enabled:   cfg.VLM.Enabled,
		Endpoint:  cfg.VLM.URL,
		Model:     cfg.VLM.Model,
		TimeoutMs: cfg.VLM.TimeoutMs,
		Weight:    cfg.VLM.Weight,
	})

	// QDRANT-005 Phase 1 Blocker 2 (June 2026): Qdrant subsystems are
	// sourced from qd.Runtime (PR 4, June 2026,
	// refactor/single-qdrant-runtime) so there is exactly ONE
	// *qdrant.Client + ONE *IndexSchema per process. Pre-PR4 the
	// BuildProcessBundle body had its OWN qdrant.NewClient + DefaultV3Schema
	// call, second to the ones in composition.go::buildQdrantDeps — the
	// two *Clients were distinct pointer values (so wire_only
	// invariants like api-key header could silently drift between the
	// two) but functionally identical. After PR 4 all subsystems
	// read from the runtime. nil qd.Runtime → all subsystems nil
	// (Qdrant disabled feature flag).
	var (
		collectionMgr    *qdrant.CollectionManager
		vectorSvc        assetsearch.VectorStorePort
		qdrantClient     *qdrant.Client
		qdrantHealthProbe *qdrant.HealthProbe
		locatorCleaner    *qdrant.LocatorCleaner
		qdrantSearcher    *qdrant.Searcher
	)

	if qd.Runtime != nil {
		collectionMgr    = qd.Runtime.Manager
		vectorSvc        = qd.Runtime.SearchAdapter
		qdrantClient     = qd.Runtime.Client
		qdrantHealthProbe = qd.Runtime.Health
		locatorCleaner   = qd.Runtime.Cleaner
		qdrantSearcher   = qd.Runtime.Searcher

		log.Info("QDRANT-005 PR4: HealthProbe + LocatorCleaner + Searcher + CollectionManager sourced from single QdrantRuntime (BuildProcessBundle)",
			zap.String("qdrant_url", cfg.Qdrant.BaseURL),
			zap.String("schema_version", qd.Runtime.Schema.Version))
		log.Info("QDRANT-004 PR4: VectorStorePort sourced from single QdrantRuntime.SearchAdapter (BuildProcessBundle)")
	} else {
		log.Info("QDRANT-003: Qdrant disabled — no Qdrant components wired (BuildProcessBundle)")
	}

	return &ProcessBundle{
		MediaProcessor:     mediaProcessor,
		ClipIndexerService: qd.ClipIndexerService,
		VLMClient:          vlmClient,
		CollectionManager:  collectionMgr,
		QdrantDeleter:      qd.QdrantDeleter,
		QdrantRuntime:      qd.Runtime, // PR 4: first-class facade exposed at ProcessBundle level
		VectorSvc:          vectorSvc,
		QdrantClient:       qdrantClient,
		QdrantHealthProbe:  qdrantHealthProbe,
		LocatorCleaner:     locatorCleaner,
		QdrantSearcher:     qdrantSearcher,
	}, nil
}

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
func BuildOutboxBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, qd *QdrantDeps, jobs *JobsBundle) (*OutboxBundle, IOpaqueStartFunc, error) {
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
