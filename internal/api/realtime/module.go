package realtime

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewModule creates the Realtime module for the API registry.
func NewModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *MatchHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"realtime",
		func(cfg *config.Config) bool {
			return handler != nil && cfg.VectorSearch.RealtimeEnabled
		},
		"",
		handler,
		log,
	)
}
