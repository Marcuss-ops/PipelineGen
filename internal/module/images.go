package module

import (
	imghandler "velox/go-master/internal/api/handlers/images"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// NewImagesModule creates a new Images module using RouteModule
func NewImagesModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *imghandler.Handler,
) *RouteModule {
	return NewRouteModule(
		"images",
		func(cfg *config.Config) bool { return cfg.Features.ImagesEnabled },
		"/images",
		handler,
		log,
	)
}
