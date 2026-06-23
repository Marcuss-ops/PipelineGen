package sources

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

	"go.uber.org/zap"
)

// NewSourcesModule creates the Sources module for the API registry.
// It wraps the thin Handler in a RouteModule that registers under /api/media.
func NewSourcesModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *SourcesHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"assets",
		func() bool { return handler != nil },
		"/media",
		handler,
		log,
	)
}
