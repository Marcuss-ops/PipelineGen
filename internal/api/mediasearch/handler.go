// Package mediasearch (api) — handler.go is the thin HTTP transport
// for the QDRANT-004 single private media-search API.
//
//	POST /internal/v1/media/search
//
// All business logic lives in internal/application/mediasearch. This
// file is responsible for: bind the JSON body, extract the workspace
// scope from the auth context (never from the request body), call the
// canonical search.Aggregator (Wave 21), translate the canonical
// Result into the legacy MediaSearchResponse envelope, and render a
// JSON response with no internal-server fields exposed.
//
// Wave 21 PR 10 (June 2026): Search() now consumes search.Aggregator
// directly. The Aggregator's semanticSearchBackend internally calls
// mediasearch.Service (kept alive as the semantic path's backend
// engine); per-source providers and the local catalog search flow
// alongside. The wire-equivalence test is byte-stable for fields
// present on the canonical search.Candidate; fields NOT in Candidate
// (matched_channels, reason, tags, language, duration_ms, width,
// height, request_id) are deliberately dropped in this CUTOVER and
// flagged with the X-Deprecation header so clients know to migrate.
//
// Do NOT add anything here that requires `database/sql`, an HTTP
// client, os/exec, or any other infra import — AGENTS.md Pattern 8
// ("API package: thin transport only") applies.
//
// Wired at internal/app/registry.go → wiring.MediasearchHandler →
// internal/api/routes.go::Setup() → internalGroup (/internal/v1)
// with WorkerAuth middleware. The route is:
//
//	POST /internal/v1/media/search
//
// NOT mounted under /api. The handler receives an *gin.RouterGroup
// already scoped to /internal/v1/media via routes.go::Setup().
package mediasearch

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	mediasearchapp "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// AggregatorSearcher is the narrow contract the handler depends on
// after PR 10. Production wiring injects *search.Aggregator (cast as
// the interface to keep the dependency narrow). Tests pass a stub.
// Keeping the dependency narrow lets the handler stay free of the
// underlying types (VectorSearchPort / MediaReadRepository /
// CrossSearchResponse) — per AGENTS.md Pattern 8.
type AggregatorSearcher interface {
	Search(ctx context.Context, q search.Query) (*search.Result, error)
}

// WireParams bundles all dependencies the handler needs. PR 10 splits
// it from the previous struct-of-fields shape to make the
// constructor-injection contract explicit (Wave 15 typed-port rule).
type WireParams struct {
	Aggregator AggregatorSearcher
	Log        *zap.Logger
}

// Handler is the thin HTTP transport for the canonical MediaSearch API.
type Handler struct {
	aggreg AggregatorSearcher
	log    *zap.Logger
}

// NewHandler creates a MediaSearchHandler wired to the canonical
// search.Aggregator (Wave 21). The legacy
// *mediasearchapp.Service direct-construction path is
// CompositionRoot-internal only now (semantic backend uses it).
func NewHandler(p WireParams) *Handler {
	return &Handler{aggreg: p.Aggregator, log: p.Log}
}

// safeError logs an error through h.log without panicking if the
// logger is nil (the composition root may legitimately pass nil in
// unit tests or in a stripped-down bootstrap mode).
func (h *Handler) safeError(msg string, fields ...zap.Field) {
	if h.log == nil {
		return
	}
	h.log.Error(msg, fields...)
}

// RegisterRoutes mounts the route under /internal/v1/media/search.
// The route prefix is the responsibility of the caller (see
// internal/app/registry.go). Authorization and workspace-scope
// middleware MUST be applied upstream of this handler: the handler
// treats `ScopeFromContext(c) == default` as a hard 403.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/search", h.Search)
}

// ── DTO types ───────────────────────────────────────────────────────────

// searchRequest is the JSON body. Note: workspace_id, asset_id, and
// any other auth-context fields are deliberately absent from this
// struct — they MUST come from the auth context, never the body
// (AGENTS.md Hard Rule: never trust client-supplied workspace).
//
// QDRANT-004 §advanced filters: filters.Source/MediaType/Category/
// Language map 1:1 to Qdrant must-predicates (already wired in the
// service). Tags is AND-semantics; DurationMsMin is enforced
// post-hydration (canonical duration comes from SQLite, not the
// vector payload — that's why it's not a vector-store filter).
type searchRequest struct {
	Query    string              `json:"query" binding:"required"`
	Mode     string              `json:"mode,omitempty"` // "ann" or "hybrid"
	Limit    int                 `json:"limit,omitempty"`
	MinScore float64             `json:"min_score,omitempty"`
	Filters  searchRequestFilter `json:"filters,omitempty"`
}

type searchRequestFilter struct {
	Source        string   `json:"source,omitempty"`
	MediaType     string   `json:"media_type,omitempty"`
	Category      string   `json:"category,omitempty"`
	Language      string   `json:"language,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	DurationMsMin int      `json:"duration_ms_min,omitempty"`
}

// ── Handler ─────────────────────────────────────────────────────────────

// Search is the HTTP handler for POST /internal/v1/media/search.
//
// Status codes:
//
//	200 — OK, results in body (may be empty if no hit survives filters).
//	400 — malformed JSON, missing query, or invalid mode.
//	401 — missing/invalid auth token (handled upstream by middleware.Auth).
//	403 — workspace not provided in auth context, or worker tried to
//	      pick a non-default workspace (handled upstream by
//	      middleware.WorkspaceScopeMiddleware).
//	422 — invalid cursor (semantic error from Aggregator).
//	500 — internal error from embedder / vector store / hydration.
//
// Wire shape (Wave 21 PR 10): MediasearchResponse is rebuilt from the
// canonical search.Result via the package-local cutover.go helper.
// The partial-preferred model flips OK=false when (Result.Partial == true
// AND len(Result.Items) == 0); OK=true otherwise.
func (h *Handler) Search(c *gin.Context) {
	if h.aggreg == nil {
		apiutil.Error(c, http.StatusServiceUnavailable,
			"media search aggregator not wired")
		return
	}

	workspace, ok := h.extractWorkspace(c)
	if !ok {
		return // 403 already rendered
	}

	req, ok := apiutil.BindJSON[searchRequest](c)
	if !ok {
		return // 400 already rendered
	}

	mode, ok := h.parseMode(c, req.Mode)
	if !ok {
		return
	}

	limit := defaults.Int(req.Limit, mediasearchapp.DefaultLimit)
	q := searchQueryFromRequest(req, mode, limit)

	res, err := h.aggreg.Search(c.Request.Context(), q)
	if err != nil {
		switch {
		case errors.Is(err, search.ErrInvalidCursor):
			apiutil.Error(c, http.StatusUnprocessableEntity, "invalid cursor")
		case errors.Is(err, mediasearchapp.ErrMissingWorkspace):
			apiutil.Error(c, http.StatusForbidden, "workspace_id required in context")
		default:
			h.safeError("mediasearch.Search failed",
				zap.String("workspace", workspace.WorkspaceID),
				zap.Error(err))
			apiutil.InternalError(c, err)
		}
		return
	}

	// Build the legacy MediaSearchRequest envelope so cutover.go's
	// translator has all the metadata it needs (Query, Mode, Workspace).
	legacyReq := mediasearchapp.MediaSearchRequest{
		Query:     q.Text,
		Mode:      mode,
		Limit:     limit,
		MinScore:  req.MinScore,
		Filters:   q.Filters, // alias-mediated; identity at type level
		Workspace: workspace,
	}
	resp := resultToMediaSearchResponse(res, legacyReq)

	// X-Deprecation: true — Wave 21 PR 10 cutover marker. The legacy
	// route shape (more fields, complex hydration) has been replaced
	// by the canonical Aggregator pipeline; the migration window is
	// in effect. Clients should consume the new field set. See
	// deprecation record PR-SEARCH-LEGACY-MEDIASEARCH in
	// architecture/deprecations.yaml.
	c.Header("X-Deprecation", "true")
	c.Header("X-Deprecation-Migration", "aggregator")

	c.JSON(http.StatusOK, resp)
}

// ── Helpers ─────────────────────────────────────────────────────────────

// extractWorkspace pulls the workspace from the gin context set by
// middleware.WorkspaceScopeMiddleware. If the scope is empty or
// explicitly "default", the handler refuses with 403 (worker
// principals cannot bypass through the body).
func (h *Handler) extractWorkspace(c *gin.Context) (mediasearchapp.WorkspaceContext, bool) {
	scope := middleware.ScopeFromContext(c)
	if scope.WorkspaceID == "" || scope.WorkspaceID == "default" {
		apiutil.Error(c, http.StatusForbidden,
			"workspace_id is required (set X-Workspace-ID header for admin, or authenticate as a tenant principal)")
		return mediasearchapp.WorkspaceContext{}, false
	}
	isAdmin, _ := c.Get("is_admin")
	principalID, _ := c.Get("principal_id")
	return mediasearchapp.WorkspaceContext{
		WorkspaceID: scope.WorkspaceID,
		ProjectID:   scope.ProjectID,
		PrincipalID: strings.TrimSpace(toString(principalID)),
		IsAdmin:     toBool(isAdmin),
	}, true
}

// parseMode accepts "", "ann", "hybrid" — anything else is a 400.
func (h *Handler) parseMode(c *gin.Context, raw string) (mediasearchapp.SearchMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "hybrid":
		return mediasearchapp.SearchModeHybrid, true
	case "ann":
		return mediasearchapp.SearchModeANN, true
	default:
		apiutil.Error(c, http.StatusBadRequest,
			"mode must be one of: hybrid, ann")
		return "", false
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}
