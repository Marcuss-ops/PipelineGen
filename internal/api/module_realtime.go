package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewRealtimeModule creates a new Realtime module using RouteModule.
func NewRealtimeModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *MatchHandler,
) *RouteModule {
	return NewRouteModule(
		"realtime",
		func(cfg *config.Config) bool {
			return handler != nil && cfg.VectorSearch.RealtimeEnabled
		},
		"",
		handler,
		log,
	)
}
