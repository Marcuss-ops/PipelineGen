// Package assets provides the unified Assets HTTP module that aggregates
// all asset-related sub-handlers: storage, diagnostics, search,
// media-ingest, and scraper. A single module registers all routes under
// the /api/media prefix.
package assets

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/diagnostics"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/search"
)

// Dependencies holds the pre-built sub-handlers for the Assets module.
type Dependencies struct {
	Diagnostics *diagnostics.Handler
	Search      *search.Handler
	// Existing handlers from the legacy api/assets package:
	MediaIngest *MediaingestHandler
	Scraper     *ScraperHandler
}

// Module is the unified Assets HTTP module. It does NOT implement
// lifecycle (Start/Stop) — it only registers routes. Lifecycle is
// managed by the composition root.
type Module struct {
	deps Dependencies
	log  *zap.Logger
}

// NewModule creates an AssetsModule from pre-built dependencies.
func NewModule(deps Dependencies, log *zap.Logger) *Module {
	return &Module{deps: deps, log: log}
}

// RegisterRoutes registers all asset routes under the given parent group.
// Mount at /api/media or similar base prefix via api.RouteModule.
func (m *Module) RegisterRoutes(r *gin.RouterGroup) {
	m.log.Info("Registering unified Assets module routes")

	// Diagnostics operations (index-health, qdrant health)
	if m.deps.Diagnostics != nil {
		m.deps.Diagnostics.RegisterRoutes(r)
	}

	// Search operations (cross-provider search, semantic-search, recommend)
	if m.deps.Search != nil {
		m.deps.Search.RegisterRoutes(r)
	}

	// Legacy media-ingest routes
	if m.deps.MediaIngest != nil {
		m.deps.MediaIngest.RegisterRoutes(r)
	}

	// Legacy scraper routes
	if m.deps.Scraper != nil {
		m.deps.Scraper.RegisterRoutes(r)
	}
}
