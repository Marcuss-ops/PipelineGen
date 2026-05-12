package module

import (
	"context"

	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// NewAssetsModule creates a new Assets module using RouteModule
func NewAssetsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *sources.Handler,
) *RouteModule {
	return NewRouteModule(
		"assets",
		func(cfg *config.Config) bool { return handler != nil },
		"/assets",
		handler,
		log,
		WithStart(func(ctx context.Context) error {
			log.Info("starting assets module")
			return nil
		}),
	)
}
