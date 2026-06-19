package app

import (
	imghandler "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"

	"go.uber.org/zap"
)

// ImagesWiring holds the Images module wiring
type ImagesWiring struct {
	Handler *imghandler.ImagesHandler
	Module  module.Module
}

// WireImages creates the Images handler and module
func WireImages(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ImagesWiring, error) {
	handler := imghandler.NewImagesHandler(coreDeps.ImageService)

	mod := module.NewRouteModule(
		"images",
		func(cfg *config.Config) bool { return cfg.Features.ImagesEnabled },
		"/images",
		handler,
		log,
	)
	log.Info("created Images module using RouteModule")

	return &ImagesWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
