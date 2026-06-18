package module

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/config"

	"go.uber.org/zap"
)

// NewJobsModule creates a new Jobs module using RouteModule
func NewJobsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *jobs.Handler,
) *RouteModule {
	return NewRouteModule(
		"jobs",
		func(cfg *config.Config) bool { return true },
		"/jobs",
		handler,
		log,
	)
}
