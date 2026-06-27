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
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	assetclipssearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/clipssearch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	appsearchsvc "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	appstorage "github.com/Marcuss-ops/PipelineGen/internal/application/assets/storage"
	imgapp "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	voapp "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoverpkg "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	infraassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/gin-gonic/gin"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
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
var dbHealthCheckerAdapter = infraassets.NewDBHealthCheckerAdapter(nil) // AssetsBundle is the capability bundle for the unified Assets module.
// PR4d-chunk2 (June 2026): wraps 10 cross-bundle reads of WireAssets.
// ClipIndexerService moved INTO the bundle (was a direct arg in earlier
// draft); RealtimeService moved OUT (single-use, fits clean as a 10th
// direct arg). AssetTreeService + AssetIndexService stay inside since they
// have multiple uses (deletion svc + handler ctor).
//
// PR8 (June 2026): gain IdempotencyStore (typed port, principally for
// the bundle's compile-time port assertion) and IdempotencyStoreHandler
// (the gin.HandlerFunc constructed once at WireRegistry and threaded
// through here). The cleanup goroutine is owned by
// ComposeRoot.IdempotencyMiddleware (single instance per app), NOT
// constructed inside WireAssets — keeps the registry-level lifecycle
// simple and avoids double-ticker leaks.
//
// Field budget: 12 fields (PR8 adds two).
type AssetsBundle struct {
	ClipsRepo               *assets.ClipsRepository
	VoiceoverRepo           *assets.VoiceoversRepository
	ImageRepo               *assets.ImagesRepository
	Assets                  *asset.Service
	DriveClient             *gdrive.Service
	AssetTreeService        *assettree.Service
	AssetIndexService       *assetindex.Service
	MediaProcessor          asset.Processor
	CatalogSyncService      *catalogsync.Service
	ClipIndexerService      *clipindexer.Service
	IdempotencyStore        middleware.IdempotencyStore
	IdempotencyStoreHandler gin.HandlerFunc
	// Wave 21 PR 9 (June 2026): composition inputs for the
	// canonical SearchAggregator. MediasearchService + WorkspaceID
	// activate the semantic backend (QDRANT-004 tenant-isolation
	// gate requires a non-default workspace); empty disables it.
	// SearchBackendRegistry is the frozen cross-capability backend
	// catalog populated by WireAssets and exposed here for
	// diagnostic routes + future Health probes.
	//
	// Wave 19 cross-capability rule: module_media.go is itself a
	// composition-only bridge; the heavy cross-cap import dance
	// (search ↔ providers ↔ mediasearch ↔ assets.ClipsRepository)
	// is OWNED by internal/app/search_backends.go and surfaces
	// here only as a typed slot.
	MediasearchService    *mediasearch.Service
	SearchWorkspaceID     string
	SearchBackendRegistry *search.BackendRegistry
}

// AssetsWiring holds the Assets module wiring.
type AssetsWiring struct {
	Module               module.Module
	DeletionSvc          *deletion.DeletionService
	InternalMediaHandler *assetstorage.Handler
	// Wave 21 PR 9 (June 2026): the canonical SearchAggregator.
	// BACKFILL keeps legacy clipssearch.Service wired alongside;
	// PR 10 cutover swaps clip_search.go::AdvancedSearch to
	// consume this Aggregator.
	SearchAggregator *search.Aggregator
}

// WireAssets creates the unified Assets handler and module.
//
// PR4d-chunk2 (June 2026): takes *AssetsBundle + 8 narrow direct args
// (VectorStore, JobsBundle, voiceoverSvc, voiceoverSync, realtimeSvc,
// catalogRepo, maintenanceSvc). ClipIndexer is in the bundle now.
// PR3 (June 2026): providerRegistry added for constructor injection
// (replaces post-construction SetProviderRegistry).
// Cascade fix (June 2026, cmd/admin recovery collateral): the
// `realtime` package was removed in commit d61068b3. The WireAssets
// signature below accepts a realtimeSvc parameter typed as `interface{}`
// (was `*realtime.Service`) so the caller in registry.go can pass
// `root.Domains.RealtimeService` (also interface{} post-fix) without any
// type assertions. The diagnostic / search adapters downstream of this
// function already use `interface{}` for the realtimeSvc field type
// (see internal/app/assets_adapters.go: diagIndexHealthAdapter.realtime
// + searchVectorAdapter.realtimeSvc), so this change re-aligns the
// caller signature with the adapter field types.
// WireAssets creates the unified Assets handler and module.
//
// PR4d-chunk2 (June 2026): takes *AssetsBundle + 8 narrow direct args
// (VectorStore, JobsBundle, voiceoverSvc, voiceoverSync, realtimeSvc,
// catalogRepo, maintenanceSvc). ClipIndexer is in the bundle now.
// PR3 (June 2026): providerRegistry added for constructor injection
// (replaces post-construction SetProviderRegistry).
// Wave 16 (June 2026): realtimeSvc typed to `assetsapi.RealtimeMatcher`
// per AGENTS.md Pattern 0 (typed-port abstraction). The realtime
// package was removed in commit d61068b3; the typed parameter stays
// nil-safe (typed-nil of an interface equals nil interface).
func WireAssets(cfg *config.Config, log *zap.Logger, bundle *AssetsBundle, jobs *JobsBundle, voiceoverSvc *voiceoverpkg.Service, voiceoverSync *voiceoversync.Service, realtimeSvc assetsapi.RealtimeMatcher, catalogRepo *catalog.Repository, maintenanceSvc *maintenance.Service, providerRegistry *providers.Registry, dispatcher *outbox.Dispatcher) (*AssetsWiring, error) {
	// PG-034 (June 2026): vectorStore arg removed — Qdrant capability deleted.
	var driveUploader *driveutil.Uploader
	if bundle.DriveClient != nil {
		driveUploader = &driveutil.Uploader{Service: bundle.DriveClient, Log: log}
	}
	var assetRepo asset.Repository
	if bundle.Assets != nil {
		assetRepo = bundle.Assets.Repository()
	}
	folderMemSvc := foldermemory.NewService(log, bundle.ClipsRepo)
	metaWriter := semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
	deletionSvc := deletion.NewDeletionService(bundle.ClipsRepo, bundle.ClipsRepo, bundle.ClipsRepo, bundle.VoiceoverRepo, bundle.ImageRepo, driveUploader, bundle.AssetTreeService, bundle.AssetIndexService, dispatcher, log)

	// PR8 (June 2026): idemHandler is passed in from WireRegistry (see
	// registry.go). WireAssets does NOT construct its own Idempotency
	// instance — the cleanup goroutine lives once at the registry level
	// and is owned by ComposeRoot.IdempotencyMiddleware (Stop() on
	// shutdown). A nil idemHandler (e.g. nil-store test fixture) is
	// tolerated; the handler wrappers fall through to pass-through.
	var idemHandler gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if bundle.IdempotencyStore != nil {
		idemHandler = bundle.IdempotencyStoreHandler
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
	// W14 PR2 slice 3 (June 2026): construct the BulkUploadWorker
	// from typed ports so the handler never imports infra for the
	// bulk-upload job path. The concrete adapters already exist in
	// clips_adapters_index.go.
	bulkUploadWorker := appclips.NewBulkUploadWorker(
		newClipsDriveAdapter(driveUploader),
		newClipsRepoAdapter(bundle.ClipsRepo),
		newClipsIndexerAdapter(bundle.ClipIndexerService),
		newClipsHashAdapter(),
		newClipsCfgAdapter(cfg),
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
		bundle.ClipsRepo, bundle.ClipsRepo, bundle.ClipsRepo,
		bundle.VoiceoverRepo, bundle.ImageRepo,
		driveUploader, metaWriter, bundle.ClipIndexerService,
		folderMemSvc, bundle.AssetTreeService,
		nil, // vectorSvc removed PG-034
	)
	clipOpsSvc := appclips.NewClipOpsService(
		clipsOpsPorts.SourceResolver,
		clipsOpsPorts.VoiceoverRepo,
		clipsOpsPorts.ImagesRepo,
		clipsOpsPorts.DriveUploader,
		newClipsCleanupPortAdapter(deletionSvc),
		newClipsJobsPortAdapter(jobs.Facade),
		log,
	)
	clipsHandler := clipsapi.NewHandler(clipsapi.Deps{
		SourceResolver: artifacts.NewSourceResolver(bundle.ClipsRepo, bundle.ClipsRepo, bundle.ClipsRepo),
		AssetRepo:      assetRepo,
		ClipsRepo:      bundle.ClipsRepo,
		StockRepo:      bundle.ClipsRepo,
		ArtlistRepo:    bundle.ClipsRepo,
		DeletionSvc:    deletionSvc,
		DriveUploader:  driveUploader,
		MediaProcessor: bundle.MediaProcessor,
		AssetTreeSvc:   bundle.AssetTreeService,
		MetaWriter:     metaWriter,
		ClipIndexer:    bundle.ClipIndexerService,
		JobsSvc:        jobs.Facade,
		Cfg:            cfg,
		Log:            log,
		VoiceoverRepo:  bundle.VoiceoverRepo,
		ImagesRepo:     bundle.ImageRepo,
		FolderMemSvc:   folderMemSvc,
		SearchSvc: assetclipssearch.NewService(log, map[string]assetclipssearch.AdvancedSearchRepo{
			"youtube": bundle.ClipsRepo,
			"artlist": bundle.ClipsRepo,
			"stock":   bundle.ClipsRepo,
		}),
		ProcessRunner:   processRunnerAdapter,
		Dispatcher:      clipsDispatcherPort,
		BulkUploadWorker: bulkUploadWorker,
		ClipOpsService:   clipOpsSvc,
	}, idemHandler)
	var drivePort appstorage.DrivePort
	if driveUploader != nil {
		drivePort = &storageDriveAdapter{up: driveUploader}
	}
	storageSvc := appstorage.NewService(drivePort, &zapLogAdapter{log})
	storageHandler := assetstorage.NewHandler(storageSvc, jobs.Facade, bundle.CatalogSyncService, log)

	// ── PR 3 (June 2026): diagnostics + search wired with real ports ─
	// Diagnostics: IndexHealth via realtime.Service + AssetStats via ClipsRepository.
	// QDRANT-005 Fase 1 (June 2026): diagIndexHealthAdapter rewired with
	// real SQLite + Qdrant deps. Nil-tolerant — when bundle.ClipsRepo is
	// nil the handler falls back to 503.
	var diagSvc *appdiag.Service
	if bundle.ClipsRepo != nil {
		diagSvc = appdiag.NewService(
			&diagIndexHealthAdapter{clips: bundle.ClipsRepo, qdrant: nil, collectionName: ""},
			&diagAssetStatsAdapter{clips: bundle.ClipsRepo},
			&zapDiagLogAdapter{log: log},
		)
	}
	diagHandler := assetsdiag.NewHandler(diagSvc, log)

	// Search service: cross-provider search only (semantic consolidated into mediasearch).
	var searchSvc *appsearchsvc.Service
	if providerRegistry != nil {
		searchSvc = appsearchsvc.NewService(
			&searchRegistryAdapter{registry: providerRegistry},
			&searchCatalogAdapter{catalog: catalogRepo},
			&searchClipAdapter{catalog: catalogRepo},
			&searchConfigAdapter{cfg: cfg},
			&zapSearchLogAdapter{log: log},
		)
	}
	searchHandler := assetsearch.NewHandler(searchSvc, log)
	if diagSvc != nil && searchSvc != nil {
		log.Info("diagnostics and search services wired with production ports")
	} else {
		log.Warn("diagnostics and/or search services NOT fully wired — some routes will return 503")
	}

	// ── PR 4 (June 2026): extract voiceover, soundeffect, register ─
	// Voiceover: GroupsResolver is built here for the standalone handler.
	var groupsResolver *voiceoverpkg.GroupsResolver
	if bundle.AssetTreeService != nil {
		gr, grErr := voiceoverpkg.NewGroupsResolver(bundle.AssetTreeService, log)
		if grErr != nil {
			log.Warn("voiceover groups_resolver not initialized (PR4)", zap.Error(grErr))
		} else {
			groupsResolver = gr
		}
	}
	defaultVoiceoverRoot := cfg.Drive.VoiceoverRootFolder
	if defaultVoiceoverRoot != "" {
		log.Info("voiceover groups_resolver enabled (PR4)", zap.String("root", defaultVoiceoverRoot))
	}
	voiceoverHandler := assetvoice.NewHandler(voiceoverSvc, voiceoverSync, jobs.Facade, groupsResolver, defaultVoiceoverRoot, log)

	// SoundEffect: wrapped repos + uploader + metaWriter via sfxports
	// adapters. PG-003 (June 2026) replaces the four concrete
	// infrastructure reach-throughs with structural ports so the api/
	// layer stays thin (per AGENTS.md Pattern 0).
	sfxClips := &sfxClipsRepoAdapter{repo: bundle.ClipsRepo}
	sfxMeta := &sfxSemanticWriterAdapter{w: metaWriter}
	sfxResolver := &sfxResolverAdapter{r: driveutil.NewResolver("data", "")}
	var sfxDriveUp sfxports.DriveUploaderPort
	if driveUploader != nil {
		sfxDriveUp = &sfxDriveUploaderAdapter{up: driveUploader}
	}
	sfxHandler := assetsfx.NewHandler(sfxClips, sfxDriveUp, sfxMeta, sfxResolver, cfg.Drive.SoundEffectsRootFolder, processRunnerAdapter, log)

	// Register: the HTTP layer now depends on a single sourcing use case.
	registerSvc := newAssetRegisterService(cfg, log, bundle.ClipsRepo, driveUploader, bundle.AssetTreeService, providerRegistry, clipsHandler, dispatcher)
	// PR8: register receives the same shared idempotency handler as clips.
	registerHandler := assetregister.NewHandler(registerSvc, log, idemHandler)

	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Storage:     storageHandler,
		Diagnostics: diagHandler,
		Search:      searchHandler,
		Clips:       clipsHandler,
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

	// ── Wave 21 PR 9 (June 2026): canonical SearchAggregator
	//    ALONGSIDE legacy clipssearch.Service for BACKFILL
	//    dual-path. Compose roots:
	//     1. Bridge adapters (providerBackend, localBackend,
	//        semanticBackend) — registered via the composition-
	//        only BuildSearchBackends helper at
	//        internal/app/search_backends.go.
	//     2. Frozen BackendRegistry → NewAggregator → return to
	//        caller via AssetsWiring.SearchAggregator.
	//    Until PR 10 cutover, the Aggregator is exposed for
	//    diagnostics + integration tests; clip_search.go::AdvancedSearch
	//    still consumes assetclipssearch.Service for safety.
	searchBackends := BuildSearchBackends(SearchBackendBuildOpts{
		Logger:         log,
		ProviderReg:    providerRegistry,
		ClipsRepo:      bundle.ClipsRepo,
		MediasearchSvc: bundle.MediasearchService,
		WorkspaceID:    bundle.SearchWorkspaceID,
	})
	bundle.SearchBackendRegistry = searchBackends
	searchAggregator := search.NewAggregator(searchBackends, &zapSearchLogAdapter{log: log})
	log.Info("search aggregator wired (PR 9 BACKFILL dual-path)",
		zap.Int("backends", len(searchBackends.All())))

	return &AssetsWiring{
		Module:               assetsRouteMod,
		DeletionSvc:          deletionSvc,
		InternalMediaHandler: storageHandler,
		SearchAggregator:     searchAggregator,
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
type MediaIngestBundle struct {
	DB                *storage.SQLiteDB
	Assets            *asset.Service
	DriveClient       *gdrive.Service
	ImageRepo         *sqassets.ImagesRepository
	VoiceoverRepo     *sqassets.VoiceoversRepository
	ClipsRepo         *sqassets.ClipsRepository
	AssetIndexService *assetindex.Service
	PrebuiltService   *ingest.Service
}

// MediaIngestWiring holds the MediaIngest module wiring.
type MediaIngestWiring struct {
	Handler *assetsapi.MediaingestHandler
	Module  module.Module
	Service *ingest.Service
}

// WireMediaIngest creates the MediaIngest handler and module.
//
// PR4d-chunk2 (June 2026): takes *MediaIngestBundle.
// PR3 (June 2026): if PrebuiltService is set, reuses it instead of creating
// a new service (avoids double construction when BuildDomainBundle already
// built the ingest service).
// PR8 (June 2026): added idempotencyMiddleware (reusable Gin idempotency
// middleware instance) — installed by MediaingestHandler on POST /ingest.
func WireMediaIngest(cfg *config.Config, log *zap.Logger, bundle *MediaIngestBundle, idempotencyMiddleware gin.HandlerFunc) (*MediaIngestWiring, error) {
	if bundle == nil || bundle.DriveClient == nil {
		return nil, nil
	}
	if bundle.ImageRepo == nil || bundle.VoiceoverRepo == nil || bundle.ClipsRepo == nil || bundle.AssetIndexService == nil {
		return nil, nil
	}
	svc := bundle.PrebuiltService
	if svc == nil {
		imagesRegistry := imgapp.NewRegistryAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath(), log)
		imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewImageStoreAdapter(bundle.ImageRepo, cfg.Storage.ImagesPath())}, log)
		voiceoverRegistry := voapp.NewVoiceoverRegistryAdapter(bundle.VoiceoverRepo)
		voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(bundle.VoiceoverRepo)}, log)
		clipRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())
		clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())}, log)
		stockRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())
		stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService, Store: ingest.NewClipStoreAdapter(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())}, log)
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
	svc := appjobs.NewService(repo, dispatcher, log)

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
