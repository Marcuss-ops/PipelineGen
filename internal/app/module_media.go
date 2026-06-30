package app

import (
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	assetsdiag "github.com/Marcuss-ops/PipelineGen/internal/api/assets/diagnostics"
	assetregister "github.com/Marcuss-ops/PipelineGen/internal/api/assets/register"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/api/assets/search"
	assetsfx "github.com/Marcuss-ops/PipelineGen/internal/api/assets/soundeffect"
	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/api/assets/storage"
	assetvoice "github.com/Marcuss-ops/PipelineGen/internal/api/assets/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	voiceoverpkg "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoverreconcile "github.com/Marcuss-ops/PipelineGen/internal/application/assets/reconciliation/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	infraassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// processRunnerAdapter is a package-level adapter for the infrastructure ProcessRunner port.
// Used by ScraperHandler and other handlers in registry.go that need subprocess execution.
var processRunnerAdapter = infraassets.NewProcessRunnerAdapter()

// toolCheckerAdapter is a package-level adapter for the infrastructure ToolChecker port.
// Used by YouTubeClipHandler and system handler to check external tool availability.
var toolCheckerAdapter = infraassets.NewToolCheckerAdapter()

// dbHealthCheckerAdapter is a package-level adapter for the infrastructure DBHealthChecker port.
// Used by system handler to check database health.
var dbHealthCheckerAdapter = infraassets.NewDBHealthCheckerAdapter(nil)

// AssetsWiring holds the Assets module wiring.
type AssetsWiring struct {
	Module               module.Module
	DeletionSvc          *deletion.DeletionService
	InternalMediaHandler *assetstorage.Handler
	// Wave 21 PR 10 (June 2026): the canonical SearchAggregator
	// (search.NewAggregator). The legacy clipssearch.Service +
	// appsearch.Service wirings were deleted in PR 10 — see
	// architecture/deprecations.yaml records
	// PR-SEARCH-LEGACY-CLIPSSEARCH + PR-SEARCH-LEGACY-CROSSPROVIDER.
	SearchAggregator *search.Aggregator
	// PR-2 (June 2026): the canonical SearchFanOut (decorator
	// wrapping the canonical Aggregator). Exposed via this
	// wiring handle so other consumers (diagnostics + future
	// Health probes) read the SHARED instance rather than
	// constructing a parallel one. == deps.Search.SearchFanOut alias.
	SearchFanOut search.SearchFanOut
}

// WireAssets creates the unified Assets handler and module.
//
// PR4d-chunk2 (June 2026): takes *AssetsModuleDeps + 8 narrow direct args
// PR3 (June 2026): providerRegistry added for constructor injection
// (replaces post-construction SetProviderRegistry).
// Wave 16 (June 2026): realtimeSvc typed to `assetsapi.RealtimeMatcher`
// per AGENTS.md Pattern 0 (typed-port abstraction). The realtime
// package was removed in commit d61068b3; the typed parameter stays
// nil-safe (typed-nil of an interface equals nil interface).
// Wave 21 PR 10 (June 2026): catalogRepo, providerRegistry no longer
// feed the legacy appsearch.Service wire (which was removed). They
// remain in the signature because BuildSearchBackends consumes
// providerRegistry (cross-capability bridge to providers.Registry),
// and the future Wave 22 follow-ups may reuse catalogRepo.
func WireAssets(cfg *config.Config, log *zap.Logger, deps *AssetsModuleDeps, jobs *JobsBundle, voiceoverSvc *voiceoverpkg.Service, voiceoverSync *voiceoverreconcile.Service, realtimeSvc assetsapi.RealtimeMatcher, catalogRepo *catalog.Repository, maintenanceSvc *maintenance.Service, providerRegistry *providers.Registry, dispatcher *outbox.Dispatcher) (*AssetsWiring, error) {
	// PG-034 (June 2026): vectorStore arg removed — Qdrant capability deleted.
	// FASE 9 Step 2: extract concrete *drive.Uploader from Admin port.
	// Type assertion is safe — DriveBundle.Admin is always *drive.Uploader
	// (or nil when Drive is not configured).
	var driveUploader *driveutil.Uploader
	if deps.Delivery.Admin != nil {
		if up, ok := deps.Delivery.Admin.(*driveutil.Uploader); ok {
			driveUploader = up
		}
	}
	var assetRepo asset.Repository
	if deps.Core.Assets != nil {
		assetRepo = deps.Core.Assets.Repository()
	}

	// ── Wave 21 PR 10 (June 2026): canonical SearchAggregator
	//    + the PR-2 SearchFanOut decorator wrapping it are
	//    pre-built by WireRegistry and stamped onto
	//    deps.Search.SearchFanOut + deps.Search.SearchBackendRegistry.
	//    WireAssets CONSUMES the pre-built instance — it does
	//    not construct its own. The single shared instance
	//    invariant guarantees stats counters aggregate across
	//    every search entry-point (YouTube + Assets + Mediasearch
	//    + FindDuplicates) instead of fragmenting per-handler.
	//    See AssetsModuleDeps package doc in assets_core.go for the
	//    sub-area regrouping rationale (Core 8 + Search 5 +
	//    Delivery 1 + Background 2; P0-2 commit 1).
	//    The legacy clipssearch.Service + appsearchsvc.Service
	//    wirings are gone (PR-10 CUTOVER delivered). The
	//    aggregator is the SSOT for the Search capability; see
	//    architecture/deprecations.yaml records PR-SEARCH-LEGACY-*
	//    + the new PR-SEARCH-LEGACY-PROVIDERS-AGGREGATOR.
	searchBackends := deps.Search.SearchBackendRegistry
	searchFanOut := deps.Search.SearchFanOut
	if searchFanOut == nil {
		// Composition-bug guard: WireRegistry is required to
		// stamp deps.Search.SearchFanOut BEFORE WireAssets runs. A
		// nil slot is a wiring-time defect; surface it here as
		// a fail-closed error instead of letting downstream
		// handlers silently degrade to 503 on every Search call.
		return nil, fmt.Errorf("WireAssets: deps.Search.SearchFanOut is nil (composition root must call BuildCanonicalSearchFanOut before WireAssets)")
	}
	searchAggregator := search.NewAggregator(searchBackends, &zapSearchLogAdapter{log: log})
	log.Info("WireAssets: consumed pre-built canonical SearchFanOut",
		zap.Int("backends", len(searchBackends.All())))

	folderMemSvc := foldermemory.NewService(log, deps.Core.ClipsRepo)
	metaWriter := semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
	deletionSvc := deletion.NewDeletionService(deps.Core.ClipsRepo, deps.Core.ClipsRepo, deps.Core.ClipsRepo, deps.Core.VoiceoverRepo, deps.Core.ImageRepo, driveUploader, deps.Core.AssetTreeService, deps.Core.AssetIndexService, dispatcher, log)

	// PR8 (June 2026): idemHandler is passed in from WireRegistry (see
	// registry.go). WireAssets does NOT construct its own Idempotency
	// instance — the cleanup goroutine lives once at the registry level
	// and is owned by ComposeRoot.IdempotencyMiddleware (Stop() on
	// shutdown). A nil idemHandler (e.g. nil-store test fixture) is
	// tolerated; the handler wrappers fall through to pass-through.
	var idemHandler gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if deps.Background.IdempotencyStore != nil {
		idemHandler = deps.Background.IdempotencyStoreHandler
	}

	// PR12c (June 2026): wire the dispatcher's port, NOT the concrete
	// *outbox.Dispatcher, into the unified clips handler. Adapter is
	// constructed only when dispatcher is non-nil (partial deploy path);
	// when dispatcher is nil the handler falls back to raw repo.UpsertClip
	// via the `if h.dispatcher != nil` guard in UpdateClip.
	var clipsDispatcherPort appclips.ClipIndexDispatcherPort
	if dispatcher != nil {
		clipsDispatcherPort = &clipsDispatcherAdapter{disp: dispatcher}
	}
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): wire the
	// canonical mutations.AssetMutationDispatcher SSOT for the wider
	// application-layer producers (BulkUploadWorker + ReprocessUseCase
	// constructed inside NewHandler). Both adapters wrap the same
	// *outbox.Dispatcher; the SSOT one is the canonical surface per
	// mutations/dispatcher.go ("application producers MUST depend on the
	// SSOT shape"). Strict fail-closed — composition errors when
	// dispatcher is nil, mirroring WireArtlist's strict composition-time
	// check (QDRANT-002 PR7 invariant).
	mutationsDisp, err := newMutationsDispatcherAdapter(dispatcher)
	if err != nil {
		return nil, fmt.Errorf("WireAssets: %w", err)
	}
	// S1a (June 2026): construct the shared EnrichUseCase ONCE at
	// composition time. The clipsHandler receives it via Deps.EnrichUC
	// so the same instance is used by:
	//   (a) the handler's EnrichMedia / CreateClip / UploadVideoClip /
	//       ReindexClip paths, and
	//   (b) the media.enrich worker registered below, which the
	//       handlers now enqueue to instead of spawning goroutines
	//       with context.WithoutCancel.
	enrichUC := appclips.NewEnrichUseCase(assetRepo, deps.Search.ClipIndexerService, metaWriter, log)
	// W14 PR2 slice 3 (June 2026): construct the BulkUploadWorker
	// from typed ports so the handler never imports infra for the
	// bulk-upload job path. The concrete adapters already exist in
	// clips_adapters_index.go.
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): the
	// mutations.AssetMutationDispatcher SSOT is the 7th positional arg
	// so the worker routes media_assets UPSERT through the canonical
	// outbox+tx writer (QDRANT-002 atomicity invariant).
	//
	// HC-1 (June 2026): the `cfg` 5th positional arg is now constructed
	// with appjobs.Compose() as the typed TimeoutResolver — the
	// bulk_upload worker uses cfg.JobTimeout(TypeBulUploadYouTubeClips)
	// to derive its 2*time.Hour literal via the canonical Registry
	// (replaces the pre-HC-1 hard-coded context.WithTimeout(ctx, 2*time.Hour)
	// in bulk_upload_worker.go::HandleJob).
	bulkUploadWorker := appclips.NewBulkUploadWorker(
		newClipsDriveAdapter(driveUploader, driveUploader),
		deps.Delivery.Publisher,
		newClipsRepoAdapter(deps.Core.ClipsRepo),
		newClipsIndexerAdapter(deps.Search.ClipIndexerService),
		newClipsHashAdapter(),
		newClipsCfgAdapter(cfg, appjobs.Compose()),
		mutationsDisp,
		log,
	)
	// P1.5 (June 2026): CUTOVER — construct the upload UseCase from the
	// canonical typed ports declared in internal/application/clips/upload/ports.go.
	// DriveUploader, IndexDispatcher, Config, and TreeBuilder are type aliases
	// of the parent clips.* ports (ClipDriveUploaderPort, etc.) so the existing
	// adapters satisfy them directly.
	// P0.1 (June 2026): Artifact is now mandatory — the composition root
	// wires *artifacts.Service (constructed in BuildDomainBundle) via
	// artifactServiceAdapter (clips_adapters_artifact.go). A nil
	// ArtifactService in CoreDeps causes NewUseCase to return error
	// at boot time instead of silently producing HTTP 500 at request time.
	var uploadUC *appupload.UseCase
	uploadUC, err = appupload.NewUseCase(appupload.UseCaseDeps{
		// F2.9 (June 2026): DriveUploader dropped — Publisher is the
		// single canonical Drive-write canal. Composition-time ARG
		// signature of UseCaseDeps no longer has DriveUploader.
		Artifact:      NewArtifactServiceAdapter(deps.Core.ArtifactService),
		Publisher:     deps.Delivery.Publisher,
		Dispatcher:    clipsDispatcherPort,
		Config:        newClipsCfgAdapter(cfg, appjobs.Compose()),
		TreeBuilder:   newClipsAssetTreeAdapter(deps.Core.AssetTreeService),
		JobsSvc:       jobs.Facade,
		ProcessRunner: processRunnerAdapter,
		Log:           log,
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: upload.NewUseCase: %w", err)
	}
	// F2.9 (June 2026): construct the application-layer ReuploadUseCase
	// wired to the canonical delivery.Publisher. The legacy composition
	// wired driveUploader (ClipDriveUploaderPort) which has been
	// retired; NewReuploadUseCase panics on nil publisher as a
	// composition-time fail-fast (mirrors processor.NewProcessor,
	// F2.8 closure). Folder-root mapping is config-driven; the artlist
	// + stock path markers use the Storage methods discovered via
	// cfg audit — empty PathMarker for a source disables dynamic
	// resolution for that source (clip.FolderID() is then required).
	reuploadFolderRoots := map[string]appclips.ReuploadFolderRoot{
		"clips":   {RootID: cfg.Drive.ClipsFolder(), PathMarker: cfg.Storage.YoutubeClipsPath()},
		"youtube": {RootID: cfg.Drive.ClipsFolder(), PathMarker: cfg.Storage.YoutubeClipsPath()},
		"artlist": {RootID: cfg.Drive.ArtlistFolder(), PathMarker: cfg.Storage.ArtlistPath()},
		"stock":   {RootID: cfg.Drive.StockFolder(), PathMarker: cfg.Storage.FullPath("stock")},
	}
	reuploadUC := appclips.NewReuploadUseCase(
		assetRepo,
		deps.Delivery.Publisher,
		clipsDispatcherPort,
		reuploadFolderRoots,
		log,
	)
	// PR 2 (June 2026): construct the application-layer ClipOpsService so
	// the HTTP handler can delegate Reconcile / Cleanup / VerifyClip to a
	// single canonical service instead of duplicating the business logic
	// locally. The clipsAdapterBundle exposes typed ports for every dep
	// the service takes; the new clipsJobsPortAdapter bridges
	// domain/job.Service.Enqueue into the service's narrowed DTO. The
	// cleanup port is a thin pass-through over deletionSvc (signatures
	// already match the clips.CleanupServicePort contract).
	clipsOpsPorts := newClipsAdapterBundle(
		cfg, log,
		deps.Core.ClipsRepo, deps.Core.ClipsRepo, deps.Core.ClipsRepo,
		deps.Core.VoiceoverRepo, deps.Core.ImageRepo,
		driveUploader, metaWriter, deps.Search.ClipIndexerService,
		folderMemSvc, deps.Core.AssetTreeService,
		nil, // vectorSvc removed PG-034
		// HC-1 (June 2026): pass the typed TimeoutResolver (canonical
		// impl: appjobs.Compose() — *jobs.Registry) so the cfg adapter
		// in the bundle has the timeouts port wired. Mirrors the
		// newClipsCfgAdapter(appjobs.Compose()) call above for the
		// BulkUploadWorker.
		appjobs.Compose(),
	)
	clipOpsSvc := appclips.NewClipOpsService(
		clipsOpsPorts.SourceResolver,
		clipsOpsPorts.VoiceoverRepo,
		clipsOpsPorts.ImagesRepo,
		clipsOpsPorts.DriveUploader,
		newClipsCleanupPortAdapter(deletionSvc),
		newClipsJobsPortAdapter(jobs.Facade),
		clipsDispatcherPort,
		log,
	)
	// Wave 21 PR 10: SearchSvc is the canonical *search.Aggregator
	// (was *assetclipssearch.Service, deleted in PR 10). The legacy
	// map[string]AdvancedSearchRepo hand-rolled fan-out is replaced
	// by the Aggregator's 4-key dedup + ranking pipeline.

	// Blocco C1-Step 5 (June 2026): Clips capability is now built via
	// the canonical clips.Build(deps) (api.Descriptor, error) contract,
	// matching the artlist / youtube precedent. The orchestrator
	// *Handler is constructed inside Build and captured by the
	// returned Module's closure. clipsDescriptor.Handler stays
	// accessible to the one non-HTTP consumer
	// (newAssetRegisterService → sourcingEnrichmentAdapter →
	// handler.EnrichAndIndexClip).
	clipsDescriptor, err := clipsapi.Build(clipsapi.Dependencies{
		ClipsRepo:        deps.Core.ClipsRepo,
		AssetRepo:        assetRepo,
		DeletionSvc:      deletionSvc,
		DriveAdmin:    driveUploader,
		MediaProcessor:   deps.Core.MediaProcessor,
		AssetTreeSvc:     deps.Core.AssetTreeService,
		MetaWriter:       metaWriter,
		ClipIndexer:      deps.Search.ClipIndexerService,
		JobsSvc:          jobs.Facade,
		Cfg:              cfg,
		Log:              log,
		VoiceoverRepo:    deps.Core.VoiceoverRepo,
		ImagesRepo:       deps.Core.ImageRepo,
		FolderMemSvc:     folderMemSvc,
		ProcessRunner:    processRunnerAdapter,
		Dispatcher:       clipsDispatcherPort,
		EnrichUC:         enrichUC,
		SearchSvc:        searchAggregator,
		BulkUploadWorker: bulkUploadWorker,
		ClipOpsService:   clipOpsSvc,
		UploadUC:         uploadUC,
		ReuploadUC:       reuploadUC, // F2.9: wired via delivery.Publisher (was nil pre-F2.9)
		Idempotency:      idemHandler,
		EnabledFunc:      func() bool { return true },
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: clips.Build: %w", err)
	}
	// Blocco C1-Step 5 (June 2026): Build returns the canonical
	// api.Descriptor surface. The Descriptor's forwarder methods
	// (Name/Enabled/RegisterRoutes) are interface-level; the
	// non-HTTP consumer below (sourcingEnrichmentAdapter →
	// handler.EnrichAndIndexClip) needs the raw orchestrator
	// *Handler, which is reachable only via the concrete
	// *ClipsDescriptor.Handler field. Type-assert once and
	// reuse the concrete for both consumers (the concrete
	// *ClipsDescriptor satisfies api.Descriptor structurally,
	// so the assetsapi.NewModule call below accepts it).
	clipsDesc, ok := clipsDescriptor.(*clipsapi.ClipsDescriptor)
	if !ok {
		return nil, fmt.Errorf("WireAssets: clips.Build returned unexpected descriptor type %T (want *clipsapi.ClipsDescriptor)", clipsDescriptor)
	}

	// Blocco C1-Step 12 (June 2026; user-documented Step 11):
	// Storage capability is now built via the canonical
	// assetstorage.Build(deps) (api.Descriptor, error) contract,
	// matching the artlist / youtube / clips / stock / voiceover /
	// soundeffect / register precedent. The Handler is constructed
	// inside Build and captured by the returned StorageDescriptor's
	// Module closure. The composition site type-asserts ONCE to
	// *assetstorage.StorageDescriptor (fail-closed) and reuses the
	// concrete for both consumers:
	//
	//  (a) the assetsapi.NewModule(..., Storage: storageDesc, ...)
	//      call (the concrete *StorageDescriptor satisfies
	//      api.Descriptor structurally — its RegisterRoutes
	//      forwarder delegates to the embedded api.Module
	//      which mounts POST /api/media/sync on the parent
	//      /api/media group); AND
	//
	//  (b) the AssetsWiring.InternalMediaHandler forwarder
	//      (storageDesc.Handler) — the QDRANT-001 closure kept a
	//      narrow api.MediaInternalRouter port at
	//      internal/api/routes.go::Setup(). The Router binds
	//      this via Router.SetInternalMediaHandler, which calls
	//      storageDesc.Handler.RegisterInternalMediaRoutes(...) for
	//      the /internal/v1/media/sync server-to-server surface.
	//
	// The storage capability is unique in the assets tree today:
	// exposure of a raw Handler alongside the Module is needed
	// because RegisterInternalMediaRoutes is a Handler-level method
	// (not a Module-level one). Mirrors the clips precedent
	// exactly. See internal/api/assets/storage/module.go godoc for
	// the canonical rationale + pattern parity list.
	storageDescriptor, err := assetstorage.Build(assetstorage.Dependencies{
		Jobs:        jobs.Facade,
		CatalogSync: deps.Core.CatalogSyncService,
		EnabledFunc: func() bool { return true }, // storage is always on in production
		ModuleOpts:  nil,                         // no per-feature middleware (matches pre-Step-12 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: storage.Build: %w", err)
	}
	storageDesc, ok := storageDescriptor.(*assetstorage.StorageDescriptor)
	if !ok || storageDesc == nil {
		return nil, fmt.Errorf("WireAssets: storage.Build returned unexpected descriptor type %T (want *assetstorage.StorageDescriptor)", storageDescriptor)
	}

	// ── PR 3 (June 2026): diagnostics + search wired with real ports ─
	// Diagnostics: IndexHealth via realtime.Service + AssetStats via ClipsRepository.
	// QDRANT-005 Fase 1 (June 2026): diagIndexHealthAdapter rewired with
	// real SQLite + Qdrant deps. Nil-tolerant — when deps.Core.ClipsRepo is
	// nil the handler falls back to 503.
	var diagSvc *appdiag.Service
	if deps.Core.ClipsRepo != nil {
		diagSvc = appdiag.NewService(
			&diagIndexHealthAdapter{clips: deps.Core.ClipsRepo, qdrant: nil, collectionName: ""},
			&diagAssetStatsAdapter{clips: deps.Core.ClipsRepo},
			&zapDiagLogAdapter{log: log},
		)
	}
	// Blocco C1-Step 10 (June 2026): Diagnostics capability is
	// now built via the canonical diagnostics.Build(deps)
	// (api.Descriptor, error) contract, matching the artlist /
	// youtube / clips / stock / voiceover / soundeffect /
	// register precedent. The Handler is constructed inside
	// Build and captured by the returned DiagnosticsDescriptor's
	// Module closure. The composition site type-asserts ONCE to
	// *diagnostics.DiagnosticsDescriptor (fail-closed) and
	// reuses the concrete for the assetsapi.NewModule(...,
	// Diagnostics: dd, ...) call (the concrete
	// *DiagnosticsDescriptor satisfies api.Descriptor
	// structurally). The diagnostics capability has no non-HTTP
	// consumer in the codebase (the 3 routes are the entire
	// public surface, reachable only via HTTP), so the
	// Descriptor surface is the smallest possible — just
	// `Module` field + forwarder methods (matches the stock /
	// voiceover / soundeffect / register precedent exactly).
	//
	// The *appdiag.Service is constructed at the composition
	// root from 3 typed-port adapters (IndexHealthAdapter +
	// AssetStatsAdapter + ZapLogAdapter) per AGENTS.md
	// Pattern 0 — the api/ layer never builds it.
	diagnosticsDescriptor, err := assetsdiag.Build(assetsdiag.Dependencies{
		Service:     diagSvc,
		EnabledFunc: func() bool { return true }, // diagnostics is always on in production
		ModuleOpts:  nil,                         // no per-feature middleware (matches pre-Step-10 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: diagnostics.Build: %w", err)
	}
	dd, ok := diagnosticsDescriptor.(*assetsdiag.DiagnosticsDescriptor)
	if !ok || dd == nil {
		return nil, fmt.Errorf("WireAssets: diagnostics.Build returned unexpected descriptor type %T (want *assetsdiag.DiagnosticsDescriptor)", diagnosticsDescriptor)
	}

	// Search handler: now consumes the canonical Aggregator
	// (constructed above). The legacy cross-provider Service
	// (appsearchsvc.Service) was deleted in PR 10 — see
	// PR-SEARCH-LEGACY-CROSSPROVIDER deprecation record.
	//
	// Blocco C1-Step 11 (June 2026): Search capability is now
	// built via the canonical search.Build(deps)
	// (api.Descriptor, error) contract, matching the artlist /
	// youtube / clips / stock / voiceover / soundeffect /
	// register / diagnostics precedent. The Handler is
	// constructed inside Build and captured by the returned
	// SearchDescriptor's Module closure. The composition site
	// type-asserts ONCE to *search.SearchDescriptor
	// (fail-closed) and reuses the concrete for the
	// assetsapi.NewModule(..., Search: sd, ...) call (the
	// concrete *SearchDescriptor satisfies api.Descriptor
	// structurally). The search capability has no non-HTTP
	// consumer in the codebase (POST /search is the entire
	// public surface, reachable only via HTTP), so the
	// Descriptor surface is the smallest possible — just
	// `Module` field + forwarder methods (matches the stock /
	// voiceover / soundeffect / register / diagnostics
	// precedent exactly).
	//
	// The *search.Aggregator is constructed at the composition
	// root from the pre-built SearchBackends + ZapLogAdapter
	// per AGENTS.md Pattern 0 — the api/ layer never builds it.
	searchDescriptor, err := assetsearch.Build(assetsearch.Dependencies{
		Aggregator:  searchAggregator,
		EnabledFunc: func() bool { return true }, // search is always on in production
		ModuleOpts:  nil,                         // no per-feature middleware (matches pre-Step-11 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: search.Build: %w", err)
	}
	sd, ok := searchDescriptor.(*assetsearch.SearchDescriptor)
	if !ok || sd == nil {
		return nil, fmt.Errorf("WireAssets: search.Build returned unexpected descriptor type %T (want *assetsearch.SearchDescriptor)", searchDescriptor)
	}
	if diagSvc != nil {
		log.Info("diagnostics and search services wired with production ports")
	} else {
		log.Warn("diagnostics service NOT fully wired — some routes will return 503")
	}

	// ── PR 4 (June 2026): extract voiceover, soundeffect, register ─
	// Blocco 4 EXPAND slim (June 2026): the standalone voiceover handler
	// is now async-only via the voiceover.generate job (the canonical
	// /generate route binds GenerateVoiceoversCommand + enqueues via
	// jobsSvc). GroupsResolver + VoiceoverRootFolder are no longer
	// referenced in this module layer — the legacy /groups +
	// /generate-with-group /batch /promo /sync routes have all been
	// retired per Wave 21 / PR-VOICEOVER-RECOVERY (V1..V7); see
	// architecture/deprecations.yaml PR-VO-SUNSET-MACHINERY-RETIRE
	// for the tracked closure of the 2026-06-28 → 2026-09-26 Sunset
	// window. voiceoverSvc + voiceoverSync remain in WireAssets's
	// signature for the typed-port chain (godlike/07 framework).
	//
	// Blocco C1-Step 7 (June 2026): Voiceover capability is now built
	// via the canonical voiceover.Build(deps) (api.Descriptor, error)
	// contract, matching the artlist / youtube / clips / stock
	// precedent. The Handler is constructed inside Build and captured
	// by the returned VoiceoverDescriptor's Module closure. The
	// composition site type-asserts ONCE to
	// *voiceover.VoiceoverDescriptor (fail-closed) and reuses the
	// concrete for the assetsapi.NewModule(..., Voiceover: vd, ...)
	// call (the concrete *VoiceoverDescriptor satisfies api.Descriptor
	// structurally). The voiceover capability has no non-HTTP
	// consumer in the codebase (/generate is the entire public
	// surface, reachable only via HTTP), so the Descriptor surface is
	// the smallest possible — just `Module` field + forwarder methods
	// (matches the stock precedent exactly).
	voiceoverDescriptor, err := assetvoice.Build(assetvoice.Dependencies{
		Jobs:        jobs.Facade,
		EnabledFunc: func() bool { return true }, // voiceover is always on in production (no feature flag)
		ModuleOpts:  nil,                         // no per-feature middleware for the voiceover capability (matches pre-Step-7 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: voiceover.Build: %w", err)
	}
	vd, ok := voiceoverDescriptor.(*assetvoice.VoiceoverDescriptor)
	if !ok || vd == nil {
		return nil, fmt.Errorf("WireAssets: voiceover.Build returned unexpected descriptor type %T (want *assetvoice.VoiceoverDescriptor)", voiceoverDescriptor)
	}

	// SoundEffect: wrapped repos + uploader + metaWriter + dispatcher via
	// sfxports adapters. PG-003 (June 2026) replaced the four concrete
	// infrastructure reach-throughs with structural ports so the api/
	// layer stays thin (per AGENTS.md Pattern 0).
	// PR 6 (June 2026, codex/qdrant-api-writers-fail-closed):
	// sfxDispatcher is added so the Generate path routes through the
	// canonical *outbox.Dispatcher.EnqueueAndIndex instead of the legacy
	// direct h.clipsRepo.Upsert fallback — the same fail-closed pattern
	// the four clips writers in this PR migrate to. Nil-tolerant in
	// composition: when dispatcher is nil (test fixtures, partial
	// deploys), the handler returns 503 to the operator. Production
	// wiring in cmd/server always supplies a non-nil dispatcher.
	// FASE 7 (June 2026): deps.Delivery.Publisher is passed
	// directly to NewHandler; sfxports.PublisherPort is the narrow
	// surface of delivery.Publisher (both share
	// delivery.PublishRequest/PublishResult types), so the concrete
	// delivery.Publisher satisfies sfxports.PublisherPort
	// structurally — no explicit sfxPublisherAdapter is needed
	// (matches the parallel session's b9412605 wiring).
	//
	// Blocco C1-Step 8 (June 2026): SoundEffect capability is now
	// built via the canonical soundeffect.Build(deps) (api.Descriptor,
	// error) contract, matching the artlist / youtube / clips /
	// stock / voiceover precedent. The Handler is constructed
	// inside Build and captured by the returned
	// SoundeffectDescriptor's Module closure. The composition
	// site type-asserts ONCE to *soundeffect.SoundeffectDescriptor
	// (fail-closed) and reuses the concrete for the
	// assetsapi.NewModule(..., SoundEffect: sd, ...) call (the
	// concrete *SoundeffectDescriptor satisfies api.Descriptor
	// structurally). The soundeffect capability has no non-HTTP
	// consumer in the codebase (/generate is the entire public
	// surface, reachable only via HTTP), so the Descriptor surface
	// is the smallest possible — just `Module` field + forwarder
	// methods (matches the stock / voiceover precedent exactly).
	sfxClips := &sfxClipsRepoAdapter{repo: deps.Core.ClipsRepo}
	sfxMeta := &sfxSemanticWriterAdapter{w: metaWriter}
	sfxResolver := &sfxResolverAdapter{r: driveutil.NewResolver("data", "")}

	sfxDispatcher := newSfxDispatcherAdapter(dispatcher)
	soundeffectDescriptor, err := assetsfx.Build(assetsfx.Dependencies{
		ClipsRepo:              sfxClips,
		MetaWriter:             sfxMeta,                 // nil-tolerant at request time (default tag/searchText)
		Resolver:               sfxResolver,             // mandatory — Generate calls unconditionally
		Dispatcher:             sfxDispatcher,           // mandatory — Build is fail-closed on nil
		Publisher:              deps.Delivery.Publisher, // FASE 7: structural match (delivery.Publisher → sfxports.PublisherPort)
		SoundEffectsRootFolder: cfg.Drive.SoundEffectsRootFolder,
		ProcessRunner:          processRunnerAdapter,
		EnabledFunc:            func() bool { return true }, // soundeffect is always on in production
		ModuleOpts:             nil,                         // no per-feature middleware (matches pre-Step-8 wiring)
		Logger:                 log,
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: soundeffect.Build: %w", err)
	}
	soundeffectDesc, ok := soundeffectDescriptor.(*assetsfx.SoundeffectDescriptor)
	if !ok || soundeffectDesc == nil {
		return nil, fmt.Errorf("WireAssets: soundeffect.Build returned unexpected descriptor type %T (want *assetsfx.SoundeffectDescriptor)", soundeffectDescriptor)
	}

	// Register: the HTTP layer now depends on a single sourcing use case.
	// Blocco C1-Step 5 (June 2026): the sourcingEnrichmentAdapter
	// (constructed inside newAssetRegisterService) is the one
	// non-HTTP consumer of the clips orchestrator. It calls
	// handler.EnrichAndIndexClip(ctx, clip, source) — which the
	// Descriptor exposes via its Handler field. threading
	// clipsDescriptor.Handler here keeps the wrapping identical
	// to the pre-Step-5 composition site (the function signature
	// still takes *clipsapi.Handler; the descriptor is just a
	// canonical accessor to the same instance).
	registerSvc := newAssetRegisterService(cfg, log, deps.Core.ClipsRepo, driveUploader, deps.Core.AssetTreeService, providerRegistry, clipsDesc.Handler, dispatcher, deps.Delivery.Publisher)

	// Blocco C1-Step 9 (June 2026): Register capability is now
	// built via the canonical register.Build(deps)
	// (api.Descriptor, error) contract, matching the artlist /
	// youtube / clips / stock / voiceover / soundeffect
	// precedent. The Handler is constructed inside Build and
	// captured by the returned RegisterDescriptor's Module
	// closure. The composition site type-asserts ONCE to
	// *register.RegisterDescriptor (fail-closed) and reuses the
	// concrete for the assetsapi.NewModule(..., Register: rd,
	// ...) call (the concrete *RegisterDescriptor satisfies
	// api.Descriptor structurally). The register capability has
	// no non-HTTP consumer in the codebase
	// (/register-from-youtube + /register-batch are the entire
	// public surface, reachable only via HTTP), so the
	// Descriptor surface is the smallest possible — just
	// `Module` field + forwarder methods (matches the stock /
	// voiceover / soundeffect precedent exactly).
	//
	// Idempotency middleware (PR8): the same shared idemHandler
	// instance is threaded through Build; the descriptor's
	// Module installs it on POST /register-from-youtube. Build
	// nil-tolerates the field (no-op pass-through) for
	// test-fixture paths.
	registerDescriptor, err := assetregister.Build(assetregister.Dependencies{
		Service:     registerSvc,
		Idempotency: idemHandler,
		EnabledFunc: func() bool { return true }, // register is always on in production
		ModuleOpts:  nil,                         // no per-feature middleware (matches pre-Step-9 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, fmt.Errorf("WireAssets: register.Build: %w", err)
	}
	rd, ok := registerDescriptor.(*assetregister.RegisterDescriptor)
	if !ok || rd == nil {
		return nil, fmt.Errorf("WireAssets: register.Build returned unexpected descriptor type %T (want *assetregister.RegisterDescriptor)", registerDescriptor)
	}

	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Storage:     storageDesc,
		Diagnostics: dd,
		Search:      sd,
		Clips:       clipsDesc,
		Voiceover:   vd,
		SoundEffect: soundeffectDesc,
		Register:    rd,
	}, log)
	assetsRouteMod := module.NewRouteModule(
		"assets",
		func() bool { return true },
		"/media",
		assetsMod,
		log,
	)
	log.Info("created unified Assets module (thin transport)")

	// Return AssetsWiring. The canonical SearchAggregator + the
	// pre-built SearchFanOut are exposed here for diagnostic
	// routes, future Health probes, and Wave 22 follow-ups. Both
	// reference the SAME instance wired by BuildCanonicalSearchFanOut
	// in WireRegistry (see the AssetBundle construction below).
	return &AssetsWiring{
		Module:               assetsRouteMod,
		DeletionSvc:          deletionSvc,
		InternalMediaHandler: storageDesc.Handler,
		SearchAggregator:     searchAggregator,
		SearchFanOut:         searchFanOut,
	}, nil
}


