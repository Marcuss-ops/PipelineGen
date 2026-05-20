package app

import (
	imghandler "velox/go-master/internal/api/handlers/images"
	"velox/go-master/internal/config"
	"velox/go-master/internal/module"

	"go.uber.org/zap"
)

// ImagesWiring holds the Images module wiring
type ImagesWiring struct {
	Handler *imghandler.Handler
	Module  module.Module
}

// WireImages creates the Images handler and module
func WireImages(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*ImagesWiring, error) {
	handler := imghandler.NewHandler(coreDeps.ImageService)

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
