package bootstrap

import (
	imghandler "velox/go-master/internal/api/handlers/images"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"

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

	mod := module.NewImagesModule(cfg, log, handler)
	log.Info("created Images module")

	return &ImagesWiring{
		Handler: handler,
		Module:  mod,
	}, nil
}
