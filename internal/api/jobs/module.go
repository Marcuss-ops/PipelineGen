package jobs

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewModule creates the Jobs module for the API registry.
func NewModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *JobsHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"jobs",
		func(cfg *config.Config) bool { return true },
		"/jobs",
		handler,
		log,
	)
}
