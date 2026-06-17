package module

import (
	"velox/go-master/internal/api/handlers/mediaingest"
	"velox/go-master/internal/config"

	"go.uber.org/zap"
)

func NewMediaIngestModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *mediaingest.Handler,
) *RouteModule {
	return NewRouteModule(
		"media-ingest",
		func(cfg *config.Config) bool { return handler != nil },
		"/media",
		handler,
		log,
	)
}
