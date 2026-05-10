package bootstrap

import (
	assetshandler "velox/go-master/internal/api/handlers/assets"
	"velox/go-master/internal/module"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/media"
	drive "velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// MediaWiring holds the Media module wiring
type MediaWiring struct {
	Handler *assetshandler.Handler
	Module  module.Module
}

// WireMedia creates the Media handler and module
func WireMedia(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*MediaWiring, error) {
	var handler *assetshandler.Handler

	if coreDeps.StockDriveRepo != nil && coreDeps.ArtlistRepo != nil && coreDeps.ClipsOnlyRepo != nil {

		// Create drive uploader
		var driveUploader *drive.Uploader
		if coreDeps.DriveClient != nil {
			driveUploader = &drive.Uploader{Service: coreDeps.DriveClient, Log: log}
		}

		// Create drive cleanup service
		var driveCleanupSvc *drivecleanup.Service
		if coreDeps.DriveClient != nil {
			driveCleanupSvc = drivecleanup.NewService(coreDeps.ArtlistRepo, coreDeps.DriveClient, log, true)
		}

		// Create deletion service
		deletionSvc := media.NewDeletionService(
			coreDeps.ArtlistRepo,
			coreDeps.ClipsOnlyRepo,
			coreDeps.StockDriveRepo,
			coreDeps.VoiceoverRepo,
			coreDeps.ImageRepo,
			driveUploader,
			coreDeps.AssetTreeService,
			log,
		)

		handler = assetshandler.NewHandler(
			nil,
			nil,
			nil,
			coreDeps.ArtlistRepo,
			coreDeps.ClipsOnlyRepo,
			coreDeps.StockDriveRepo,
			driveCleanupSvc,
			nil,
			coreDeps.AssetTreeService,
			driveUploader,
			coreDeps.MediaProcessor,
			deletionSvc,
			log,
		)

		// Add voiceover repo if available
		if coreDeps.VoiceoverRepo != nil {
			handler.SetVoiceoverRepo(coreDeps.VoiceoverRepo)
			log.Info("voiceover repo added to media handler")
		}

		// Add images repo if available
		if coreDeps.ImageRepo != nil {
			handler.SetImagesRepo(coreDeps.ImageRepo)
			log.Info("images repo added to media handler")
		}

		log.Info("common media handler initialized")
	} else {
		log.Warn("common media handler not initialized - missing dependencies")
	}

	mod := module.NewRouteModule("media", nil, "/media", handler, log)
	log.Info("created Media module using RouteModule")

	return &MediaWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
