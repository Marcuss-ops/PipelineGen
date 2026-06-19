package api

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// NewAssetsModule creates a new Assets module using RouteModule
func NewAssetsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *SourcesHandler,
) *RouteModule {
	return NewRouteModule(
		"assets",
		func(cfg *config.Config) bool { return handler != nil },
		"/media",
		handler,
		log,
		WithStart(func(ctx context.Context) error {
			log.Info("starting assets module")
			return nil
		}),
	)
}
