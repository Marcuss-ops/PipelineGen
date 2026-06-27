// Package search (api/assets/search) — handler.go is the thin HTTP
// transport for the cross-provider search surface.
//
// Wave 21 PR 10 (June 2026, CUTOVER): the cross-provider Service is
// gone (git rm'd alongside PR-SEARCH-LEGACY-CROSSPROVIDER record in
// architecture/deprecations.yaml). The handler now delegates directly
// to the canonical search.Aggregator. Response envelope is the
// canonical search.Result projection (items, next_cursor, partial,
// provider_errors) — clients recover the full hit shape per-item
// (asset_id, score, source, source_ref, preview_url, etc.).
//
// X-Deprecation header is set on every response so dashboards and
// migration tooling can spot callers still using the legacy shape.
// The route /api/assets/search is preserved during the migration
// window; a future PR will collapse it into /api/clips/search/advanced
// per the Wave 21 cutover spec.
package search

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// Handler is the thin HTTP transport for canonical Aggregator-backed
// cross-provider search. Wire shape: GET /api/assets/search?q=…&type=…&limit=20
// (legacy cross-provider GET preserved at the route level).
type Handler struct {
	aggreg *search.Aggregator
	log    *zap.Logger
}

// NewHandler constructs the canonical Aggregator-backed SearchHandler.
// Composition root (internal/app/module_media.go::WireAssets) injects
// the *search.Aggregator populated by BuildSearchBackends + NewAggregator
// (search_backends.go).
func NewHandler(aggreg *search.Aggregator, log *zap.Logger) *Handler {
	return &Handler{aggreg: aggreg, log: log}
}

// RegisterRoutes registers search routes under the given group. The
// /api/assets/search route is mounted by the assetsapi module's
// RegisterRoutes after WireAssets wires the searchHandler into its
// dependencies.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/search", h.Search)
}

// searchRequest is the canonical query-string DTO. Mode is not exposed
// at this endpoint — cross-provider search is always ALL-CAP (no ANN
// toggle) so we leave Mode="hybrid" for the semantic backend only.
// Cursor is the opaque pagination token emitted by the Aggregator.
//
// Field semantics per Wave 21 PR 9 dedup policy:
//   - q        — Text field for canonical search.Query
//   - type     — MediaType filter (video|image|audio|music). Empty=all.
//   - limit    — Page size. Aggregator clamps to DefaultLimit / MaxLimit.
//   - cursor   — Opaque base64-JSON pagination token (PR 9 cursor codec).
type searchRequest struct {
	Q      string `form:"q" binding:"required"`
	Type   string `form:"type"`
	Limit  int    `form:"limit,default=20"`
	Sort   string `form:"sort"` // legacy: tolerated; Aggregator ignores
	Cursor string `form:"cursor"`
}

// Search is the HTTP handler for GET /api/assets/search.
//
// Status codes:
//   200 — OK, canonical Result envelope in body
//   400 — invalid query (missing q, malformed cursor)
//   422 — invalid cursor (semantic error from Aggregator)
//   500 — internal error from Aggregator fanout (providerErrors populated)
//   503 — search aggregator not wired
func (h *Handler) Search(c *gin.Context) {
	if h.aggreg == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "search aggregator not wired")
		return
	}

	var req searchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid query: "+err.Error())
		return
	}
	q := strings.TrimSpace(req.Q)
	if q == "" {
		apiutil.BadRequest(c, "query parameter 'q' is required")
		return
	}
	limit := defaults.Int(req.Limit, 20)

	// MediaType → MediaTypes single-element slice (only when set).
	// Empty / "all" → Aggregator defaults to all 4 capabilities.
	var mediaTypes []string
	if req.Type != "" && req.Type != "all" {
		mediaTypes = []string{req.Type}
	}

	res, err := h.aggreg.Search(c.Request.Context(), search.Query{
		Text:       q,
		Limit:      limit,
		MediaTypes: mediaTypes,
		Cursor:     req.Cursor,
	})
	if err != nil {
		switch err {
		case search.ErrInvalidCursor:
			apiutil.Error(c, http.StatusUnprocessableEntity, "invalid cursor")
		default:
			h.log.Error("search failed", zap.Error(err))
			apiutil.InternalError(c, err)
		}
		return
	}

	// X-Deprecation: true — Wave 21 PR 10 cutover marker. The legacy
	// ProviderResult/map-encoded response envelope has been replaced
	// by the canonical Result envelope; clients should migrate. See
	// deprecation record PR-SEARCH-LEGACY-CROSSPROVIDER in
	// architecture/deprecations.yaml.
	c.Header("X-Deprecation", "true")
	c.Header("X-Deprecation-Migration", "aggregator")

	c.JSON(http.StatusOK, gin.H{
		"items":           res.Items,
		"next_cursor":     res.NextCursor,
		"partial":         res.Partial,
		"provider_errors": res.ProviderErrors,
	})
}
