package assets

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// NewScraperModule creates the Scraper module for the API registry.
// Mounted at /api/scraper/*; preserves the historical URL subtree.
func NewScraperModule(log *zap.Logger, handler *ScraperHandler) *api.RouteModule {
	return api.NewRouteModule(
		"scraper",
		func() bool { return handler != nil },
		"/scraper",
		handler,
		log,
	)
}
