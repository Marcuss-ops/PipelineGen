package module

import (
	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/assets"
	"velox/go-master/pkg/config"
)

// NewAssetsModule creates a unified assets module using RouteModule
func NewAssetsModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *assets.Handler,
) *RouteModule {
	return NewRouteModule(
		"assets",
		func(cfg *config.Config) bool { return handler != nil },
		"/assets",
		handler,
		log,
	)
}
