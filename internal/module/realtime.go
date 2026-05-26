package module

import (
	realtimehandler "velox/go-master/internal/api/handlers/realtime"
	"velox/go-master/internal/config"

	"go.uber.org/zap"
)

// NewRealtimeModule creates a new Realtime module using RouteModule.
func NewRealtimeModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *realtimehandler.MatchHandler,
) *RouteModule {
	return NewRouteModule(
		"realtime",
		func(cfg *config.Config) bool {
			return handler != nil && cfg.VectorSearch.RealtimeEnabled
		},
		"/api",
		handler,
		log,
	)
}
