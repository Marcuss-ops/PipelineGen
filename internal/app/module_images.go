package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/images"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"go.uber.org/zap"
)

// ImagesWiring holds the Images module wiring.
type ImagesWiring struct {
	Handler *images.ImagesHandler
	Module  module.Module
}

// WireImages creates the Images handler and module.
func WireImages(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*ImagesWiring, error) {
	handler := images.NewImagesHandler(coreDeps.ImageService)
	mod := images.NewModule(cfg, log, handler)
	log.Info("created Images module using api/images")
	return &ImagesWiring{Handler: handler, Module: mod}, nil
}
