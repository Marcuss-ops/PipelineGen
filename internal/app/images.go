package app

import (
	imagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/images"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"

	"go.uber.org/zap"
)

// ImagesWiring holds the Images module wiring
type ImagesWiring struct {
	Handler *imagesapi.ImagesHandler
	Module  module.Module
}

// WireImages creates the Images handler and module
func WireImages(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ImagesWiring, error) {
	handler := imagesapi.NewImagesHandler(coreDeps.ImageService)

	mod := imagesapi.NewModule(cfg, log, handler)
	log.Info("created Images module using api/images")

	return &ImagesWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
