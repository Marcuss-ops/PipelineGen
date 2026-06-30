package app

import (
	"context"
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	artsources "github.com/Marcuss-ops/PipelineGen/internal/api/assets/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/stock"
	ytsources "github.com/Marcuss-ops/PipelineGen/internal/api/assets/youtube"
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	mutations "github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	fullimagessvc "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ytService "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	svcjobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/clipcatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ArtlistWiring holds the Artlist module wiring.
//
// PR4d-chunk2 (June 2026): Resolver field removed. clipresolver.Service
// does not implement script.AutoHarvestService (no EnqueueHarvest method),
// so the harvest service is constructed locally in WireRegistry from
// root.Jobs.Facade (the same path used pre-PR4d). WireArtlist remains the
// canonical owner of the clipresolver construction; ArtlistWiring no longer
// needs to expose it.
//
// Blocco C1-Step 3 (June 2026): Handler field removed. The HTTP Handler
// is constructed inside `artsources.Build(deps)` and captured by the
// returned ArtlistDescriptor's Module closure. No caller (composition
// root, tests, internal services) needs to read the raw `*ArtlistHandler`
// outside the package — matches the channels precedent of dropping the
// explicit Handler field in favor of descriptor-only wiring.
type ArtlistWiring struct {
	Module  api.Module
	Service *artlistPkg.Service
}

// WireArtlist creates the Artlist service, handler, and module.
//
// PR4d-chunk2 (June 2026): accepts *ArtlistBundle (10 cross-bundle deps)
// + vectorStore (1 of 2 cross-bundle deps that didn't fit) +
// dispatcher (PR2.5: was SetDispatcher setter, now constructor arg so
// the canonical UpsertClip + IndexClip path stays wired in production).
// Returns ArtlistWiring with Resolver populated so caller can use the
// clipresolver for ScriptFlow late-binding without round-tripping.
func WireArtlist(ctx context.Context, cfg *config.Config, log *zap.Logger, bundle *ArtlistBundle, dispatcher *outbox.Dispatcher, publisher delivery.Publisher) (*ArtlistWiring, error) {
	// QDRANT-002 PR7: dispatcher is now an unconditional requirement.
	// The legacy "UpsertClip + IndexClip fallback when dispatcher is
	// nil" was wrong-by-design: a nil dispatcher at runtime means the
	// canonical ingest atomically lost any half-state between the two
	// ops (PR1 retain window). Treat a nil dispatcher at composition
	// time as a code defect — explicit error beats silent fallback
	// that surfaces only at first ingest.
	if dispatcher == nil {
		return nil, fmt.Errorf("WireArtlist: dispatcher is required at composition time — QDRANT-002 PR7 removed the legacy UpsertClip+IndexClip fallback; production must wire root.Outbox.Dispatcher")
	}
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): construct
	// the canonical mutations.AssetMutationDispatcher SSOT once here so
	// both wireArtlistLifecycle (below) and the SemanticEnricher
	// (further down) route media_assets UPSERT through the same
	// outbox+tx writer. The var is declared BEFORE its first use at the
	// wireArtlistLifecycle call (Go's declaration-before-use rule).
	mutationsDisp, err := newMutationsDispatcherAdapter(dispatcher)
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: %w", err)
	}
	// vectorStore arg removed from this service constructor.
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): mutationsDisp
	// threaded into wireArtlistLifecycle so the Artlist lifecycle's
	// embedded artifacts.NewClipsRegistry routes media_assets UPSERT
	// through the canonical outbox+tx writer.
	artlistLifecycle := wireArtlistLifecycle(bundle, mutationsDisp, log)
	clipCatalogRepo, clipIndexerSvc := wireArtlistCatalog(ctx, cfg, bundle, log)
	assetDestResolver := wireAssetDestinationResolver(cfg, bundle, log)
	presetsConfig, _ := artlistPkg.LoadPresets("config/artlist_presets.yaml")
	if presetsConfig == nil {
		log.Warn("failed to load artlist presets, using defaults")
	}

	// PR2.7: build the DriveFolderManager adapter BEFORE the
	// SemanticEnricher so the enricher can receive the canonical
	// port instead of the legacy *drive.Uploader concrete. The
	// adapter wraps *bundle.DriveClient (the raw *driveapi.Service)
	// so callers (semantic_enricher as well as anyone reading
	// Service.driveFolderManager) never see SDK types. When
	// bundle.DriveClient is nil (e.g. test fixtures), the adapter
	// stays nil and the enricher's updateCumulativeMetadataJSON is
	// a no-op (dropDriveManager nil-tolerance path).
	var driveManager artlistPkg.DriveFolderManager
	if bundle.DriveClient != nil {
		driveManager = drive.NewDriveFolderManagerAdapter(bundle.DriveClient, log)
	}

	// PR2.5: build the SemanticEnricher BEFORE NewService so its
	// Dispatcher constructor argument captures the canonical
	// outbox.Dispatcher at composition time. No setter is called
	// afterwards — the enricher is passed via ServiceDeps.MetadataWriter.
	// PR2.7: the enricher now takes the DriveFolderManager port
	// (driveManager) instead of the narrow *drive.Uploader concrete.
	// PR2.5: build the SemanticEnricher BEFORE NewService so its
	// Dispatcher constructor argument captures the canonical
	// outbox.Dispatcher at composition time. No setter is called
	// afterwards — the enricher is passed via ServiceDeps.MetadataWriter.
	// PR2.7: the enricher now takes the DriveFolderManager port
	// (driveManager) instead of the narrow *drive.Uploader concrete.
	// Dispatcher is the canonical media_index_outbox dispatcher from
	// root.Outbox (already built by BuildOutboxBundle before WireRegistry
	// runs).
	//
	// QDRANT-002 PR7: dispatcher is now an unconditional requirement.
	// The legacy "UpsertClip + IndexClip fallback when dispatcher is
	// nil" was wrong-by-design: a nil dispatcher at runtime means the
	// canonical ingest atomically lost any half-state between the two
	// ops (PR1 retain window). Treat a nil dispatcher at composition
	// time as a code defect — explicit error beats silent fallback
	// that surfaces only at first ingest.
	//
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): mutationsDisp
	// constructed at top of WireArtlist (declared before its first use);
	// this block retains the dispatcher's role for the SemanticEnricher.
	var enricher artlistPkg.MetadataWriter
	if bundle.ClipsRepo != nil {
		metaWriter := semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
		enricher = artlistPkg.NewSemanticEnricher(bundle.ClipsRepo, clipIndexerSvc, metaWriter, driveManager, dispatcher, log)
		log.Info("wired semantic enricher (MetadataWriter port) with canonical outbox.Dispatcher — production canonical path active (QDRANT-002 PR7)")
	}

	artlistSvc, err := wireArtlistService(cfg, bundle, artlistLifecycle, assetDestResolver, clipIndexerSvc, enricher, driveManager, dispatcher, publisher, log)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		return nil, err
	}
	clipResolver := wireClipResolver(cfg, bundle, clipCatalogRepo, presetsConfig, log)
	descriptor, err := wireArtlistModule(cfg, artlistSvc, bundle, clipResolver, log)
	if err != nil {
		log.Warn("Failed to build Artlist module", zap.Error(err))
		return nil, err
	}
	ad, typeAssertOk := descriptor.(*artsources.ArtlistDescriptor)
	if !typeAssertOk || ad == nil {
		return nil, fmt.Errorf("WireArtlist: artsources.Build returned unexpected descriptor type %T (want *artsources.ArtlistDescriptor)", descriptor)
	}
	log.Info("created Artlist module via Build contract (Blocco C1-Step 3)")
	return &ArtlistWiring{Module: ad.Module, Service: artlistSvc}, nil
}

// wireArtlistModule composes the Artlist HTTP module by delegating to
// the canonical `artsources.Build(deps Dependencies) (api.Descriptor, error)`
// entrypoint (Blocco C1-Step 3, June 2026). The composition root has
// the only knowledge of `cfg.Features.ArtlistEnabled` and the
// FeatureFlagChecker middleware; this function maps those onto the
// typed narrow Dependencies.
//
// nil-tolerant: when artlistSvc is nil, returns nil + nil error so
// upstream WireArtlist's tolerant skip path stays intact (the bundle
// can be wired with optional deps missing and the capability does not
// inline-mount its routes).
func wireArtlistModule(cfg *config.Config, artlistSvc *artlistPkg.Service, bundle *ArtlistBundle, clipResolver interface{}, log *zap.Logger) (api.Descriptor, error) {
	if artlistSvc == nil {
		return nil, nil // tolerated: module is skipped
	}
	// The clipresolver package was removed from remote (commit
	// d61068b3). wireClipResolver returns nil typed as interface{};
	// the safe type assertion forwards a typed-nil into Build, and
	// the resulting ArtlistHandler stays nil-tolerant and short-
	// circuits the /recommend route at request time.
	var resolver artsources.ClipResolverPort
	if val, ok := clipResolver.(artsources.ClipResolverPort); ok {
		resolver = val
	}
	// Wrap `*config.Config` in the typed `ArtlistConfigPort` (defined in
	// internal/application/assets/providers/artlist/ports.go) so the api
	// handler stays free of infrastructure-layer imports.
	// newArtlistConfigAdapter(nil) returns a nil interface, preserving
	// the handler's `if h.cfg != nil` discipline if any caller adds a
	// short-circuit path.
	cfgPort := newArtlistConfigAdapter(cfg)
	return artsources.Build(artsources.Dependencies{
		Service:        artlistSvc,
		CatalogSync:    bundle.CatalogSyncService,
		Jobs:           bundle.Jobs.Facade,
		ClipResolver:   resolver,
		NodeScraperDir: "node-scraper",
		CfgPort:        cfgPort,
		EnabledFunc:    func() bool { return cfg.Features.ArtlistEnabled },
		ModuleOpts: []api.RouteModuleOption{
			api.WithMiddleware(middleware.FeatureFlagChecker("Artlist", cfg.Features.ArtlistEnabled)),
		},
		Logger: log,
	})
}

// wireArtlistLifecycle builds the Artlist capability's lifecycle
// service with the canonical mutations SSOT.
//
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): mutationsDisp
// is the 2nd positional arg so artifacts.NewClipsRegistry's media_assets
// UPSERT routes through the dispatcher (QDRANT-002 atomicity invariant).
func wireArtlistLifecycle(bundle *ArtlistBundle, mutationsDisp mutations.AssetMutationDispatcher, log *zap.Logger) *lifecycle.Service {
	clipsRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository(), mutationsDisp)
	return NewLifecycleFromDeps(&LifecycleDeps{Registry: clipsRegistry, DriveUploader: bundle.DriveUploader, AssetIndex: bundle.AssetIndexService}, log)
}

func wireAssetDestinationResolver(cfg *config.Config, bundle *ArtlistBundle, log *zap.Logger) asset.Resolver {
	if bundle.DriveUploader != nil {
		storageResolver := drive.NewResolver(drive.MediaRoot(cfg.Storage.MediaPath()), drive.DriveRoot(cfg.Drive.RootFolder()))
		mediaStore := drive.NewStore(storageResolver, bundle.DriveUploader, cfg.Drive.RootFolder(), "", "", cfg.Drive.SoundEffectsFolder(), log)
		return drive.NewDestinationResolver(mediaStore)
	}
	return nil
}

func wireClipResolver(cfg *config.Config, bundle *ArtlistBundle, clipCatalogRepo *clipcatalog.Repository, presetsConfig *artlistPkg.PresetsConfig, log *zap.Logger) interface{} {
	_ = cfg
	_ = bundle
	_ = clipCatalogRepo
	_ = presetsConfig
	_ = log
	return nil // clipresolver package removed from remote
}

// wireArtlistService composes the artlist service via ServiceDeps (PR2.5+PR2.7).
// All cross-cutting dependencies are injected through the deps struct —
// no setters, no late-binding. The SemanticEnricher is built above (in
// WireArtlist) so its Dispatcher hookup is the composition root's only
// source of truth. clipIndexerSvc satisfies the Indexer port directly
// (IndexClip + IsEnabled match). dispatcher is the canonical
// outbox.Dispatcher from root.Outbox (passed through from WireArtlist).
// driveManager (PR2.7) is the DriveFolderManager port — the adapter
// wrapping bundle.DriveClient was constructed in WireArtlist above and
// is threaded into both ServiceDeps.ServicePorts.DriveFolderManager and
// the SemanticEnricher constructor.
func wireArtlistService(
	cfg *config.Config,
	bundle *ArtlistBundle,
	artlistLifecycle *lifecycle.Service,
	assetDestResolver asset.Resolver,
	clipIndexerSvc *clipindexer.Service,
	enricher artlistPkg.MetadataWriter,
	driveManager artlistPkg.DriveFolderManager,
	dispatcher *outbox.Dispatcher,
	publisher delivery.Publisher,
	log *zap.Logger,
) (*artlistPkg.Service, error) {
	// PR2.6: wireArtlistService uses the named-sub-structs shape for
	// ServiceDeps (ServicePorts + ServiceDependencies). Production
	// wiring receives root.Outbox.Dispatcher which feeds both Service
	// (via ServiceDependencies.Dispatcher) and the SemanticEnricher
	// (via the upstream NewSemanticEnricher(... dispatcher ...)).
	// PR2.7: DriveFolderManager joins ServicePorts (was 3 → 4 fields);
	// DriveClient is dropped from ServiceDependencies (was 12 → 11 fields).
	artlistSvc, err := artlistPkg.NewService(artlistPkg.ServiceDeps{
		ServicePorts: artlistPkg.ServicePorts{
			AssetStore:         bundle.ClipsRepo, // *assets.ClipsRepository implements AssetStore
			Indexer:            clipIndexerSvc,   // *clipindexer.Service implements Indexer
			MetadataWriter:     enricher,
			DriveFolderManager: driveManager, // *drive.DriveFolderManagerAdapter wraps bundle.DriveClient
			Publisher:          publisher,     // canonical Drive publisher (FASE 8)
		},
		ServiceDependencies: artlistPkg.ServiceDependencies{
			Cfg:        cfg,
			MainDB:     bundle.DB.DB, // ArtlistDB removed PR2.6: == MainDB post-consolidation
			Log:        log,
			Dispatcher: dispatcher,
			// DriveClient removed PR2.7: replaced by DriveFolderManager port in ServicePorts
			MediaProcessor:    bundle.MediaProcessor,
			LifecycleService:  artlistLifecycle,
			AssetDestResolver: assetDestResolver,
			JobsSvc:           bundle.Jobs.Facade,
			AssetProcRepo:     bundle.Assets.ProcessingRepository(),
			AssetVerRepo:      bundle.Assets.VersionRepository(),
			AssetLocRepo:      bundle.Assets.LocationRepository(),
		},
	})
	if err != nil {
		return nil, err
	}
	if artlistSvc != nil && bundle.Jobs.Service != nil {
		bundle.Jobs.Service.RegisterHandler(svcjobs.TypeArtlistRun, artlistSvc.HandleJob)
		bundle.Jobs.Service.RegisterHandler("artlist.run", artlistSvc.HandleJob)
		log.Info("registered artlist job handlers")
	}
	return artlistSvc, nil
}

func wireArtlistCatalog(ctx context.Context, cfg *config.Config, bundle *ArtlistBundle, log *zap.Logger) (*clipcatalog.Repository, *clipindexer.Service) {
	if bundle.ClipIndexerService != nil {
		return clipcatalog.NewRepository(bundle.DB.DB, log), bundle.ClipIndexerService
	}
	if bundle.DB != nil && bundle.DB.DB != nil {
		if err := clipcatalog.EnsureSchema(ctx, bundle.DB.DB, log); err != nil {
			log.Warn("failed to ensure clipcatalog schema", zap.Error(err))
		}
	}
	clipCatalogRepo := clipcatalog.NewRepository(bundle.DB.DB, log)
	clipIndexerSvc := clipindexer.NewService(&clipindexer.Config{Enabled: cfg.ClipIndexer.Enabled, ServerURL: cfg.ClipIndexer.ServerURL, ScriptPath: cfg.ClipIndexer.ScriptPath, PythonBin: cfg.ClipIndexer.PythonBin, AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist, MaxConcurrentIndexing: cfg.ClipIndexer.MaxConcurrentIndexing, DBPath: bundle.DB.Path()}, bundle.DB, bundle.DB.Path(), log)
	if err := clipIndexerSvc.StartServer(ctx); err != nil {
		log.Warn("failed to start embedding server", zap.Error(err))
	} else {
		clipIndexerSvc.StartWatchdog(ctx)
	}
	return clipCatalogRepo, clipIndexerSvc
}

// StockBundle is the capability bundle for the stock-pipeline module.
//
// PR4d-chunk2 (June 2026): wraps the 7 cross-bundle reads of WireStockPipeline.
type StockBundle struct {
	DriveUploader      *driveutil.Uploader
	Jobs               *appjobs.Service
	JobFacade          jobdomain.Service
	AssetIndexService  *assetindex.Service
	ClipsRepo          *assets.ClipsRepository
	YoutubeClipService *ytService.Service
	ClipIndexerService *clipindexer.Service
	Dispatcher         *outbox.Dispatcher
	Publisher          delivery.Publisher
}

// StockPipelineWiring holds the StockPipeline module wiring.
//
// Blocco C1-Step 6 (June 2026): Handler field removed. The HTTP Handler
// is constructed inside `stock.Build(deps)` and captured by the
// returned StockDescriptor's Module closure. No caller (composition
// root, tests, internal services) needs to read the raw `*stock.Handler`
// outside the package — matches the artlist / youtube / clips precedent
// of dropping the explicit Handler field in favor of descriptor-only
// wiring. The pre-Step-6 `Handler` field has no non-HTTP consumer in
// the codebase (/run + /search-and-run are the entire public surface,
// both reachable via HTTP).
type StockPipelineWiring struct {
	Module  api.Module
	Service *stockpipeline.Service
}

// WireStockPipeline creates the StockPipeline service, handler, and module.
//
// PR4d-chunk2 (June 2026): takes *StockBundle.
// PR6 (June 2026): also constructs the canonical StockRenderer +
// VideoCutter infra adapters — PR-D (June 2026) injects them via the
// ctor-injected Deps struct (no setters), so the application layer
// never reaches into ffmpeg/process directly.
//
// PR-D (June 2026): the 9 legacy setters (SetCutter / SetRenderer /
// SetClipsRepo / SetAssetIndex / SetDispatcher / SetJobsSvc /
// SetYoutubeService / SetClipIndexer / SetMetadataWriter) were
// removed. WireStockPipeline now constructs Deps{...} in one literal
// — the late-bind ordering hazard that previously swapped the
// canonical ingestion path between BuildDomainBundle returning and
// the per-setter call is closed.
func WireStockPipeline(cfg *config.Config, log *zap.Logger, bundle *StockBundle) (*StockPipelineWiring, error) {
	if bundle.DriveUploader == nil {
		log.Warn("stock pipeline not wired: missing drive client")
		return nil, nil
	}

	// PR6 port wiring: render adapter + cutter adapter. The application
	// layer talks to the canonical stock ports; this composition root is
	// the only place that knows the concrete adapters exist.
	ffmpegPath := cfg.External.FfmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	transitionRegistry := render.DefaultTransitionRegistry()
	renderer := render.NewFFmpegRenderer(ffmpegPath, transitionRegistry, log)
	cutter := render.NewFFmpegCutter(ffmpegPath, log)
	log.Info("stock pipeline ports wired",
		zap.Int("transition_catalog_size", transitionRegistry.Len()))

	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	log.Info("metadata writer wired into stock pipeline")

	// PR-D: ctor injection via Deps{} literal. Composition-root
	// pre-rejection: every required dep MUST be non-nil by the time we
	// reach this call; a nil surfaces here as a fail-fast error so the
	// operator sees the missing dep at startup rather than racing the
	// late-bind setter sequence.
	if bundle.ClipsRepo == nil {
		return nil, fmt.Errorf("WireStockPipeline: bundle.ClipsRepo is required for production stock pipeline")
	}
	if bundle.AssetIndexService == nil {
		return nil, fmt.Errorf("WireStockPipeline: bundle.AssetIndexService is required for production stock pipeline")
	}
	if bundle.Dispatcher == nil {
		return nil, fmt.Errorf("WireStockPipeline: bundle.Dispatcher is required — QDRANT-002 PR7 removed the legacy fallback")
	}
	if bundle.ClipIndexerService == nil {
		return nil, fmt.Errorf("WireStockPipeline: bundle.ClipIndexerService is required for production stock pipeline")
	}

	svc, err := stockpipeline.NewService(stockpipeline.Deps{
		Cfg:       cfg,
		Log:       log,
		// FASE 9: .Service access necessary — stockpipeline.Deps.Drive is typed
		// as *gdrive.Service. Future migration: change Deps.Drive to a port interface.
		Drive:     bundle.DriveUploader.Service,
		Publisher: bundle.Publisher,
		Storage: stockpipeline.StorageDeps{
			ClipsRepo:  bundle.ClipsRepo,
			AssetIndex: bundle.AssetIndexService,
			Dispatcher: bundle.Dispatcher,
		},
		Media: stockpipeline.MediaDeps{
			Cutter:      cutter,
			Renderer:    renderer,
			ClipIndexer: bundle.ClipIndexerService,
			MetaWriter:  metaWriter,
		},
		YouTube: bundle.YoutubeClipService,
		Jobs:    bundle.Jobs,
	})
	if err != nil {
		return nil, fmt.Errorf("WireStockPipeline: stockpipeline.NewService: %w", err)
	}

	// S2b refactor (June 2026): construct the use case first so the API
	// handler holds only the use case + logger; the dispatch decision
	// (async-vs-sync, jobs-required 503) lives in stockpipeline.StockUseCase.
	useCase := stockpipeline.NewStockUseCase(svc, bundle.JobFacade, log)
	// Blocco C1-Step 6 (June 2026): Stock capability is now built via
	// the canonical stock.Build(deps) (api.Descriptor, error) contract,
	// matching the artlist / youtube / clips precedent. The HTTP Handler
	// is constructed inside Build and captured by the returned
	// StockDescriptor's Module closure. The composition site
	// type-asserts ONCE to *stock.StockDescriptor (fail-closed) and
	// reuses the concrete for the StockPipelineWiring.Module field
	// (which satisfies api.Module structurally). The canonical
	// late-bind svc.RegisterHandler(bundle.Jobs) step stays at the
	// end (matches the artlist + youtube pattern — service-side job
	// registration lives outside the Build contract because the Stock
	// Descriptor does not register its own job slot today; no
	// DescriptorJobs implementation, no Descriptor.Service field).
	descriptor, err := stock.Build(stock.Dependencies{
		UseCase:     useCase,
		EnabledFunc: func() bool { return cfg != nil && cfg.Features.StockPipelineEnabled },
		ModuleOpts:  nil, // no per-feature middleware for the stock capability (matches pre-Step-6 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, fmt.Errorf("WireStockPipeline: stock.Build: %w", err)
	}
	sd, ok := descriptor.(*stock.StockDescriptor)
	if !ok || sd == nil {
		return nil, fmt.Errorf("WireStockPipeline: stock.Build returned unexpected descriptor type %T (want *stock.StockDescriptor)", descriptor)
	}
	svc.RegisterHandler(bundle.Jobs)
	return &StockPipelineWiring{Module: sd.Module, Service: svc}, nil
}

// YouTubeClipWiring holds the YouTube Clip module wiring.
//
// Blocco C1-Step 4 (June 2026): Handler field removed. The HTTP Handler
// is constructed inside `ytsources.Build(deps)` and captured by the
// returned YouTubeDescriptor's Module closure. No caller (composition
// root, tests, internal services) needs to read the raw
// `*YouTubeClipHandler` outside the package — matches the artlist /
// channels precedent of dropping the explicit Handler field in favor of
// descriptor-only wiring.
type YouTubeClipWiring struct {
	Module  api.Module
	Service *ytService.Service
}

// WireYouTubeClip creates the YouTube Clip service + descriptor via the
// canonical `ytsources.Build(deps Dependencies) (api.Descriptor, error)`
// entrypoint (Blocco C1-Step 4, June 2026).
//
// The composition root has the only knowledge of `cfg.Features.YouTubeEnabled`
// and the canonical typed-narrow ports (`*assets.ClipsRepository`,
// `appassets.ToolChecker`, providers.SearchAggregator, …). Build maps
// those onto the typed-narrow `ytsources.Dependencies` struct.
//
// The canonical late-bind `ytSvc.RegisterHandler(jobs)` step stays at the
// end of WireYouTubeClip (matches the artlist pattern at the end of
// wireArtlistService — the late-bind lands AFTER the Service is fully
// constructed; no Descriptor slot is exposed today because DescriptorJobs
// contract is reserved for capabilities that own worker-side logic via
// the Build return shape itself).
//
// nil-tolerant: when ytSvc is nil, returns nil + nil error so the
// composition root's tolerant skip path stays intact (the bundle can
// be wired with optional deps missing and the capability does not
// inline-mount its routes).
func WireYouTubeClip(cfg *config.Config, log *zap.Logger, ytSvc *ytService.Service, jobFacade jobdomain.Service, jobs *appjobs.Service, clipsRepo *assets.ClipsRepository, toolChecker appassets.ToolChecker, idempotencyMiddleware gin.HandlerFunc, searchAggregator *providers.SearchAggregator) (*YouTubeClipWiring, error) {
	if ytSvc == nil {
		return nil, nil // tolerated: module is skipped
	}
	descriptor, err := wireYouTubeClipModule(cfg, ytSvc, jobFacade, clipsRepo, toolChecker, idempotencyMiddleware, searchAggregator, log)
	if err != nil {
		return nil, fmt.Errorf("WireYouTubeClip: %w", err)
	}
	yd, typeAssertOk := descriptor.(*ytsources.YouTubeDescriptor)
	if !typeAssertOk || yd == nil {
		return nil, fmt.Errorf("WireYouTubeClip: ytsources.Build returned unexpected descriptor type %T (want *ytsources.YouTubeDescriptor)", descriptor)
	}
	// Canonical late-bind step: route the YouTube service's worker
	// handlers (extraction, channel sync, …) into jobs.Service at
	// composition time. Stays outside the Build contract because
	// today's YouTube Descriptor does NOT register its own job slot
	// (no DescriptorJobs implementation); the registration happens
	// once per process via the canonical service.RegisterHandler(jobs).
	ytSvc.RegisterHandler(jobs)
	return &YouTubeClipWiring{Module: yd.Module, Service: ytSvc}, nil
}

// wireYouTubeClipModule composes the YouTube HTTP module by delegating
// to the canonical `ytsources.Build(deps Dependencies) (api.Descriptor, error)`
// entrypoint. The composition root has the only knowledge of
// `cfg.Features.YouTubeEnabled`; this function maps that onto the
// typed-narrow Dependencies.
//
// Always returns (non-nil descriptor, nil error) when ytSvc is non-nil;
// the caller (WireYouTubeClip) handles the nil-tolerance path before
// reaching this helper.
//
// Note: the `*appjobs.Service` (`jobs` arg of WireYouTubeClip) is
// consumed by the late-bind `ytSvc.RegisterHandler(jobs)` step at the
// end of WireYouTubeClip — it is intentionally NOT threaded through
// this helper because Build does not register any job-handler slot
// (the YouTube Descriptor does not implement DescriptorJobs). Mirrors
// the artlist pattern where `bundle.Jobs.Service.RegisterHandler(artlistSvc.HandleJob)`
// stays at the WireArtlist-service step, NOT inside the Build contract.
func wireYouTubeClipModule(cfg *config.Config, ytSvc *ytService.Service, jobFacade jobdomain.Service, clipsRepo *assets.ClipsRepository, toolChecker appassets.ToolChecker, idempotencyMiddleware gin.HandlerFunc, searchAggregator *providers.SearchAggregator, log *zap.Logger) (api.Descriptor, error) {
	return ytsources.Build(ytsources.Dependencies{
		Service:          ytSvc,
		Jobs:             jobFacade, // NewYouTubeClipHandler accepts jobservice.Service (jobFacade implements it)
		ClipStorePort:    newClipStoreAdapter(clipsRepo),
		ToolChecker:      toolChecker,
		Idempotency:      idempotencyMiddleware,
		SearchAggregator: searchAggregator,
		EnabledFunc:      func() bool { return cfg.Features.YouTubeEnabled },
		ModuleOpts:       nil, // no per-feature middleware for the clips capability (matches pre-Step-4 wiring);
		Logger:           log,
	})
}

// FullImagesWiring holds the FullImages module wiring.
//
// PR3 (June 2026): Wave 14 close. The handler was moved from
// `internal/api/fullimages/` to `internal/api/images/` as a sibling
// of ImagesHandler. The route prefix stays `/fullimages` (NOT
// `/images`) so the public REST URL stays unchanged — zero-change-
// contract per PR3 spec. The sub-path `/video/generate` is unchanged
// (no collision with `ImagesHandler.Generate` which mounts at
// `/generate` under the `/images` prefix).
type FullImagesWiring struct {
	Handler *imagesapi.FullImagesHandler
	Module  module.Module
}

// WireFullImages creates the FullImages handler and module.
//
// PR4d-chunk1 (June 2026): narrow bundle signature. Takes the canonical
// ImageService + MediaStore directly — sourced from root.Domains.ImageService
// and root.Drive.MediaStore in WireRegistry. Zero *CoreDeps dependency.
//
// PR3 (June 2026): Wave 14 close moved the receiver into
// `internal/api/images/` (the handler package), but the route prefix
// stays /fullimages (zero-change-contract — the URL must stay at
// /api/fullimages/video/generate). The new module path is co-located
// with ImagesHandler internally but the public REST contract is
// unchanged.
func WireFullImages(cfg *config.Config, log *zap.Logger, imageSvc *imgservice.Service, mediaStore *driveup.Store) (*FullImagesWiring, error) {
	if imageSvc == nil {
		log.Warn("fullimages: ImageService not available, skipping module")
		return nil, nil
	}
	svc := fullimagessvc.NewService(
		imageSvc,
		ffmpeg.NewFromConfig(cfg),
		mediaStore,
		cfg.Storage.ImagesPath(),
		log,
	)
	handler := imagesapi.NewFullImagesHandler(svc)
	// Wave 14 close (June 2026, PR3): the receiver was moved from
	// internal/api/fullimages/ into internal/api/images/ as a sibling
	// of ImagesHandler, but the public URL stays at /fullimages to
	// satisfy the zero-change-contract guarantee (public REST contract
	// is inviolate per the user spec).
	mod := module.NewRouteModule(
		"fullimages",
		func() bool { return cfg.Features.ImagesEnabled },
		"/fullimages",
		handler,
		log,
	)
	log.Info("created FullImages module (handler in api/images/, prefix /fullimages retained for zero-change-contract)")
	return &FullImagesWiring{Handler: handler, Module: mod}, nil
}
