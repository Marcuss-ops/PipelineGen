package scraper

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// NewModule creates a new Scraper module.
func NewModule(log *zap.Logger, handler *ScraperHandler) *api.RouteModule {
	return api.NewRouteModule(
		"scraper",
		nil,
		"/scraper",
		handler,
		log,
	)
}
