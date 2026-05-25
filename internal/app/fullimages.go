package app

import (
	fullimageshandler "velox/go-master/internal/api/handlers/fullimages"
	"velox/go-master/internal/config"
	"velox/go-master/internal/media/fullimages"
	"velox/go-master/internal/module"
	"velox/go-master/internal/pkg/media/ffmpeg"
	driveup "velox/go-master/internal/upload/drive"

	"go.uber.org/zap"
)

// FullImagesWiring holds the FullImages module wiring.
type FullImagesWiring struct {
	Handler *fullimageshandler.FullImagesHandler
	Module  module.Module
}

// WireFullImages creates the FullImages handler and module.
func WireFullImages(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*FullImagesWiring, error) {
	if coreDeps.ImageService == nil {
		log.Warn("fullimages: ImageService not available, skipping module")
		return nil, nil
	}

	svc := fullimages.NewService(
		coreDeps.ImageService,
		ffmpeg.New(cfg),
		&driveup.Uploader{Service: coreDeps.DriveClient, Log: log},
		cfg.Storage.ImagesPath(),
		cfg.Drive.ImagesRootFolder,
		log,
	)
	handler := fullimageshandler.NewFullImagesHandler(svc)

	mod := module.NewRouteModule(
		"fullimages",
		func(cfg *config.Config) bool { return cfg.Features.ImagesEnabled },
		"/fullimages",
		handler,
		log,
	)
	log.Info("created FullImages module using RouteModule")

	return &FullImagesWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
