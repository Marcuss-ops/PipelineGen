// Package images (api/images) — generated_search_handler.go holds
// the generated-territory read handlers and the canonical
// generated-territory read seam (searchGeneratedTerritory +
// listGeneratedTerritoryResults).
//
// Per the golden rule: generated = AI-created images. These handlers
// use the ListImagesByOrigin read seam — the canonical SQLite-backed
// query for generated-territory rows. PR-GENERATED-SEARCH-FIX
// (July 2026) retired the Step-9 forward-pointer 200+[] stub; all
// three generated-territory read routes now return real data.
//
// godlike/06 SSOT: searchGeneratedTerritory is the SOLE canonical
// generated-territory read seam — GeneratedSearch, generatedAggregate,
// and allTerritoriesAggregate all route through it. The
// listGeneratedTerritoryResults data helper never writes to the
// response (pure data helper contract).
package images

import (
	"errors"
	"fmt"
	"strconv"

	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// ErrInvalidGeneratedSearchLimit is the typed sentinel returned by
// the data-only listGeneratedTerritoryResults helper when the
// caller supplies a malformed `limit` query parameter. Wrapped via
// fmt.Errorf %w so callers probe via errors.Is — godlike/07
// typed-error contract (no string-sniffing at the call site).
// Maps to 400 BadRequest at the handler layer; other errors map to
// 500 InternalError.
var ErrInvalidGeneratedSearchLimit = errors.New("invalid generated-search limit (must be a non-negative integer)")

// ── GET /api/images/generated/search ────────────────────────────────

// GeneratedSearch handles GET /api/images/generated/search.
//
// PR-GENERATED-SEARCH-FIX (July 2026): the underlying SQLite-backed
// read seam is live. Query parameters:
//
//	?origin=<asset.ImageOrigin>  default "generated" (canonical
//	                              generated-territory territory)
//	?limit=<int>                 default + cap = 200
//
// Returns matching media_assets rows projected to the unified
// ImageSearchResult DTO, ordered by created_at DESC. Per godlike/07
// no-fake-availability, the canonical "endpoint alive but feature
// pending" 200+[] stub is RETIRED — the handler now returns real
// data when rows exist, and an empty list when the query matches
// no rows.
func (h *ImagesHandler) GeneratedSearch(c *gin.Context) {
	h.searchGeneratedTerritory(c)
}

// generatedAggregate is the territory=generated branch of
// TerritorySearch. Routes through the canonical
// searchGeneratedTerritory read seam — same underlying data,
// same response envelope as the standalone /generated/search
// endpoint.
func (h *ImagesHandler) generatedAggregate(c *gin.Context) {
	h.searchGeneratedTerritory(c)
}

// searchGeneratedTerritory is the canonical generated-territory
// read seam: query params + service call + 200 OK response envelope.
// Shared by GeneratedSearch, generatedAggregate, and
// allTerritoriesAggregate — godlike/06 SSOT one-canonical-owner-per-fact
// for the generated territory across all three routes.
//
// godlike/07 typed-error contract: invalid limit surfaces as
// 400 BadRequest via errors.Is(ErrInvalidGeneratedSearchLimit);
// any other error surfaces as 500 InternalError.
func (h *ImagesHandler) searchGeneratedTerritory(c *gin.Context) {
	results, err := h.listGeneratedTerritoryResults(c)
	if err != nil {
		if errors.Is(err, ErrInvalidGeneratedSearchLimit) {
			apiutil.BadRequest(c, err.Error())
			return
		}
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, ImageSearchResults{
		Results: results,
		Count:   len(results),
	})
}

// listGeneratedTerritoryResults is the read-only core: parses
// query params, calls the canonical service method, projects to
// the unified ImageSearchResult DTO. Returns the slice (possibly
// empty) and a typed error — the caller decides the response
// envelope. PURE data helper: never writes to the response.
//
// godlike/07 input validation: invalid limit wraps the typed
// sentinel ErrInvalidGeneratedSearchLimit so callers probe via
// errors.Is. Unknown origin is intentionally tolerated (the SQL
// returns 0 rows for `WHERE origin = 'garbage'` — the fail-closed
// surface is the empty-list itself, not a 400 on unknown origin).
func (h *ImagesHandler) listGeneratedTerritoryResults(c *gin.Context) ([]ImageSearchResult, error) {
	origin := c.DefaultQuery("origin", string(domain.ImageOriginGenerated))
	limitStr := c.DefaultQuery("limit", "200")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 0 {
		return nil, fmt.Errorf("invalid limit=%q: %w", limitStr, ErrInvalidGeneratedSearchLimit)
	}

	assets, err := h.service.ListImagesByOrigin(
		c.Request.Context(),
		domain.ImageOrigin(origin),
		limit,
	)
	if err != nil {
		return nil, err
	}

	results := make([]ImageSearchResult, 0, len(assets))
	for i := range assets {
		results = append(results, assetToResult(&assets[i]))
	}
	return results, nil
}
