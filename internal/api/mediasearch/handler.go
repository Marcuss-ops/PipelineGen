// Package mediasearch (api) — handler.go is the thin HTTP transport
// for the QDRANT-004 single private media-search API.
//
//	POST /internal/v1/media/search
//
// All business logic lives in internal/application/mediasearch.
// This file is responsible for: bind the JSON body, extract the
// workspace scope from the auth context (never from the request
// body), call the orchestrator, and render a JSON response with no
// internal-server fields exposed.
//
// Do NOT add anything here that requires `database/sql`, an HTTP
// client, os/exec, or any other infra import — AGENTS.md Pattern
// 8 ("API package: thin transport only") applies.
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
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// Searcher is the narrow contract the handler depends on. The
// production orchestrator (*mediasearch.Service) satisfies it;
// tests pass a stub. Keeping the dependency narrow lets the
// handler stay free of VectorSearchPort / MediaReadRepository
// imports (per AGENTS.md Pattern 8).
type Searcher interface {
	Search(ctx context.Context, req mediasearch.MediaSearchRequest) (*mediasearch.MediaSearchResponse, error)
}

// Handler is the thin HTTP transport for MediaSearchService.
type Handler struct {
	svc Searcher
	log *zap.Logger
}

// NewHandler creates a MediaSearchHandler.
func NewHandler(svc Searcher, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
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
//	500 — internal error from embedder / vector store / hydration.
func (h *Handler) Search(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable,
			"media search service not wired")
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

	serviceReq := mediasearch.MediaSearchRequest{
		Query:     strings.TrimSpace(req.Query),
		Mode:      mode,
		Limit:     defaults.Int(req.Limit, mediasearch.DefaultLimit),
		MinScore:  req.MinScore,
		Workspace: workspace,
		Filters: mediasearch.MediaSearchFilter{
			Source:        strings.TrimSpace(req.Filters.Source),
			MediaType:     strings.TrimSpace(req.Filters.MediaType),
			Category:      strings.TrimSpace(req.Filters.Category),
			Language:      strings.TrimSpace(req.Filters.Language),
			Tags:          req.Filters.Tags,
			DurationMsMin: req.Filters.DurationMsMin,
		},
	}

	resp, err := h.svc.Search(c.Request.Context(), serviceReq)
	if err != nil {
		switch {
		case errors.Is(err, mediasearch.ErrMissingWorkspace):
			// Defensive: the handler enforces workspace already, but if
			// Service ever rejects a non-default workspace anyway we land here.
			apiutil.Error(c, http.StatusForbidden, "workspace_id required in context")
		default:
			h.safeError("mediasearch.Search failed",
				zap.String("workspace", workspace.WorkspaceID),
				zap.Error(err))
			apiutil.InternalError(c, err)
		}
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ── Helpers ─────────────────────────────────────────────────────────────

// extractWorkspace pulls the workspace from the gin context set by
// middleware.WorkspaceScopeMiddleware. If the scope is empty or
// explicitly "default", the handler refuses with 403 (worker
// principals cannot bypass through the body).
func (h *Handler) extractWorkspace(c *gin.Context) (mediasearch.WorkspaceContext, bool) {
	scope := middleware.ScopeFromContext(c)
	if scope.WorkspaceID == "" || scope.WorkspaceID == "default" {
		apiutil.Error(c, http.StatusForbidden,
			"workspace_id is required (set X-Workspace-ID header for admin, or authenticate as a tenant principal)")
		return mediasearch.WorkspaceContext{}, false
	}
	// Best-effort audit metadata; we don't fail the request if
	// is_admin isn't present — auth middleware already enforced it.
	isAdmin, _ := c.Get("is_admin")
	principalID, _ := c.Get("principal_id")
	return mediasearch.WorkspaceContext{
		WorkspaceID: scope.WorkspaceID,
		ProjectID:   scope.ProjectID,
		PrincipalID: strings.TrimSpace(toString(principalID)),
		IsAdmin:     toBool(isAdmin),
	}, true
}

// parseMode accepts "", "ann", "hybrid" — anything else is a 400.
// Empty defaults to hybrid (which the service also defaults to;
// pin at the handler so /search callers see the same default
// whether they pass "" or omit the field).
func (h *Handler) parseMode(c *gin.Context, raw string) (mediasearch.SearchMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "hybrid":
		return mediasearch.SearchModeHybrid, true
	case "ann":
		return mediasearch.SearchModeANN, true
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
