package bootstrap

import (
	"context"

	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	artlistPkg "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/clipcatalog"
	"velox/go-master/internal/service/clipindexer"
	"velox/go-master/internal/service/clipresolver"
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
	clipIndexerSvc := clipindexer.NewService(nil, coreDeps.ArtlistDB.DB, log)

	artlistSvc, err := artlistPkg.NewService(
		cfg,
		coreDeps.DB.DB,
		coreDeps.ArtlistDB.DB, // Use existing properly-configured connection
		cfg.Paths.NodeScraperDir,
		coreDeps.ArtlistRepo,
		coreDeps.MediaProcessor,
		artlistLifecycle,
		nil, // assetDestResolver - not available at bootstrap
		clipIndexerSvc,
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

	// Create clipresolver service with harvest capability
	var clipResolver *clipresolver.Service
	if clipCatalogRepo != nil {
		clipResolver = clipresolver.NewService(clipCatalogRepo, nil, "")
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
