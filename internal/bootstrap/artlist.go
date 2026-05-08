package bootstrap

import (
	"context"

	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	artlistPkg "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/module"
	"velox/go-master/internal/service/assetdestination"
	"velox/go-master/internal/service/clipcatalog"
	"velox/go-master/internal/service/clipindexer"
	"velox/go-master/internal/service/clipresolver"
	"velox/go-master/internal/service/matchingconfig"
	"velox/go-master/internal/service/mediaregistry"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"

	"go.uber.org/zap"
)

// ArtlistWiring holds the Artlist module wiring
type ArtlistWiring struct {
	Handler *artlistHandler.Handler
	Module  module.Module
	Service *artlistPkg.Service
}

// WireArtlist creates the Artlist service, handler, and module
func WireArtlist(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ArtlistWiring, error) {
	// Create mediaregistry components for Artlist
	clipsRegistry := mediaregistry.NewClipsRegistry(coreDeps.ArtlistRepo)

	// Create LifecycleService for artlist using common factory
	artlistLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: coreDeps.DriveClient,
		AssetIndex:  coreDeps.AssetIndexService,
	}, log)

	// Ensure clipcatalog schema
	if coreDeps.ArtlistDB != nil && coreDeps.ArtlistDB.DB != nil {
		if err := clipcatalog.EnsureSchema(context.Background(), coreDeps.ArtlistDB.DB, log); err != nil {
			log.Warn("failed to ensure clipcatalog schema", zap.Error(err))
		}
	}

	// Create clipcatalog repository
	clipCatalogRepo := clipcatalog.NewRepository(coreDeps.ArtlistDB.DB, log)

	// Create clipindexer service
	clipIndexerSvc := clipindexer.NewService(nil, coreDeps.ArtlistDB.DB, coreDeps.ArtlistDB.Path(), log)

	// Create asset destination resolver for Drive uploads
	var assetDestResolver destination.Resolver
	if coreDeps.DriveClient != nil {
		assetDest := assetdestination.NewResolver(cfg, log, coreDeps.DriveClient)
		assetDestResolver = assetdestination.ToCoreResolver(assetDest)
	}

	artlistSvc, err := artlistPkg.NewService(
		cfg,
		coreDeps.DB.DB,
		coreDeps.ArtlistDB.DB, // Use existing properly-configured connection
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
		log.Warn("Failed to create Artlist service", zap.Error(err))
		return nil, err
	}

	// Register artlist job handler
	if artlistSvc != nil && coreDeps.JobsService != nil {
		coreDeps.JobsService.RegisterHandler(models.JobTypeArtlistRun, artlistSvc.HandleJob)
		log.Info("registered artlist job handler")
	}

	// Load presets config early for use by clipResolver and handler
	presetsConfig, err := artlistPkg.LoadPresets("config/artlist_presets.yaml")
	if err != nil {
		log.Warn("failed to load artlist presets, using defaults", zap.Error(err))
	}

	// Create clipresolver service with harvest capability
	var clipResolver *clipresolver.Service
	if clipCatalogRepo != nil {
		var harvestSvc clipresolver.ArtlistHarvestService
		if coreDeps.JobsService != nil {
			harvestSvc = clipresolver.NewJobHarvestService(coreDeps.JobsService, log, presetsConfig)
		}

		matchingCfg, err := matchingconfig.LoadMatchingConfig("config/matching.yaml")
		if err != nil {
			log.Warn("failed to load matching config, using defaults", zap.Error(err))
		}

		clipResolver = clipresolver.NewService(clipCatalogRepo, harvestSvc, "config/ontology.yaml", matchingCfg)
	}

	// Store clipResolver in coreDeps for other wire functions
	if clipResolver != nil {
		coreDeps.ClipResolver = clipResolver
	}

	var handler *artlistHandler.Handler
	if artlistSvc != nil {
		handler = artlistHandler.NewHandler(
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

	// Create module
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

// GetClipResolver returns the clipResolver from the wiring (if available)
func (w *ArtlistWiring) GetClipResolver() *clipresolver.Service {
	if w == nil || w.Handler == nil {
		return nil
	}
	return nil
}
