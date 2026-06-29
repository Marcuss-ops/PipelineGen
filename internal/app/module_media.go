package app

import (
	"context"
	"fmt"
	"strings"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	assetsdiag "github.com/Marcuss-ops/PipelineGen/internal/api/assets/diagnostics"
	assetregister "github.com/Marcuss-ops/PipelineGen/internal/api/assets/register"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/api/assets/search"
	assetsfx "github.com/Marcuss-ops/PipelineGen/internal/api/assets/soundeffect"
	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/api/assets/storage"
	assetvoice "github.com/Marcuss-ops/PipelineGen/internal/api/assets/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	appstorage "github.com/Marcuss-ops/PipelineGen/internal/application/assets/storage"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	imgapp "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	voapp "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoverpkg "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	infraassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/assets"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
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
func WireAssets(cfg *config.Config, log *zap.Logger, deps *AssetsModuleDeps, jobs *JobsBundle, voiceoverSvc *voiceoverpkg.Service, voiceoverSync *voiceoversync.Service, realtimeSvc assetsapi.RealtimeMatcher, catalogRepo *catalog.Repository, maintenanceSvc *maintenance.Service, providerRegistry *providers.Registry, dispatcher *outbox.Dispatcher) (*AssetsWiring, error) {
	// PG-034 (June 2026): vectorStore arg removed — Qdrant capability deleted.
	var driveUploader *driveutil.Uploader
	if deps.Delivery.DriveClient != nil {
		driveUploader = &driveutil.Uploader{Service: deps.Delivery.DriveClient, Log: log}
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
		newClipsDriveAdapter(driveUploader),
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
	// adapters satisfy them directly. ArtifactServicePort is nil — the concrete
	// *artifacts.Service is not yet wired in AssetsModuleDeps.Core; the use case
	// current production behaviour (UploadVideoClip returns 500).
	uploadUC := appupload.NewUseCase(appupload.UseCaseDeps{
		Artifact:      nil, // *artifacts.Service not in bundle yet
		DriveUploader: newClipsDriveAdapter(driveUploader),
		Dispatcher:    clipsDispatcherPort,
		Config:        newClipsCfgAdapter(cfg, appjobs.Compose()),
		TreeBuilder:   newClipsAssetTreeAdapter(deps.Core.AssetTreeService),
		JobsSvc:       jobs.Facade,
		ProcessRunner: processRunnerAdapter,
		Log:           log,
	})
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
		DriveUploader:    driveUploader,
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

	storageHandler := assetstorage.NewHandler(jobs.Facade, deps.Core.CatalogSyncService, log)

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
	diagHandler := assetsdiag.NewHandler(diagSvc, log)

	// Search handler: now consumes the canonical Aggregator
	// (constructed above). The legacy cross-provider Service
	// (appsearchsvc.Service) was deleted in PR 10 — see
	// PR-SEARCH-LEGACY-CROSSPROVIDER deprecation record.
	searchHandler := assetsearch.NewHandler(searchAggregator, log)
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
	voiceoverHandler := assetvoice.NewHandler(jobs.Facade, log)

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
	sfxClips := &sfxClipsRepoAdapter{repo: deps.Core.ClipsRepo}
	sfxMeta := &sfxSemanticWriterAdapter{w: metaWriter}
	sfxResolver := &sfxResolverAdapter{r: driveutil.NewResolver("data", "")}
	var sfxDriveUp sfxports.DriveUploaderPort
	if driveUploader != nil {
		sfxDriveUp = &sfxDriveUploaderAdapter{up: driveUploader}
	}
	var sfxDispatcher sfxports.DispatcherPort
	sfxDispatcher = newSfxDispatcherAdapter(dispatcher)
	sfxHandler := assetsfx.NewHandler(sfxClips, sfxDriveUp, sfxMeta, sfxResolver, sfxDispatcher, cfg.Drive.SoundEffectsRootFolder, processRunnerAdapter, log)

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
	// PR8: register receives the same shared idempotency handler as clips.
	registerHandler := assetregister.NewHandler(registerSvc, log, idemHandler)

	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Storage:     storageHandler,
		Diagnostics: diagHandler,
		Search:      searchHandler,
		Clips:       clipsDesc,
		Voiceover:   voiceoverHandler,
		SoundEffect: sfxHandler,
		Register:    registerHandler,
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
		InternalMediaHandler: storageHandler,
		SearchAggregator:     searchAggregator,
		SearchFanOut:         searchFanOut,
	}, nil
}

// storageDriveAdapter adapts drive.Uploader to storage.DrivePort.
type storageDriveAdapter struct {
	up *driveutil.Uploader
}

// Compile-time assertion: storageDriveAdapter satisfies appstorage.DrivePort.
var _ appstorage.DrivePort = (*storageDriveAdapter)(nil)

func (a *storageDriveAdapter) ListFiles(ctx context.Context, folderID string) ([]appstorage.DriveFile, error) {
	files, err := a.up.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}
	out := make([]appstorage.DriveFile, len(files))
	for i, f := range files {
		out[i] = appstorage.DriveFile{ID: f.ID, Name: f.Name, MimeType: f.MimeType}
	}
	return out, nil
}

func (a *storageDriveAdapter) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	return a.up.MoveFile(ctx, fileID, fromFolderID, toFolderID)
}

func (a *storageDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	return a.up.GetOrCreateFolder(ctx, name, parentID)
}

func (a *storageDriveAdapter) RenameFile(ctx context.Context, fileID, newName string) error {
	return a.up.RenameFile(ctx, fileID, newName)
}

// zapLogAdapter adapts *zap.Logger to storage.Logger.
type zapLogAdapter struct{ log *zap.Logger }

func (a *zapLogAdapter) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapLogAdapter) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapLogAdapter) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}
func (a *zapLogAdapter) Debug(msg string, keysAndValues ...any) {
	a.log.Sugar().Debugw(msg, keysAndValues...)
}

// MediaIngestBundle is the capability bundle for the media-ingest module.
//
// PR4d-chunk2 (June 2026): wraps the 11 cross-bundle reads of WireMediaIngest
// into 7 typed fields. PR3 (June 2026): PrebuiltService added so
// BuildDomainBundle can pre-build the service and pass it via the bundle.
// PG-011 (June 2026): DB typed as *storage.SQLiteDB instead of *sql.DB so
// the composition layer never holds raw sqlite handles.
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): Dispatcher
// added so WireMediaIngest can construct the canonical mutations SSOT
// for NewClipStoreAdapter / NewClipsRegistry (the 4 ctor calls in
// buildIngestService clone paths).
type MediaIngestBundle struct {
	DB                *storage.SQLiteDB
	Assets            *asset.Service
	DriveClient       *gdrive.Service
	ImageRepo         *sqassets.ImagesRepository
	VoiceoverRepo     *sqassets.VoiceoversRepository
	ClipsRepo         *sqassets.ClipsRepository
	AssetIndexService *assetindex.Service
	PrebuiltService   *ingest.Service
	Dispatcher        *outbox.Dispatcher
}

// MediaIngestWiring holds the Mediaingest module wiring.
type MediaIngestWiring struct {
	Handler *assetsapi.MediaingestHandler
	Module  module.Module
	Service *ingest.Service
}

// WireMediaIngest creates the Mediaingest handler and module.
//
// PR4d-chunk2 (June 2026): takes *MediaIngestBundle.
// PR3 (June 2026): if PrebuiltService is set, reuses it instead of creating
// a new service (avoids double construction when BuildDomainBundle already
// built the ingest service).
// PR8 (June 2026): added idempotencyMiddleware (reusable Gin idempotency
// middleware instance) — installed by MediaingestHandler on POST /ingest.
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): construct the
// canonical mutations.AssetMutationDispatcher SSOT once here so both the
// clip + stock registries (and their ingest lifecycle stores) route
// media_assets UPSERT through the same outbox+tx writer.
func WireMediaIngest(cfg *config.Config, log *zap.Logger, bundle *MediaIngestBundle, idempotencyMiddleware gin.HandlerFunc) (*MediaIngestWiring, error) {
	if bundle == nil || bundle.DriveClient == nil {
		return nil, nil
	}
	if bundle.ImageRepo == nil || bundle.VoiceoverRepo == nil || bundle.ClipsRepo == nil || bundle.AssetIndexService == nil {
		return nil, nil
	}
	mutationsDisp, err := newMutationsDispatcherAdapter(bundle.Dispatcher)
	if err != nil {
		return nil, fmt.Errorf("WireMediaIngest: %w", err)
	}
	svc := bundle.PrebuiltService
	if svc == nil {
		imagesRegistry := imgapp.NewRegistryAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath(), log)
		imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewImageStoreAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath())}, log)
		voiceoverRegistry := voapp.NewVoiceoverRegistryAdapter(bundle.VoiceoverRepo)
		voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(bundle.VoiceoverRepo)}, log)
		clipRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)
		clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)}, log)
		stockRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)
		stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)}, log)
		svc = ingest.NewService(cfg, log, bundle.DriveClient, map[ingest.Kind]*ingest.Pipeline{
			ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
			ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
			ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
			ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
		})
	}
	handler := assetsapi.NewMediaingestHandler(svc, idempotencyMiddleware)
	mod := module.NewRouteModule(
		"media-ingest",
		func() bool { return handler != nil },
		"/media",
		handler,
		log,
	)
	return &MediaIngestWiring{Handler: handler, Module: mod, Service: svc}, nil
}

func isAIImageIngestSource(req *ingest.Request) bool {
	if req == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.Source)) {
	case "google-vids", "google-vids-image", "google-slides", "google-flow", "nvidia", "nvidia-local", "local-nim", "flux-1-dev", "flux-1-schnell", "flux.1-schnell", "flux1-schnell", "flux-2-klein", "flux.2-klein-4b", "flux-2-klein-4b":
		return true
	default:
		return false
	}
}

// JobsBundle is the Job module's *owned* runtime surface.
//
// Phase-B ownership inversion (June 2026): these objects are constructed
// once by BuildJobsBundle, returned as a typed bundle, and consumed by
// composeIntegration for cross-module handler registration.
type JobsBundle struct {
	Repo       *sqljobs.SQLiteStore
	Dispatcher *appjobs.Dispatcher
	Service    *appjobs.Service
	Facade     job.Service // canonical domain interface satisfied by *appjobs.Service
}

// BuildJobsBundle constructs the Job runtime pieces in the canonical order:
//
//  1. SQLite-backed job Store.
//  2. In-process Dispatcher (handler registry; kept nil-free until Freeze).
//  3. The application Service that orchestrates enqueue / list / cancel /
//     status propagation.
//  4. The domain job.Service interface satisfied by *appjobs.Service.
//
// Returns a JobsBundle. The bundle is fully constructed but its Dispatcher
// is NOT frozen yet — freezing is performed by WireServices in bootstrap.go
// *after* WireRegistry, so that no new module can register a handler while
// workers are claiming jobs.
//
// Returning `(nil, error)` is reserved for unrecoverable construction errors
// (nil db / nil logger). All four fields are required to be non-nil on success.
func BuildJobsBundle(db *storage.SQLiteDB, log *zap.Logger) (*JobsBundle, error) {
	if db == nil {
		return nil, fmt.Errorf("build jobs bundle: db is nil")
	}
	if log == nil {
		return nil, fmt.Errorf("build jobs bundle: log is nil")
	}

	repo := sqljobs.NewSQLiteStore(db.DB, log)
	dispatcher := appjobs.NewDispatcher()
	// Issue 4 (June 2026, P1): attach the canonical job-type Registry
	// so Enqueue() routes the MaxRetries fallback through the registry
	// for REGISTERED job types (script.generate -> DefaultMaxRetries=2)
	// and keeps the legacy hard-coded 3-retry safety net only for
	// UNREGISTERED types. Mirrors the HC-1 Runner.WithRegistry(reg) and
	// Worker.WithRegistry(reg) plumbing that landed in Issues 2 + the
	// existing HC-1 typed-port chain (TimeoutResolver for worker timeouts).
	svc := appjobs.NewService(repo, dispatcher, log).WithRegistry(appjobs.Compose())

	// *appjobs.Service satisfies the domain job.Service interface directly.
	// No facade needed — consumers declare their dependency as job.Service
	// (interface value) and the composition root injects this concrete pointer.
	return &JobsBundle{
		Repo:       repo,
		Dispatcher: dispatcher,
		Service:    svc,
		Facade:     svc,
	}, nil
}
