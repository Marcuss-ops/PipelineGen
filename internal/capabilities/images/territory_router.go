// Package images (api/images) — territory_router.go holds the
// TerritorySearch handler that routes to retrieved/generated/all
// territory branches. Per the July 2026 image-restructuring plan,
// each territory branch lives in its own capability file.
//
// The canonical image search API is split by territory:
//
//	GET /api/images/search?territory=retrieved|generated|all
//	                              → Aggregator; default = retrieved
package images

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// territoryAggregators is the canonical territory → aggregator
// dispatch table. Map lookup keeps the C2-C AST check deterministic.
// switch-case detection (godlike/06 SSOT co-located structural
// validation: the canonical HTTP-route scope lives in the
// territory handler itself, not in a shared registry).
var territoryAggregators = map[string]func(*ImagesHandler, *gin.Context){
	"retrieved": func(h *ImagesHandler, c *gin.Context) { h.retrievedAggregate(c) },
	"":          func(h *ImagesHandler, c *gin.Context) { h.retrievedAggregate(c) },
	"generated": func(h *ImagesHandler, c *gin.Context) { h.generatedAggregate(c) },
	"all":       func(h *ImagesHandler, c *gin.Context) { h.allTerritoriesAggregate(c) },
}

// TerritorySearch handles GET /api/images/search with a
// territory query param. Replaces the pre-Step-10 /search
// handler — callers that used /search?q=X without ?territory
// default to territory=retrieved (same behaviour).
//
// territory=retrieved → delegates to retrievedAggregate.
// territory=generated → delegates to generatedAggregate.
// territory=all      → delegates to allTerritoriesAggregate.
func (h *ImagesHandler) TerritorySearch(c *gin.Context) {
	territory := c.DefaultQuery("territory", "retrieved")
	if agg, ok := territoryAggregators[territory]; ok {
		agg(h, c)
		return
	}
	apiutil.BadRequest(c, fmt.Sprintf("unknown territory=%q (expected retrieved|generated|all)", territory))
}
