package app

import (
	fullimageshandler "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/fullimages"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/platform/ffmpeg"

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
		ffmpeg.NewFromConfig(cfg),
		coreDeps.MediaStore,
		cfg.Storage.ImagesPath(),
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
