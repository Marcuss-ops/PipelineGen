// Package images (api/images) — all_territories_handler.go holds
// the territory=all aggregator that fans out across retrieved
// and generated territories and merges results in canonical order.
//
// Per the golden rule: all = aggregator only, never own business
// logic. This handler delegates to the canonical read seams
// (SearchAndDownload for retrieved, searchGeneratedTerritory for
// generated) and merges deterministically: retrieved first, then
// generated. It must NOT contain territory-specific query logic.
package images

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// allTerritoriesAggregate fans out across both wired
// territories and merges results in canonical order.
//
// PR-GENERATED-SEARCH-FIX (July 2026): the generated
// branch routes through the canonical ListImagesByOrigin read
// seam so the user-facing aggregator (territory=all) sees the
// same generated-territory rows as the canonical
// /api/images/generated/search endpoint.
//
// Order is canonical: retrieved first, generated second.
// Error from the generated helper short-circuits the merge
// and writes the typed 400/500 envelope (no double-write with
// this aggregator's own apiutil.OK).
func (h *ImagesHandler) allTerritoriesAggregate(c *gin.Context) {
	results := make([]ImageSearchResult, 0, 8)

	// Retrieved first — has the canonical query-driven path.
	if c.Query("q") != "" {
		lang := c.DefaultQuery("lang", "en")
		slug := textutil.Slugify(c.Query("q"))
		searchResult, err := h.service.SearchAndDownloadDetailed(
			c.Request.Context(),
			slug, c.Query("q"), c.Query("q"), lang, nil,
		)
		if err == nil && searchResult != nil && searchResult.Asset != nil {
			results = append(results, assetToResultWithCache(searchResult.Asset, boolPtr(searchResult.CacheHit), searchResult.CacheSource, searchResult.RetrievalProvider))
		}
	}

	// Generated second — canonical read seam.
	genResults, err := h.listGeneratedTerritoryResults(c)
	if err != nil {
		if errors.Is(err, ErrInvalidGeneratedSearchLimit) {
			apiutil.BadRequest(c, err.Error())
			return
		}
		apiutil.InternalError(c, err)
		return
	}
	results = append(results, genResults...)

	if h.service.Log() != nil {
		h.service.Log().Info("territory=all aggregate: count",
			zap.Int("count", len(results)),
		)
	}
	apiutil.OK(c, ImageSearchResults{
		Results: results,
		Count:   len(results),
	})
}
