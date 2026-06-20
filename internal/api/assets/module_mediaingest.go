package assets

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"

	"go.uber.org/zap"
)

// NewMediaIngestModule creates the MediaIngest module for the API
// registry. Mounted at /api/media/*; preserves the historical URL
// subtree.
func NewMediaIngestModule(
	cfg *config.Config,
	log *zap.Logger,
	handler *MediaingestHandler,
) *api.RouteModule {
	return api.NewRouteModule(
		"media-ingest",
		func(cfg *config.Config) bool { return handler != nil },
		"/media",
		handler,
		log,
	)
}
