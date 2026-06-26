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
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	fullimagessvc "github.com/Marcuss-ops/PipelineGen/internal/application/images/fullimages"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ytService "github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
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
	gdrive "google.golang.org/api/drive/v3"
)

// ArtlistWiring holds the Artlist module wiring.
//
// PR4d-chunk2 (June 2026): Resolver field removed. clipresolver.Service
// does not implement script.AutoHarvestService (no EnqueueHarvest method),
// so the harvest service is constructed locally in WireRegistry from
// root.Jobs.Facade (the same path used pre-PR4d). WireArtlist remains the
// canonical owner of the clipresolver construction; ArtlistWiring no longer
// needs to expose it.
type ArtlistWiring struct {
	Handler *artsources.ArtlistHandler
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
func WireArtlist(ctx context.Context, cfg *config.Config, log *zap.Logger, bundle *ArtlistBundle, dispatcher *outbox.Dispatcher) (*ArtlistWiring, error) {
	// vectorStore arg removed from this service constructor.
	artlistLifecycle := wireArtlistLifecycle(bundle, log)
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
	var enricher artlistPkg.MetadataWriter
	if bundle.ClipsRepo != nil {
		if dispatcher == nil {
			return nil, fmt.Errorf("WireArtlist: dispatcher is required at composition time — QDRANT-002 PR7 removed the legacy UpsertClip+IndexClip fallback; production must wire root.Outbox.Dispatcher")
		}
		metaWriter := semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
		enricher = artlistPkg.NewSemanticEnricher(bundle.ClipsRepo, clipIndexerSvc, metaWriter, driveManager, dispatcher, log)
		log.Info("wired semantic enricher (MetadataWriter port) with canonical outbox.Dispatcher — production canonical path active (QDRANT-002 PR7)")
	}

	artlistSvc, err := wireArtlistService(cfg, bundle, artlistLifecycle, assetDestResolver, clipIndexerSvc, enricher, driveManager, dispatcher, log)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		return nil, err
	}
	clipResolver := wireClipResolver(cfg, bundle, clipCatalogRepo, presetsConfig, log)
	handler := wireArtlistHandler(cfg, artlistSvc, bundle, clipResolver, log)
	var mod api.Module
	if artlistSvc != nil && handler != nil {
		mod = api.NewRouteModule(
			"artlist",
			func() bool { return cfg.Features.ArtlistEnabled },
			"/artlist",
			handler,
			log,
			api.WithMiddleware(middleware.FeatureFlagChecker("Artlist", cfg.Features.ArtlistEnabled)),
		)
		log.Info("created Artlist module")
	}
	return &ArtlistWiring{Handler: handler, Module: mod, Service: artlistSvc}, nil
}

func wireArtlistHandler(cfg *config.Config, artlistSvc *artlistPkg.Service, bundle *ArtlistBundle, clipResolver interface{}, log *zap.Logger) *artsources.ArtlistHandler {
	if artlistSvc == nil {
		return nil
	}
	// The clipresolver package was removed from remote (commit
	// d61068b3). wireClipResolver returns nil typed as interface{}. The ArtlistHandler constructor expects a typed
	// ClipResolverPort; perform a safe type assertion so the typed nil
	// is forwarded (handler stays nil-tolerant and short-circuits).
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
	return artsources.NewArtlistHandler(artlistSvc, bundle.CatalogSyncService, bundle.Jobs.Facade, resolver, "node-scraper", log, cfgPort)
}

func wireArtlistLifecycle(bundle *ArtlistBundle, log *zap.Logger) *lifecycle.Service {
	clipsRegistry := artifacts.NewClipsRegistry(bundle.DB.DB, bundle.Assets.Repository(), bundle.Assets, bundle.Assets.LocationRepository(), bundle.Assets.ProcessingRepository())
	return NewLifecycleFromDeps(&LifecycleDeps{Registry: clipsRegistry, DriveClient: bundle.DriveClient, AssetIndex: bundle.AssetIndexService}, log)
}

func wireAssetDestinationResolver(cfg *config.Config, bundle *ArtlistBundle, log *zap.Logger) asset.Resolver {
	if bundle.DriveClient != nil {
		storageResolver := drive.NewResolver(drive.MediaRoot(cfg.Storage.MediaPath()), drive.DriveRoot(cfg.Drive.RootFolder()))
		mediaStore := drive.NewStore(storageResolver, &driveutil.Uploader{Service: bundle.DriveClient, Log: log}, cfg.Drive.RootFolder(), "", "", cfg.Drive.SoundEffectsFolder(), log)
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
		log.Info("registered artlist job handler")
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
	DriveClient        *gdrive.Service
	Jobs               *appjobs.Service
	JobFacade          jobdomain.Service
	AssetIndexService  *assetindex.Service
	ClipsRepo          *assets.ClipsRepository
	YoutubeClipService *ytService.Service
	ClipIndexerService *clipindexer.Service
	Dispatcher         *outbox.Dispatcher
}

// StockPipelineWiring holds the StockPipeline module wiring.
type StockPipelineWiring struct {
	Handler *stock.Handler
	Module  api.Module
	Service *stockpipeline.Service
}

// WireStockPipeline creates the StockPipeline service, handler, and module.
//
// PR4d-chunk2 (June 2026): takes *StockBundle.
// PR6 (June 2026): also constructs the canonical StockRenderer +
// VideoCutter infra adapters and injects them via SetRenderer + SetCutter
// so the application layer never reaches into ffmpeg/process directly.
func WireStockPipeline(cfg *config.Config, log *zap.Logger, bundle *StockBundle) (*StockPipelineWiring, error) {
	if bundle.DriveClient == nil {
		log.Warn("stock pipeline not wired: missing drive client")
		return nil, nil
	}
	svc := stockpipeline.NewService(cfg, log, bundle.DriveClient)
	svc.SetJobsSvc(bundle.Jobs)
	svc.SetAssetIndex(bundle.AssetIndexService)
	if bundle.ClipsRepo != nil {
		svc.SetClipsRepo(bundle.ClipsRepo)
	}
	if bundle.YoutubeClipService != nil {
		svc.SetYoutubeService(bundle.YoutubeClipService)
	}
	if bundle.ClipIndexerService != nil {
		svc.SetClipIndexer(bundle.ClipIndexerService)
	}
	if bundle.Dispatcher != nil {
		svc.SetDispatcher(bundle.Dispatcher)
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
	svc.SetRenderer(renderer)
	svc.SetCutter(cutter)
	log.Info("stock pipeline ports wired",
		zap.Int("transition_catalog_size", transitionRegistry.Len()))

	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	svc.SetMetadataWriter(metaWriter)
	log.Info("metadata writer wired into stock pipeline")
	handler := stock.NewHandler(svc, bundle.JobFacade, log)
	stockEnabled := cfg != nil && cfg.Features.StockPipelineEnabled
	mod := api.NewRouteModule(
		"stock-pipeline",
		func() bool { return stockEnabled },
		"/stock-pipeline",
		handler,
		log,
	)
	svc.RegisterHandler(bundle.Jobs)
	return &StockPipelineWiring{Handler: handler, Module: mod, Service: svc}, nil
}

// YouTubeClipWiring holds the YouTube Clip module wiring.
type YouTubeClipWiring struct {
	Handler *ytsources.YouTubeClipHandler
	Module  api.Module
	Service *ytService.Service
}

// WireYouTubeClip creates the YouTube Clip handler and module.
//
// PR4d-chunk2 (June 2026): takes 4 direct narrow args (no bundle —
// only 4 cross-bundle reads, no coherence warrant for a bundle).
// PR3 (June 2026): providerRegistry added for constructor injection
// (replaces post-construction SetProviderRegistry).
// PG-003 (June 2026): clipsRepo (still typed *assets.ClipsRepository
// at the wiring seam) is passed through the canonical
// newClipStoreAdapter(...) helper defined in youtube_adapters.go. The
// handler depends on the typed youtubeports.ClipStorePort only; the
// helper itself preserves `if h.clipsRepo != nil` semantics in the
// handler because newClipStoreAdapter(nil) returns a nil interface.
// PR8 (June 2026): added idempotencyMiddleware arg — installed by
// YouTubeClipHandler on POST /clips/process.
func WireYouTubeClip(cfg *config.Config, log *zap.Logger, ytSvc *ytService.Service, jobFacade jobdomain.Service, jobs *appjobs.Service, clipsRepo *assets.ClipsRepository, providerRegistry *providers.Registry, toolChecker appassets.ToolChecker, idempotencyMiddleware gin.HandlerFunc) (*YouTubeClipWiring, error) {
	handler := ytsources.NewYouTubeClipHandler(ytSvc, log, jobFacade, providerRegistry, newClipStoreAdapter(clipsRepo), toolChecker, idempotencyMiddleware)
	var mod api.Module
	if ytSvc != nil {
		mod = api.NewRouteModule(
			"clips",
			func() bool { return cfg.Features.YouTubeEnabled },
			"/clips",
			handler,
			log,
		)
		log.Info("created Clips module")
		ytSvc.RegisterHandler(jobs)
	}
	return &YouTubeClipWiring{Handler: handler, Module: mod, Service: ytSvc}, nil
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
