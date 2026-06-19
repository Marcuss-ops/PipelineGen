package api

import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

func NewMediaIngestModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *MediaingestHandler,
) *RouteModule {
	return NewRouteModule(
		"media-ingest",
		func(cfg *config.Config) bool { return handler != nil },
		"/media",
		handler,
		log,
	)
}
