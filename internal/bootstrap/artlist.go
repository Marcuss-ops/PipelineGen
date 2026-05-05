package bootstrap

import (
	"path/filepath"

	artlistHandler "velox/go-master/internal/api/handlers/artlist"
	artlistService "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/service/drivereconcile"
	"velox/go-master/internal/service/mediaregistry"
	 drive "velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
	
	"go.uber.org/zap"
)

// ArtlistDeps holds the dependencies needed for the Artlist module
type ArtlistDeps struct {
	ArtlistService   *artlistService.Service
	ArtlistHandler   *artlistHandler.Handler
	DriveCleanupSvc  *drivecleanup.Service
	DriveReconcileSvc *drivereconcile.Service
}

// InitArtlistModule initializes the Artlist module
func InitArtlistModule(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ArtlistDeps, CleanupFunc, error) {
	
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
	artlistDriveService := artlistService.NewDriveService(coreDeps.DriveClient, driveFolderID, driveDestinationService, log)
	
	artlistSvc, err := artlistService.NewService(
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
		// Continue without artlist service
	}
	
	// Create Artlist handler
	var artlistHandlerVar *artlistHandler.Handler
	if artlistSvc != nil {
	artlistHandlerVar = artlistHandler.NewHandler(
			artlistSvc,
			coreDeps.CatalogSyncService,
			coreDeps.JobsService,
			cfg.Paths.NodeScraperDir,
			log,
		)
	}
	
	// Create drive cleanup service
	var driveCleanupSvc *drivecleanup.Service
	if coreDeps.DriveClient != nil {
		driveCleanupSvc = drivecleanup.NewService(coreDeps.ArtlistRepo, coreDeps.DriveClient, log, true)
		log.Info("drive cleanup service initialized (trash mode)")
	}
	
	// Create drive reconcile service
	var driveReconcileSvc *drivereconcile.Service
	if coreDeps.DriveClient != nil {
		driveReconcileSvc = drivereconcile.NewService(coreDeps.ArtlistRepo, coreDeps.DriveClient, log)
		log.Info("drive reconcile service initialized")
	}
	
	// Register artlist job handler
	if artlistSvc != nil && coreDeps.JobsService != nil {
	coreDeps.JobsService.RegisterHandler("media.artlist", artlistSvc.HandleJob)
		log.Info("registered artlist job handler")
	}
	
	deps := &ArtlistDeps{
		ArtlistService:   artlistSvc,
		ArtlistHandler:   artlistHandlerVar,
		DriveCleanupSvc:  driveCleanupSvc,
		DriveReconcileSvc: driveReconcileSvc,
	}
	
	cleanup := func() {
		if artlistSvc != nil {
			artlistSvc.Close()
		}
	}
	
	return deps, cleanup, nil
}
