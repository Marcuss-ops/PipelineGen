package bootstrap

import (
	mediahandler "velox/go-master/internal/api/handlers/media"
	"velox/go-master/internal/service/drivecleanup"
	foldermemory "velox/go-master/internal/service/foldermemory"
	drive "velox/go-master/internal/upload/drive"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// MediaWiring holds the Media module wiring
type MediaWiring struct {
	Handler *mediahandler.CommonHandler
	Module  module.Module
}

// WireMedia creates the Media handler and module
func WireMedia(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*MediaWiring, error) {
	var handler *mediahandler.CommonHandler

	if coreDeps.StockDriveRepo != nil && coreDeps.ArtlistRepo != nil && coreDeps.ClipsOnlyRepo != nil {
		// Create folder memory service
		folderMemSvc := foldermemory.NewService(log, coreDeps.ArtlistRepo)

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

		handler = mediahandler.NewCommonHandler(
			coreDeps.ArtlistRepo,
			coreDeps.ClipsOnlyRepo,
			coreDeps.StockDriveRepo,
			driveCleanupSvc,
			folderMemSvc,
			coreDeps.AssetTreeService,
			driveUploader,
			coreDeps.MediaProcessor,
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

	mod := module.NewMediaModule(cfg, log, handler)
	log.Info("created Media module")

	return &MediaWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
