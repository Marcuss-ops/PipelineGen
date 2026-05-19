package app

import (
	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/config"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/media/assetregistry"
	"velox/go-master/internal/media/clipcatalog"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/clipresolver"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/ontology"
	"velox/go-master/internal/module"
	"velox/go-master/internal/pkg/matchingconfig"
	artlistPkg "velox/go-master/internal/sources/artlist"
	"velox/go-master/internal/storage/assetdestination"
	driveutil "velox/go-master/internal/upload/drive"
)

// ArtlistWiring holds the Artlist module wiring
type ArtlistWiring struct {
	Handler *sources.ArtlistHandler
	Module  module.Module
	Service *artlistPkg.Service
}

// WireArtlist creates the Artlist service, handler, and module
func WireArtlist(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ArtlistWiring, error) {
	// 1. Lifecycle
	artlistLifecycle := wireArtlistLifecycle(coreDeps, log)

	// 2. Catalog & Indexer
	clipCatalogRepo, clipIndexerSvc := wireArtlistCatalog(cfg, coreDeps, log)

	// 3. Resolvers
	assetDestResolver := wireAssetDestinationResolver(cfg, coreDeps, log)

	// Load presets early
	presetsConfig, err := artlistPkg.LoadPresets("config/artlist_presets.yaml")
	if err != nil {
		log.Warn("failed to load artlist presets, using defaults", zap.Error(err))
	}

	// 4. Service
	artlistSvc, err := wireArtlistService(cfg, coreDeps, artlistLifecycle, assetDestResolver, clipIndexerSvc, log)
	if err != nil {
		log.Warn("Failed to create Artlist service", zap.Error(err))
		return nil, err
	}

	// 5. Clip Resolver
	clipResolver := wireClipResolver(cfg, coreDeps, clipCatalogRepo, presetsConfig, log)
	if clipResolver != nil {
		coreDeps.ClipResolver = clipResolver
	}

	// 6. Handler
	handler := wireArtlistHandler(cfg, artlistSvc, coreDeps, clipResolver, presetsConfig, log)

	// 7. Module
	var mod module.Module
	if artlistSvc != nil && handler != nil {
		mod = module.NewArtlistModule(cfg, log, artlistSvc, handler)
		log.Info("created Artlist module")
	}

	return &ArtlistWiring{
		Handler: handler,
		Module:  mod,
		Service: artlistSvc,
	}, nil
}

func wireArtlistHandler(
	cfg *config.Config,
	artlistSvc *artlistPkg.Service,
	coreDeps *CoreDeps,
	clipResolver *clipresolver.Service,
	presetsConfig *artlistPkg.PresetsConfig,
	log *zap.Logger,
) *sources.ArtlistHandler {
	if artlistSvc == nil {
		return nil
	}
	return sources.NewArtlistHandler(
		artlistSvc,
		coreDeps.CatalogSyncService,
		coreDeps.JobsService,
		clipResolver,
		"node-scraper",
		log,
		presetsConfig,
		cfg,
	)
}

func wireArtlistLifecycle(coreDeps *CoreDeps, log *zap.Logger) *lifecycle.Service {
	clipsRegistry := assetregistry.NewClipsRegistry(coreDeps.ArtlistRepo)
	return NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
	}, log)
}

func wireAssetDestinationResolver(cfg *config.Config, coreDeps *CoreDeps, log *zap.Logger) destination.Resolver {
	if coreDeps.DriveClient != nil {
		assetDest := assetdestination.NewResolver(cfg, log, coreDeps.DriveClient)
		return assetdestination.ToCoreResolver(assetDest)
	}
	return nil
}

func wireClipResolver(cfg *config.Config, coreDeps *CoreDeps, clipCatalogRepo *clipcatalog.Repository, presetsConfig *artlistPkg.PresetsConfig, log *zap.Logger) *clipresolver.Service {
	if clipCatalogRepo == nil {
		return nil
	}

	var harvestSvc clipresolver.ArtlistHarvestService
	if coreDeps.JobsService != nil {
		harvestSvc = clipresolver.NewJobHarvestService(coreDeps.JobsService, log, presetsConfig, driveutil.ResolveArtlistRootFolderID(cfg))
	}

	matchingCfg, err := matchingconfig.LoadMatchingConfig("config/matching.yaml")
	if err != nil {
		log.Warn("failed to load matching config, using defaults", zap.Error(err))
	}

	// Load ontology registry
	ontologyReg, err := ontology.LoadRegistry("config/ontology.yaml")
	var ontologyScorer clipresolver.OntologyScorer
	if err != nil {
		log.Warn("failed to load ontology registry", zap.Error(err))
	} else {
		ontologyScorer = ontology.NewScorer(ontologyReg)
	}

	// Create embedding provider (points to the Python server started by clipindexer)
	embedProvider := clipresolver.NewPythonEmbeddingProvider("http://127.0.0.1:8001", clipCatalogRepo)

	// Build map of prioritized repositories
	repos := make(map[string]*clipcatalog.Repository)

	// 1. Stock database (highest priority)
	if coreDeps.StockDriveRepo != nil && coreDeps.StockDriveRepo.DB() != nil {
		repos["stock"] = clipcatalog.NewRepository(coreDeps.StockDriveRepo.DB(), log)
		repos["stock"].SetServerInfo("http://127.0.0.1:8001", cfg.Storage.StockDBFullPath())
		repos["stock"].SetSource("stock")
	}

	// 2. YouTube clips database
	if coreDeps.MediaDB != nil && coreDeps.MediaDB.DB != nil {
		repos["youtube"] = clipcatalog.NewRepository(coreDeps.MediaDB.DB, log)
		repos["youtube"].SetServerInfo("http://127.0.0.1:8001", coreDeps.MediaDB.Path())
		repos["youtube"].SetSource("youtube")
	}

	// 3. Artlist database (fallback)
	repos["artlist"] = clipCatalogRepo
	repos["artlist"].SetSource("artlist")

	return clipresolver.NewService(repos, harvestSvc, embedProvider, ontologyScorer, matchingCfg)
}

func wireArtlistService(
	cfg *config.Config,
	coreDeps *CoreDeps,
	artlistLifecycle *lifecycle.Service,
	assetDestResolver destination.Resolver,
	clipIndexerSvc *clipindexer.Service,
	log *zap.Logger,
) (*artlistPkg.Service, error) {
	artlistSvc, err := artlistPkg.NewService(
		cfg,
		coreDeps.DB.DB,
		coreDeps.MediaDB.DB,
		coreDeps.ArtlistRepo,
		coreDeps.MediaProcessor,
		artlistLifecycle,
		assetDestResolver,
		clipIndexerSvc,
		coreDeps.JobsService,
		coreDeps.DriveClient,
		log,
	)

	if err != nil {
		return nil, err
	}

	// Register artlist job handler
	if artlistSvc != nil && coreDeps.JobsService != nil {
		coreDeps.JobsService.RegisterHandler(models.JobTypeArtlistRun, artlistSvc.HandleJob)
		log.Info("registered artlist job handler")
	}

	return artlistSvc, nil
}
