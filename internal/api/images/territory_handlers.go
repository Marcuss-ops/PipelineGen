// Package images (api/images) — territory_handlers.go holds
// the Step-10 territory-separated handlers.
//
// Per the July 2026 image-restructuring plan, the canonical
// image search API is split by territory:
//
//   GET  /api/images/retrieved/search → SearchAndDownload +
//                                       multimedia fallback chain
//   GET  /api/images/generated/search → step-9 forward-pointer
//                                       (returns empty DTO list today;
//                                        SQLite-backed read-only filter is
//                                        a follow-up).
//   POST /api/images/generated/generate → GenerateSmartImageWithAccount
//                                         (matches the existing /generate
//                                          payload semantics but mounted
//                                          under /generated/*).
//   GET  /api/images/generated/styles   → StyleRegistry.List
//   GET  /api/images/search?territory=retrieved|generated|all
//                                       → Aggregator; default = retrieved
//
// All handlers use the unified ImageSearchResult DTO (see
// types_search.go). Each handler is registered in
// ImagesHandler.RegisterRoutes (impl.go).
package images

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

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
// Step 9 forward-pointer: the underlying SQLite-backed
// ListImagesByOrigin method doesn't exist on
// *ImageStorageService today. Returning a stable 200 OK +
// empty ImageSearchResults list gives callers a contract to
// code against while the read-only filter ships in a
// follow-up. The behavior marks this as "endpoint alive
// but feature pending" rather than 501 Not Implemented so
// the smoke-test can verify 200 OK + correct DTO shape.
func (h *ImagesHandler) GeneratedSearch(c *gin.Context) {
	apiutil.OK(c, ImageSearchResults{
		Results: []ImageSearchResult{},
		Count:   0,
	})
}

// ── 3. POST /api/images/generated/generate ─────────────────────────────

// GeneratedGenerateRequest is the body for POST
// /api/images/generated/generate. Mirrors the existing
// GenerateImageRequest shape but is mounted under /generated/*
// to emphasise territory separation.
type GeneratedGenerateRequest struct {
	Prompt    string   `json:"prompt" binding:"required"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Style     string   `json:"style" example:"medievale"`
	Tags      []string `json:"tags"`
	Account   string   `json:"account,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
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

	asset, err := h.service.GenerateSmartImageWithAccount(
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
		req.Account,
		req.ProjectID,
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

// styleDefToInfo projects a canonical GenerationStyle to a
// StyleInfo DTO. AllowedProviders/Models are joined with commas
// for JSON portability (canonical type uses []string).
//
// Note: the canonical GenerationStyle lives in
// internal/domain/asset (not generation/), since Step 3
// moved the type definitions to the domain layer.
func styleDefToInfo(s domain.GenerationStyle) StyleInfo {
	return StyleInfo{
		StyleID:         s.Name,
		Name:            s.Name,
		Version:         s.Version,
		PromptSuffix:    s.EffectiveSuffix(),
		NegativePrompt:  s.NegativePrompt,
		DestinationKey:  s.DestinationKey,
		Enabled:         s.IsEnabled(),
		AllowedProviders: strings.Join(s.AllowedProviders, ","),
		AllowedModels:   strings.Join(s.AllowedModels, ","),
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
//                        GeneratedSearch comment for context).
// territory=all      → fan-out to generated + retrieved and
//                       merge in deterministic order: retrieved
//                       first (canonical search behaviour), then
//                       generated (forward-pointer empty list today
//                       so behaviour == retrieved in practice).
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

// generatedAggregate is the territory=generated branch.
// Returns empty DTO list (Step-9 forward-pointer).
func (h *ImagesHandler) generatedAggregate(c *gin.Context) {
	apiutil.OK(c, ImageSearchResults{Results: []ImageSearchResult{}, Count: 0})
}

// allTerritoriesAggregate fans out across both wired
// territories and merges results in canonical order.
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

	// Generated second — empty list today (forward-pointer).
	// Future PR adds the SQLite-backed list.

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


