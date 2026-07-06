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
	switch territory {
	case "retrieved", "":
		h.retrievedAggregate(c)
		return
	case "generated":
		h.generatedAggregate(c)
		return
	case "all":
		h.allTerritoriesAggregate(c)
		return
	default:
		apiutil.BadRequest(c, fmt.Sprintf("unknown territory=%q (expected retrieved|generated|all)", territory))
		return
	}
}
