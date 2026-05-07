package module

import (
	scraperhandler "velox/go-master/internal/api/handlers/scraper"
	"go.uber.org/zap"
)

// NewScraperModule creates a new Scraper module using RouteModule.
func NewScraperModule(
	log *zap.Logger,
	handler *scraperhandler.Handler,
) *RouteModule {
	return NewRouteModule(
		"scraper",
		nil, // enabled check: handler != nil
		"/scraper",
		handler,
		log,
	)
}
