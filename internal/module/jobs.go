package module

import (
	"velox/go-master/internal/api/handlers/jobs"
	"velox/go-master/pkg/config"

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
