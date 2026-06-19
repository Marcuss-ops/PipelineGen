package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewModule creates the Images module for the API registry.
func NewModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *ImagesHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"images",
		func(cfg *config.Config) bool { return cfg.Features.ImagesEnabled },
		"/images",
		handler,
		log,
	)
}
