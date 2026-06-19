package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	fullimageshandler "github.com/Marcuss-ops/PipelineGen/internal/api/fullimages"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	fullimagessvc "github.com/Marcuss-ops/PipelineGen/internal/media/fullimages"
	"go.uber.org/zap"
)

// FullImagesWiring holds the FullImages module wiring.
type FullImagesWiring struct {
	Handler *fullimageshandler.FullImagesHandler
	Module  module.Module
}

// WireFullImages creates the FullImages handler and module.
func WireFullImages(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*FullImagesWiring, error) {
	if coreDeps.ImageService == nil {
		log.Warn("fullimages: ImageService not available, skipping module")
		return nil, nil
	}
	svc := fullimagessvc.NewService(
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
	return &FullImagesWiring{Handler: handler, Module: mod}, nil
}
