package bootstrap

import (
	"context"

	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/module"
	artlistPkg "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/assetdestination"
	"velox/go-master/internal/service/clipcatalog"
	"velox/go-master/internal/service/clipindexer"
	"velox/go-master/internal/service/clipresolver"
	"velox/go-master/internal/service/matchingconfig"
	"velox/go-master/internal/service/mediaregistry"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
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
	clipCatalogRepo, clipIndexerSvc := wireArtlistCatalog(coreDeps, log)

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
	clipResolver := wireClipResolver(coreDeps, clipCatalogRepo, presetsConfig, log)
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

func wireArtlistCatalog(coreDeps *CoreDeps, log *zap.Logger) (*clipcatalog.Repository, *clipindexer.Service) {
	if coreDeps.ArtlistDB != nil && coreDeps.ArtlistDB.DB != nil {
		if err := clipcatalog.EnsureSchema(context.Background(), coreDeps.ArtlistDB.DB, log); err != nil {
			log.Warn("failed to ensure clipcatalog schema", zap.Error(err))
		}
	}

	clipCatalogRepo := clipcatalog.NewRepository(coreDeps.ArtlistDB.DB, log)
	clipIndexerSvc := clipindexer.NewService(nil, coreDeps.ArtlistDB.DB, coreDeps.ArtlistDB.Path(), log)

	return clipCatalogRepo, clipIndexerSvc
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
		cfg.Paths.NodeScraperDir,
		log,
		presetsConfig,
		cfg,
	)
}

func wireArtlistLifecycle(coreDeps *CoreDeps, log *zap.Logger) *lifecycle.Service {
	clipsRegistry := mediaregistry.NewClipsRegistry(coreDeps.ArtlistRepo)
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

func wireClipResolver(coreDeps *CoreDeps, clipCatalogRepo *clipcatalog.Repository, presetsConfig *artlistPkg.PresetsConfig, log *zap.Logger) *clipresolver.Service {
	if clipCatalogRepo == nil {
		return nil
	}

	var harvestSvc clipresolver.ArtlistHarvestService
	if coreDeps.JobsService != nil {
		harvestSvc = clipresolver.NewJobHarvestService(coreDeps.JobsService, log, presetsConfig)
	}

	matchingCfg, err := matchingconfig.LoadMatchingConfig("config/matching.yaml")
	if err != nil {
		log.Warn("failed to load matching config, using defaults", zap.Error(err))
	}

	return clipresolver.NewService(clipCatalogRepo, harvestSvc, "config/ontology.yaml", matchingCfg)
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
		coreDeps.ArtlistDB.DB,
		cfg.Paths.NodeScraperDir,
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
