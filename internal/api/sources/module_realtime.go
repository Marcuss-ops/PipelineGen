package sources

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	realtimeapi "github.com/Marcuss-ops/PipelineGen/internal/api/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

	"go.uber.org/zap"
)

// NewRealtimeModule creates the Realtime module for the API registry.
// The handler lives in internal/api/realtime/; this factory wraps it as a RouteModule.
func NewRealtimeModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *realtimeapi.MatchHandler,
) *api.RouteModule {
	realtimeEnabled := cfg != nil && cfg.VectorSearch.RealtimeEnabled
	return api.NewRouteModule(
		"realtime",
		func() bool { return handler != nil && realtimeEnabled },
		"",
		handler,
		log,
	)
}
