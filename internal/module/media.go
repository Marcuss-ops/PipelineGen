package module

import (
	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// NewMediaModule creates a new Media module using RouteModule
func NewMediaModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *sources.Handler,
) *RouteModule {
	return NewRouteModule(
		"media",
		func(cfg *config.Config) bool { return handler != nil },
		"/media",
		handler,
		log,
	)
}
