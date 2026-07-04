// Package images (api/images) — territory_handlers.go holds
// the Step-10 territory-separated handlers.
//
// Per the July 2026 image-restructuring plan, the canonical
// image search API is split by territory:
//
//	GET  /api/images/retrieved/search → SearchAndDownload +
//	                                    multimedia fallback chain
//	GET  /api/images/generated/search → step-9 forward-pointer
//	                                    (returns empty DTO list today;
//	                                     SQLite-backed read-only filter is
//	                                     a follow-up).
//	POST /api/images/generated/generate → GenerateSmartImage (PR-IMAGES-SHIM-REMOVAL, 2026-07-04)
//	                                      (matches the existing /generate
//	                                       payload semantics but mounted
//	                                       under /generated/*).
//	GET  /api/images/generated/styles   → StyleRegistry.List
//	GET  /api/images/search?territory=retrieved|generated|all
//	                                    → Aggregator; default = retrieved
//
// All handlers use the unified ImageSearchResult DTO (see
// types_search.go). Each handler is registered in
// ImagesHandler.RegisterRoutes (impl.go).
package images

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ErrInvalidGeneratedSearchLimit is the typed sentinel returned by
// the data-only listGeneratedTerritoryResults helper when the
// caller supplies a malformed `limit` query parameter. Wrapped via
// fmt.Errorf %w so callers probe via errors.Is — godlike/07
// typed-error contract (no string-sniffing at the call site).
// Maps to 400 BadRequest at the handler layer; other errors map to
// 500 InternalError.
//
// godlike/06 SSOT: package-level sentinels in this repo use
// capitalised `ErrXxx` (per the ErrCompleteJobPathViolation,
// ErrInvalidPayload, ... precedent in internal/domain/remote/);
// the lowercase variant was a round-1 drift caught by code review
// and renamed in this commit.
var ErrInvalidGeneratedSearchLimit = errors.New("invalid generated-search limit (must be a non-negative integer)")

// ── Helpers ─────────────────────────────────────────────────────────────

// assetToResult projects an asset.ImageAsset to a unified
// ImageSearchResult. Shared by RetrievedSearch +
// TerritorySearch(territory=all) handlers.
//
// Direct field access (not getters): asset.ImageAsset is a
// value type exported struct with public fields; no getter
// indirection. StyleID/Author come from MetadataJSON today
// (forward-pointer for direct structure fields).
//
// Accepts a *domain.ImageAsset because the service-layer
// SearchAndDownload / ListImagesBySubject methods return
// pointers; nil is handled by callers (returns empty DTO list).
func assetToResult(a *domain.ImageAsset) ImageSearchResult {
	if a == nil {
		return ImageSearchResult{}
	}
	return ImageSearchResult{
		AssetID:    a.Hash,
		Origin:     string(a.Origin),
		Provider:   string(a.Provider),
		PreviewURL: previewURLForAsset(*a),
		StyleID:    "", // ImageAsset has no Style field today; future Step 9 migration
		License:    a.License,
		Author:     "", // Same: MetadataJSON carries author today
	}
}

// previewURLForAsset picks the best URL for an asset: prefer
// PathRel when set, else fall back to SourceURL.
func previewURLForAsset(a domain.ImageAsset) string {
	if a.PathRel != "" {
		return a.PathRel
	}
	return a.SourceURL
}

// ── 1. GET /api/images/retrieved/search ────────────────────────────────

// RetrievedSearch handles GET /api/images/retrieved/search?q=...&lang=...
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

	asset, err := h.service.SearchAndDownload(
		c.Request.Context(),
		slug, query, query, lang, nil,
	)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	if asset == nil {
		apiutil.OK(c, ImageSearchResults{
			Results: []ImageSearchResult{},
			Count:   0,
		})
		return
	}

	res := assetToResult(asset)
	apiutil.OK(c, ImageSearchResults{
		Results: []ImageSearchResult{res},
		Count:   1,
	})
}

// ── 2. GET /api/images/generated/search ────────────────────────────────

// GeneratedSearch handles GET /api/images/generated/search.
//
// PR-GENERATED-SEARCH-FIX (July 2026, Blocco 1 of
// cut-false-success-first): the underlying SQLite-backed read
// seam is now live. Query parameters:
//
//	?origin=<asset.ImageOrigin>  default "generated" (canonical
//	                              generated-territory territory)
//	?limit=<int>                 default + cap = 200
//	                              (ListImagesByOriginMaxLimit on
//	                              the canonical repo surface)
//
// Returns the matching media_assets rows projected to the unified
// ImageSearchResult DTO (per types_search.go), ordered by
// created_at DESC. Per godlike/07 no-fake-availability, the
// canonical "endpoint alive but feature pending" 200+[] stub
// (Step 9 forward-pointer, pre-PR) is RETIRED — the handler now
// returns real data when rows exist, and an empty list when the
// query matches no rows. The DTO envelope is unchanged so callers
// that coded against the stable contract keep working.
//
// godlike/06 SSOT: this handler is the SOLE production caller of
// *imgservice.Service.ListImagesByOrigin today. Future
// generated-territory read concerns (admin tools, CLI exports)
// MUST route through the canonical service method. The handler
// body delegates to searchGeneratedTerritory (the canonical
// generated-territory read seam) so the response envelope lives
// in exactly one place.
func (h *ImagesHandler) GeneratedSearch(c *gin.Context) {
	h.searchGeneratedTerritory(c)
}

// ── 3. POST /api/images/generated/generate ─────────────────────────────

// GeneratedGenerateRequest is the body for POST
// /api/images/generated/generate. Mirrors the existing
// GenerateImageRequest shape but is mounted under /generated/*
// to emphasise territory separation.
type GeneratedGenerateRequest struct {
	Prompt string   `json:"prompt" binding:"required"`
	Width  int      `json:"width"`
	Height int      `json:"height"`
	Style  string   `json:"style" example:"medievale"`
	Tags   []string `json:"tags"`
}

// GeneratedGenerate handles POST /api/images/generated/generate.
// Equivalent to the legacy POST /api/images/generate — same
// service method, same payload shape. Territory=matters for
// the URL: callers using /generated/* opt into the Step-10
// territory scope explicitly.
func (h *ImagesHandler) GeneratedGenerate(c *gin.Context) {
	var req GeneratedGenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	asset, err := h.service.GenerateSmartImage(
		c.Request.Context(),
		"", // subject
		"", // topic
		req.Style,
		[]string{req.Prompt},
		req.Tags,
		req.Width,
		req.Height,
		generated.CanonicalGoogleSlidesModel,
		false, // skipDrive = false
	)
	if err != nil {
		if errors.Is(err, imgservice.ErrImageGenNotImplemented) {
			c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
				"error":   "image generation endpoint has been removed",
				"message": err.Error(),
			})
			return
		}
		apiutil.InternalError(c, err)
		return
	}

	res := assetToResult(asset)
	apiutil.OK(c, res)
}

// ── 4. GET /api/images/generated/styles ────────────────────────────────

// GeneratedStyles handles GET /api/images/generated/styles.
// Returns the canonical StyleRegistry contents. Each style
// is projected to a StyleInfo DTO so the canonical type's
// internal fields don't leak into the API surface.
func (h *ImagesHandler) GeneratedStyles(c *gin.Context) {
	reg := h.service.StylesRegistry()
	if reg == nil {
		apiutil.OK(c, StylesResponse{Styles: []StyleInfo{}, Count: 0})
		return
	}

	defs := reg.List()
	out := make([]StyleInfo, 0, len(defs))
	for _, d := range defs {
		out = append(out, styleDefToInfo(d))
	}
	apiutil.OK(c, StylesResponse{Styles: out, Count: len(out)})
}

// styleDefToInfo projects a canonical GenerationStyle to a StyleInfo
// DTO for the admin styles endpoint.
//
// Step-1 typed migration (A1, July 2026): the slim 8-field
// StyleDefinition is the source-of-truth for the projection here.
// s.EffectiveSuffix() was retired along with the Description
// fall-back — s.PromptSuffix is the sole resolved suffix. The
// *bool-tri-state IsEnabled() method was replaced by the plain
// bool `Enabled` field (silent flip absent → false; existing
// config pins enabled explicitly so production is transparent).
//
// The StyleID JSON wire format preserves the canonical case
// ("Cinematic" → "Cinematic") to stay byte-compatible with the
// pre-A1 wire-format. A future canonical-case policy can revisit
// this conservatively.
func styleDefToInfo(s domain.GenerationStyle) StyleInfo {
	return StyleInfo{
		StyleID:        s.Name,
		Name:           s.Name,
		Version:        int(s.Version),
		PromptSuffix:   s.PromptSuffix,
		NegativePrompt: s.NegativePrompt,
		DestinationKey: s.DestinationKey,
		Enabled:        s.Enabled,
	}
}

// ── 5. GET /api/images/search?territory=retrieved|generated|all ────────

// TerritorySearch handles GET /api/images/search with a
// territory query param. Replaces the pre-Step-10 /search
// handler — callers that used /search?q=X without ?territory
// default to territory=retrieved (same behaviour).
//
// territory=retrieved → delegates to SearchAndDownload (single result).
// territory=generated → empty list (Step-9 forward-pointer, see
//
//	GeneratedSearch comment for context).
//
// territory=all      → fan-out to generated + retrieved and
//
//	merge in deterministic order: retrieved
//	first (canonical search behaviour), then
//	generated (forward-pointer empty list today
//	so behaviour == retrieved in practice).
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

	asset, err := h.service.SearchAndDownload(
		c.Request.Context(),
		slug, query, query, lang, nil,
	)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	out := ImageSearchResults{Results: []ImageSearchResult{}, Count: 0}
	if asset != nil {
		out.Results = append(out.Results, assetToResult(asset))
		out.Count = 1
	}
	apiutil.OK(c, out)
}

// generatedAggregate is the territory=generated branch of
// TerritorySearch. PR-GENERATED-SEARCH-FIX (July 2026): now wires
// through the canonical ListImagesByOrigin read seam so the
// false-success class retired on /api/images/generated/search is
// ALSO retired on /api/images/search?territory=generated — same
// underlying data, same response envelope.
func (h *ImagesHandler) generatedAggregate(c *gin.Context) {
	h.searchGeneratedTerritory(c)
}

// allTerritoriesAggregate fans out across both wired
// territories and merges results in canonical order.
//
// PR-GENERATED-SEARCH-FIX (July 2026, round 2): the generated
// branch no longer returns 200+[] — it routes through the
// canonical ListImagesByOrigin read seam so the user-facing
// aggregator (territory=all) sees the same generated-territory
// rows as the canonical /api/images/generated/search endpoint.
// Errors from the data helper are surfaced to the caller as
// 400/500 (typed) and short-circuit the merge — the round-1
// helper wrote responses inline which caused Gin to log
// "write response after body already written" when this
// aggregator's own apiutil.OK fired afterwards.
func (h *ImagesHandler) allTerritoriesAggregate(c *gin.Context) {
	results := make([]ImageSearchResult, 0, 8)

	// Retrieved first — has the canonical query-driven path.
	if c.Query("q") != "" {
		lang := c.DefaultQuery("lang", "en")
		slug := textutil.Slugify(c.Query("q"))
		asset, err := h.service.SearchAndDownload(
			c.Request.Context(),
			slug, c.Query("q"), c.Query("q"), lang, nil,
		)
		if err == nil && asset != nil {
			results = append(results, assetToResult(asset))
		}
	}

	// Generated second — canonical read seam (PR-GENERATED-SEARCH-FIX).
	// Appends the real generated-territory rows (capped at 200 per the
	// canonical hard cap on the repo surface). Order is canonical:
	// retrieved first, generated second. Error from the data helper
	// short-circuits the merge and writes the typed 400/500 envelope
	// (no double-write with this aggregator's own apiutil.OK).
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

// searchGeneratedTerritory is the canonical generated-territory
// read seam: query params + service call + 200 OK response envelope.
// Extracted from GeneratedSearch so generatedAggregate (the
// territory=generated branch of TerritorySearch) can share the
// exact same read path — godlike/06 SSOT one-canonical-owner-per-fact
// for the generated territory across both routes.
//
// godlike/07 no-fake-availability: this helper RETIRES the Step 9
// forward-pointer "endpoint alive but feature pending" 200+[] stub
// on ALL three surfaces (GeneratedSearch + generatedAggregate +
// allTerritoriesAggregate). Every generated-territory read seam
// now sees the real canonical ListImagesByOrigin data.
//
// godlike/07 typed-error contract: invalid limit surfaces as
// 400 BadRequest via errors.Is(ErrInvalidGeneratedSearchLimit);
// any other error surfaces as 500 InternalError. The data-only
// helper (listGeneratedTerritoryResults) does NOT write to the
// response — callers own the response envelope so concurrent
// handlers (allTerritoriesAggregate) don't double-write.
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
// envelope. PURE data helper: never writes to the response
// (that contract is what prevents the Gin double-write bug fixed
// in PR-GENERATED-SEARCH-FIX round 2).
//
// godlike/07 input validation: invalid limit wraps the typed
// sentinel ErrInvalidGeneratedSearchLimit so callers probe via
// errors.Is (canonical godlike/07 typed-error contract — no
// string-sniffing). Unknown origin is intentionally tolerated
// (the SQL returns 0 rows for `WHERE origin = 'garbage'` so the
// response is the canonical empty-list shape — the godlike/07
// fail-closed surface is the empty-list itself, not a 400 on
// unknown origin). The forward-pointer to a stricter
// 400-on-unknown-origin surface is in architecture/issues.yaml.
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
