package sources

import (
	"context"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

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
		func(cfg *config.Config) bool { return handler != nil },
		"/media",
		handler,
		log,
		api.WithStart(func(ctx context.Context) error {
			log.Info("starting assets module")
			return nil
		}),
	)
}
