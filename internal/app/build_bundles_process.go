// Package app — Drive bundle construction (split out from composition.go
// in commit ci/composition-split wave-1 of the 5-commit refactor for
// problem #8).
//
// This file owns the Drive adapters + MediaStore derivation + StyleRegistry
// loading for the canonical Google Drive integration. Extracted from
// composition.go so bundle debt is split per AGENTS.md Pattern 5 (1 concept
// per focused file) and BuildDriveBundle's own body remains pure (no
// concurrent goroutine spawns — composition_test.go::
// TestComposition_NoGoroutinesSpawned_FrozenSiteCount).
//
// commit ci/composition-split wave-1 (June 2026): replaced the legacy
// post-ctor setter pair (`mediaStore.SetAssetTree + SetTreeSource`) with
// a single `drive.NewStoreWithOptions(..., drive.StoreOptions{AssetTree,
// TreeSources})` call so the dependency graph lands at the ctor boundary.
package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// BuildDriveBundle constructs the Drive adapters + MediaStore + DestResolver.
// Loads StyleRegistry at the top so ensureStyleDriveFolders (called via the
// returned startDriveBackgroundFolders closure) receives the non-nil pointer.
//
// PR9-A (June 2026): BuildDriveBundle returns an IOpaqueStartFunc closure
// that defers side-effecting initialisation (Drive folder validation,
// style-folder pre-creation, storage directory creation) to the lifecycle.
// The bundle itself is fully populated on return.
func BuildDriveBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, search *SearchBundle) (*DriveBundle, IOpaqueStartFunc, error) {
	styleRegistry, _ := generation.NewStyleRegistry("config/generation_styles.yaml")

	docClient, err := drive.NewDocClient(ctx, cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	driveClient, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		log.Warn("Google Drive client not initialized", zap.Error(err))
	}

	// PG-011-residual-cleanup (June 2026): the previous
	// resolveRuntimeDestinations function (a no-op alias for
	// configOnlyDestinations — both pre-existing branches converged
	// on the same cfg-derived *DriveDestinations) was deleted;
	// dests is now derived once, unconditionally. driveClient
	// remains a dependency for driveUploader construction, the
	// mediaStore block below, and the startClosure's folder
	// validation, but it is no longer threaded through a
	// dests-resolution alias that ignored it.
	var driveUploader *drive.Uploader
	var dests = configOnlyDestinations(cfg)
	if driveClient != nil {
		driveUploader = &drive.Uploader{Service: driveClient, Log: log}
	}

	var mediaStore *drive.Store
	var destResolver asset.Resolver
	if driveClient != nil {
		storageResolver := drive.NewResolver(
			drive.MediaRoot(cfg.Storage.MediaPath()),
			drive.DriveRoot(dests.RootFolder()),
		)

		// Construct the StoreOptions at the ctor boundary — no post-ctor
		// SetAssetTree / SetTreeSource calls. TreeSources maps Drive folder
		// IDs to their logical tree source names.
		storeOpts := drive.StoreOptions{}
		if search != nil && search.AssetTreeService != nil {
			storeOpts.AssetTree = search.AssetTreeService
			storeOpts.TreeSources = map[string]string{
				dests.ImagesFolder(): "image",
			}
			log.Info("mediaStore: Drive roots configured",
				zap.String("images_folder_id", dests.ImagesFolder()))
		}

		mediaStore = drive.NewStoreWithOptions(
			storageResolver,
			driveUploader,
			dests.RootFolder(),
			dests.ImagesFolder(),
			"", // VideoAIRoot removed (PR June 2026) — pass empty string
			dests.SoundEffectsRoot,
			log,
			storeOpts,
		)

		destResolver = drive.NewDestinationResolver(mediaStore)
	}

	// PR9-A (June 2026): side-effecting initialisation is delegated to
	// startDriveBackgroundFolders (defined below). Package-level function
	// so the source-level goroutine-count freeze test reports zero spawns
	// in BuildDriveBundle's own body.
	// Lifecycle-runtime-ownership (June 2026): now returns error so
	// serverLifecycle.Start can abort on required folder validation failure.
	startClosure := func() error {
		return startDriveBackgroundFolders(ctx, cfg, driveClient, driveUploader, dests, styleRegistry, log)
	}

	return &DriveBundle{
		DriveClient:   driveClient,
		DriveUploader: driveUploader,
		DocClient:     docClient,
		DriveDests:    dests,
		MediaStore:    mediaStore,
		DestResolver:  destResolver,
		StyleRegistry: styleRegistry,
	}, startClosure, nil
}

// startDriveBackgroundFolders performs the side-effecting Drive init that
// was previously inlined in BuildDriveBundle (PR9-A, June 2026). It
// pre-creates style folders on Drive, validates critical Drive folder
// paths, and ensures local storage directories exist.
//
// Lifecycle-runtime-ownership (June 2026): now returns error on required
// folder validation failure. Style folder creation remains async (background
// after readiness passes). Local storage directory creation errors are
// logged as warnings (they are non-fatal).
//
// Invoked by the lifecycle after WireRegistry completes, before the HTTP
// server begins accepting requests.
func startDriveBackgroundFolders(
	ctx context.Context,
	cfg *config.Config,
	driveClient *gdrive.Service,
	driveUploader *drive.Uploader,
	dests *DriveDestinations,
	styleRegistry *generation.StyleRegistry,
	log *zap.Logger,
) error {
	// Style folder pre-creation: async after readiness (optional).
	if driveClient != nil && dests.ImagesFolder() != "" && dests.ImagesFolder() != dests.MediaRoot {
		concurrent.SafeGo("drive-style-folders", func() {
			ensureStyleDriveFolders(ctx, driveUploader, dests.ImagesFolder(), styleRegistry, log)
		})
		log.Info("Style Drive folders using Images root", zap.String("folder_id", dests.ImagesFolder()))
	}

	// Required folder validation: synchronous, returns error on failure.
	if driveClient != nil {
		for name, folderID := range map[string]string{
			"images": dests.ImagesFolder(),
		} {
			if folderID == "" {
				continue
			}
			if _, err := driveClient.Files.Get(folderID).Fields("id, name").Context(ctx).Do(); err != nil {
				return fmt.Errorf("required Drive folder %q (id=%s) validation failed: %w", name, folderID, err)
			}
			log.Info("Drive folder validated",
				zap.String("folder_name", name), zap.String("folder_id", folderID))
		}
	}

	// Local storage directories: optional (logged as warnings).
	for _, dir := range []string{
		cfg.Storage.DataDir, cfg.Storage.VoiceoversPath(), cfg.Storage.AssetsPath(),
		cfg.Storage.DownloadsPath(), cfg.Storage.BackupsPath(), cfg.Storage.TempPath(),
		cfg.Storage.AnimationsPath(), cfg.Storage.YoutubeClipsPath(),
		cfg.Storage.ArtlistPath(), cfg.Storage.ImagesPath(),
	} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Warn("Failed to create storage directory", zap.String("path", dir), zap.Error(err))
		}
	}
	return nil
}

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

	outboxDeps := &jobsoutbox.Deps{
		DB:                   dbs.main.DB,
		HTTPClient:           httpClient,
		MetadataDir:          cfg.Storage.FullPath("asset_metadata"),
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
	if err := jobsoutbox.RegisterOptionalHandlers(eventsRegistry, log, outboxDeps); err != nil {
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
