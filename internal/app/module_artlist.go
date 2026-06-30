package app

import (
	"context"
	"fmt"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	middleware "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	artsources "github.com/Marcuss-ops/PipelineGen/internal/api/assets/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	mutations "github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	svcjobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/clipcatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)
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
