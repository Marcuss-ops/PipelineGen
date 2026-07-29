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
//
// PR-IMG-LEGACY-3 (IMAGES-LEGACY-CLEANUP-2026-07-06 wave, 2026-07-06,
// CONTRACT phase, deadline 2026-08-08): the route intrinsically
// returns only the generated territory by URL contract —
// /api/images/generated/search is canonically a generated-territory
// read seam (path /generated/ = territory = generated). The
// vestigial `?origin=` query-parameter affordance read in
// listGeneratedTerritoryResults is RETIRED (the territory is no
// longer a caller-supplied knob). The service call now hardcodes
// domain.ImageOriginGenerated regardless of query params; any
// `?origin=X` value is silently coerced to generated per
// godlike/06 SSOT (the route is the territory, not the query param).
// Cross-domain territory switching routes via the canonical
// /api/images/aggregate?territory=all or /api/images/aggregate?territory=retrieved
// surfaces, NOT via ?origin=.
package images

import (
	"errors"
	"fmt"
	"strconv"

	domain "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// domain import retained: the canonical ImageOriginGenerated
// constant lives in internal/kernel/asset/image_taxonomy.go.

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
// read seam is live. PR-IMG-LEGACY-3 (2026-07-06, CONTRACT phase):
// the `?origin=` query parameter is RETIRED — the route's territory
// is fixed by URL contract (path /generated/ = ImageOriginGenerated).
// The vestigial parameter still being accepted at the gin layer
// (silently ignored) is the explicit godlike/06 SSOT choice: the
// route, not the caller, owns the territory. Query parameters:
//
//	?limit=<int>                 default + cap = 200
//
// (No ?origin= — retired; territory is fixed at ImageOriginGenerated.)
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
// errors.Is. PR-IMG-LEGACY-3 (2026-07-06, CONTRACT phase): the
// territory is fixed at ImageOriginGenerated (route contract);
// any caller-supplied ?origin= is silently coerced since the
// route is the territory, not the query param (godlike/06 one
// canonical owner per fact).
func (h *ImagesHandler) listGeneratedTerritoryResults(c *gin.Context) ([]ImageSearchResult, error) {
	limitStr := c.DefaultQuery("limit", "200")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 0 {
		return nil, fmt.Errorf("invalid limit=%q: %w", limitStr, ErrInvalidGeneratedSearchLimit)
	}

	// PR-IMG-LEGACY-3: territory is hardcoded to ImageOriginGenerated.
	// The route contract is /generated/search = generated territory;
	// any ?origin=X caller-supplied value is silently discarded
	// (godlike/06 SSOT: route, not query param, owns territory).
	assets, err := h.service.ListImagesByOrigin(
		c.Request.Context(),
		domain.ImageOriginGenerated,
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
