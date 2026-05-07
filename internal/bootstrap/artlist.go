package bootstrap

import (
	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	artlistPkg "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/assetpipeline"
	"velox/go-master/internal/service/mediaregistry"
	"velox/go-master/internal/module"
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
	// Create drive destination service

	// Create mediaregistry components for Artlist
	clipsRegistry := mediaregistry.NewClipsRegistry(coreDeps.ArtlistRepo)
	driveVerifier := mediaregistry.NewAPIDriveVerifier(coreDeps.DriveClient)
	mediaFinalizer := mediaregistry.NewFinalizerWithAssetIndex(clipsRegistry, driveVerifier, coreDeps.AssetIndexService, log)

	// Create LifecycleService for artlist
	artlistStore := assetpipeline.NewRegistryStoreAdapter(clipsRegistry)
	artlistLifecycle := assetpipeline.NewLifecycleService(
		artlistStore,
		coreDeps.DriveClient,
		clipsRegistry,
		coreDeps.AssetIndexService,
		mediaFinalizer,
		assetpipeline.DefaultLifecycleConfig(),
		log,
	)


	artlistSvc, err := artlistPkg.NewService(
		cfg,
		coreDeps.DB.DB,
		coreDeps.ArtlistDB.DB, // Use existing properly-configured connection
		cfg.Paths.NodeScraperDir,
		coreDeps.ArtlistRepo,
		coreDeps.MediaProcessor,
		artlistLifecycle,
		nil,
		coreDeps.JobsService,
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

	var handler *artlistHandler.Handler
	if artlistSvc != nil {
		handler = artlistHandler.NewHandler(
			artlistSvc,
			coreDeps.CatalogSyncService,
			coreDeps.JobsService,
			cfg.Paths.NodeScraperDir,
			log,
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
