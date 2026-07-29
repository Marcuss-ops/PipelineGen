// Package assets (api/assets) — handler_searchqueries.go holds the
// SearchQueriesHandler CRUD transport for scheduled YouTube topic
// searches. Wave 14 close (June 2026): this receiver was absorbed
// from the standalone internal/api/searchqueries/ package.
//
// Wave 14 problem #3 close-out (June 2026): all orchestration moved
// into internal/application/assets/searchqueries/usecase.go. This
// handler is now thin transport (Pattern 8 from AGENTS.md): bind
// → delegate → render. Earlier Wave-4 versions embedded the
// *assets.SearchQueriesRepository directly; that coupling is gone.
//
// Routes mounted on the `/search-queries` prefix module →
// /api/search-queries/{, /active, /:id, /:id/results}.
package assets

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/searchqueries"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// SearchQueriesHandler handles CRUD operations for search_queries
// (scheduled YouTube topic searches). Pure transport; all
// orchestration lives in *searchqueries.UseCase.
type SearchQueriesHandler struct {
	useCase *searchqueries.UseCase
	log     *zap.Logger
}

// NewSearchQueriesHandler creates a new search queries API handler.
// Repository wiring happens at the composition root (registry.go),
// which builds the *searchqueries.UseCase from the concrete
// *assets.SearchQueriesRepository and injects it here.
func NewSearchQueriesHandler(useCase *searchqueries.UseCase, log *zap.Logger) *SearchQueriesHandler {
	return &SearchQueriesHandler{useCase: useCase, log: log}
}

// RegisterRoutes registers the search queries CRUD routes under the
// given router group. Callers must mount the module at `/search-queries`
// so the resulting URLs are /api/search-queries/{,/active,/...
func (h *SearchQueriesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("", h.ListAll)
	r.GET("/active", h.ListActive)
	r.GET("/:id", h.GetByID)
	r.POST("", h.Upsert)
	r.DELETE("/:id", h.Delete)
	r.GET("/:id/results", h.ListResults)
}

// ListAll returns all search queries, active first.
func (h *SearchQueriesHandler) ListAll(c *gin.Context) {
	queries, err := h.useCase.ListAll(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list search queries", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"queries": queries})
}

// ListActive returns all active search queries.
func (h *SearchQueriesHandler) ListActive(c *gin.Context) {
	queries, err := h.useCase.ListActive(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list active search queries", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, gin.H{"queries": queries})
}

// GetByID returns a single search query by ID.
func (h *SearchQueriesHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiutil.BadRequest(c, "id parameter is required")
		return
	}

	q, err := h.useCase.GetByID(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to get search query", zap.String("id", id), zap.Error(err))
		apiutil.NotFound(c, "search query not found")
		return
	}
	apiutil.OK(c, q)
}

// SearchQueryUpsertRequest is the JSON body for creating or updating a search query.
type SearchQueryUpsertRequest struct {
	ID                   string `json:"id" binding:"required"`
	Query                string `json:"query" binding:"required"`
	Category             string `json:"category" binding:"required"`
	DriveFolderID        string `json:"drive_folder_id,omitempty"`
	MinScore             int    `json:"min_score,omitempty"`
	MaxResults           int    `json:"max_results,omitempty"`
	CheckInterval        string `json:"check_interval,omitempty"`
	LastRunAt            string `json:"last_run_at,omitempty"`
	LastVideoPublishedAt string `json:"last_video_published_at,omitempty"`
	IsActive             int    `json:"is_active,omitempty"`
}

// Upsert creates or updates a search query.
func (h *SearchQueriesHandler) Upsert(c *gin.Context) {
	var req SearchQueryUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	q := &asset.SearchQuery{
		ID:                   req.ID,
		Query:                req.Query,
		Category:             req.Category,
		DriveFolderID:        req.DriveFolderID,
		MinScore:             req.MinScore,
		MaxResults:           req.MaxResults,
		CheckInterval:        req.CheckInterval,
		LastRunAt:            req.LastRunAt,
		LastVideoPublishedAt: req.LastVideoPublishedAt,
		IsActive:             req.IsActive,
	}

	if err := h.useCase.Upsert(c.Request.Context(), q); err != nil {
		h.log.Error("failed to upsert search query", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"query": q,
	})
}

// Delete removes a search query by ID.
func (h *SearchQueriesHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiutil.BadRequest(c, "id parameter is required")
		return
	}

	if err := h.useCase.Delete(c.Request.Context(), id); err != nil {
		h.log.Error("failed to delete search query", zap.String("id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "deleted_id": id})
}

// ListResults returns all processed results for a search query.
func (h *SearchQueriesHandler) ListResults(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiutil.BadRequest(c, "id parameter is required")
		return
	}

	results, err := h.useCase.ListResults(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to list search query results", zap.String("id", id), zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{"results": results})
}
