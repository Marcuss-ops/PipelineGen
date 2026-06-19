package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewMediaModule creates a new Media module using RouteModule
func NewMediaModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *SourcesHandler,
) *RouteModule {
	return NewRouteModule(
		"media",
		func(cfg *config.Config) bool { return handler != nil },
		"/media",
		handler,
		log,
	)
}
