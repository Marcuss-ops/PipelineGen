package lessons

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewModule creates the Lessons module for the API registry.
func NewModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *Handler,
) *api.RouteModule {
	return api.NewRouteModule(
		"lessons",
		func(cfg *config.Config) bool { return cfg.Lessons.Enabled },
		"/lessons",
		handler,
		log,
	)
}
