package bootstrap

import (
	"path/filepath"

	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	artlistPkg "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/service/mediaregistry"
	drive "velox/go-master/internal/upload/drive"
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
	driveDestinationService := drivedestination.NewService(cfg, log, coreDeps.DriveClient)

	// Create Artlist service with drive destination
	artlistDBPath := filepath.Join(cfg.Storage.DataDir, "artlist.db.sqlite")
	driveFolderID := drive.ResolveArtlistRootFolderID(cfg)

	// Create mediaregistry components for Artlist
	clipsRegistry := mediaregistry.NewClipsRegistry(coreDeps.ArtlistRepo)
	driveVerifier := mediaregistry.NewAPIDriveVerifier(coreDeps.DriveClient)
	mediaFinalizer := mediaregistry.NewFinalizerWithAssetIndex(clipsRegistry, driveVerifier, coreDeps.AssetIndexService, log)

	// Create Artlist DriveService
	artlistDriveService := artlistPkg.NewDriveService(coreDeps.DriveClient, driveFolderID, driveDestinationService, log)

	artlistSvc, err := artlistPkg.NewService(
		cfg,
		coreDeps.DB.DB,
		coreDeps.JobsDB,
		artlistDBPath,
		cfg.Paths.NodeScraperDir,
		coreDeps.ArtlistRepo,
		artlistDriveService,
		coreDeps.MediaProcessor,
		mediaFinalizer,
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
