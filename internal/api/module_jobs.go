package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewJobsModule creates a new Jobs module using RouteModule
func NewJobsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *JobsHandler,
) *RouteModule {
	return NewRouteModule(
		"jobs",
		func(cfg *config.Config) bool { return true },
		"/jobs",
		handler,
		log,
	)
}
