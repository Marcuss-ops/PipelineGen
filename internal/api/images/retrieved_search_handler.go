// Package images (api/images) — retrieved_search_handler.go holds
// the retrieved-territory handlers: RetrievedSearch (standalone
// GET /api/images/retrieved/search) and retrievedAggregate (the
// territory=retrieved branch of TerritorySearch).
//
// Per the golden rule: retrieved = found/downloaded/ingested images
// from normal sources (stock, Wikipedia, SearXNG, DuckDuckGo, Drive).
// These handlers use the SearchAndDownload pipeline — never the
// generated/AI read seam.
package images

import (
	"strconv"

	applicationimages "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/gin-gonic/gin"
)

// ── GET /api/images/retrieved/search ────────────────────────────────

// RetrievedSearch handles GET /api/images/retrieved/search?q=...&lang=...
// An optional provider=... selects that provider through the shared registry
// for explicit live canaries; the default remains the normal fallback path.
// Mirrors the pre-Step-10 /api/images/search semantics but is
// scoped explicitly to the Retrieved territory. Single-result
// response (search-then-download pipeline today; multi-result
// listing is a follow-up).
func (h *ImagesHandler) RetrievedSearch(c *gin.Context) {
	query := c.Query("q")
	lang := c.DefaultQuery("lang", "en")
	if query == "" {
		apiutil.BadRequest(c, "missing query parameter 'q'")
		return
	}

	slug := textutil.Slugify(query)
	if slug == "" {
		slug = textutil.Slugify(query)
	}

	var searchResult *applicationimages.SearchResult
	var err error
	provider := asset.ImageProvider(c.Query("provider"))
	limit := 1
	if rawLimit := c.Query("limit"); rawLimit != "" {
		parsed, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil || parsed < 1 || parsed > applicationimages.MaxRetrievedImageBatch {
			apiutil.BadRequest(c, "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}
	if limit > 1 {
		results, err := h.service.SearchAndDownloadManyDetailedFromProvider(
			c.Request.Context(), slug, query, query, lang, nil, provider, limit,
		)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		response := ImageSearchResults{Results: make([]ImageSearchResult, 0, len(results))}
		for _, result := range results {
			if result == nil || result.Asset == nil {
				continue
			}
			response.Results = append(response.Results, assetToResultWithCache(result.Asset, boolPtr(result.CacheHit), result.CacheSource, result.RetrievalProvider))
		}
		response.Count = len(response.Results)
		apiutil.OK(c, response)
		return
	}
	if provider != "" {
		searchResult, err = h.service.SearchAndDownloadDetailedFromProvider(
			c.Request.Context(), slug, query, query, lang, nil, provider,
		)
	} else {
		searchResult, err = h.service.SearchAndDownloadDetailed(
			c.Request.Context(), slug, query, query, lang, nil,
		)
	}
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	if searchResult == nil || searchResult.Asset == nil {
		apiutil.OK(c, ImageSearchResults{
			Results: []ImageSearchResult{},
			Count:   0,
		})
		return
	}

	res := assetToResultWithCache(searchResult.Asset, boolPtr(searchResult.CacheHit), searchResult.CacheSource, searchResult.RetrievalProvider)
	apiutil.OK(c, ImageSearchResults{
		Results: []ImageSearchResult{res},
		Count:   1,
	})
}

// retrievedAggregate is the territory=retrieved branch of
// TerritorySearch. Mirrors RetrievedSearch's body; declared
// separately so the aggregator can call it without recursion.
func (h *ImagesHandler) retrievedAggregate(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		apiutil.BadRequest(c, "missing query parameter 'q' (required for territory=retrieved)")
		return
	}
	lang := c.DefaultQuery("lang", "en")
	slug := textutil.Slugify(query)

	searchResult, err := h.service.SearchAndDownloadDetailed(
		c.Request.Context(),
		slug, query, query, lang, nil,
	)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	response := ImageSearchResults{Results: []ImageSearchResult{}, Count: 0}
	if searchResult != nil && searchResult.Asset != nil {
		response.Results = append(response.Results, assetToResultWithCache(searchResult.Asset, boolPtr(searchResult.CacheHit), searchResult.CacheSource, searchResult.RetrievalProvider))
		response.Count = 1
	}
	apiutil.OK(c, response)
}
