package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewImagesModule creates a new Images module using RouteModule
func NewImagesModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *ImagesHandler,
) *RouteModule {
	return NewRouteModule(
		"images",
		func(cfg *config.Config) bool { return cfg.Features.ImagesEnabled },
		"/images",
		handler,
		log,
	)
}
