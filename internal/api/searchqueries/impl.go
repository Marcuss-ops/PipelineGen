// Package searchqueries provides HTTP handlers for managing scheduled topic searches.
package searchqueries

import (
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/api"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	sqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
)

// SearchqueriesHandler handles CRUD operations for search_queries (scheduled YouTube topic searches).
type SearchqueriesHandler struct {
	repo *sqlite.SearchQueriesRepository
	log  *zap.Logger
}

// NewHandler creates a new search queries API handler.
func NewSearchqueriesHandler(repo *sqlite.SearchQueriesRepository, log *zap.Logger) *SearchqueriesHandler {
	return &SearchqueriesHandler{repo: repo, log: log}
}

// RegisterRoutes registers the search queries CRUD routes under the given router group.
func (h *SearchqueriesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("", h.ListAll)
	r.GET("/active", h.ListActive)
	r.GET("/:id", h.GetByID)
	r.POST("", h.Upsert)
	r.DELETE("/:id", h.Delete)
	r.GET("/:id/results", h.ListResults)
}

// ListAll returns all search queries, active first.
func (h *SearchqueriesHandler) ListAll(c *gin.Context) {
	queries, err := h.repo.ListAll(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list search queries", zap.Error(err))
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"queries": queries})
}

// ListActive returns all active search queries.
func (h *SearchqueriesHandler) ListActive(c *gin.Context) {
	queries, err := h.repo.ListActive(c.Request.Context())
	if err != nil {
		h.log.Error("failed to list active search queries", zap.Error(err))
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"queries": queries})
}

// GetByID returns a single search query by ID.
func (h *SearchqueriesHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		api.BadRequest(c, "id parameter is required")
		return
	}

	q, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to get search query", zap.String("id", id), zap.Error(err))
		api.NotFound(c, "search query not found")
		return
	}
	api.OK(c, q)
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
func (h *SearchqueriesHandler) Upsert(c *gin.Context) {
	var req SearchQueryUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, err.Error())
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

	if err := h.repo.Upsert(c.Request.Context(), q); err != nil {
		h.log.Error("failed to upsert search query", zap.Error(err))
		api.InternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"query": q,
	})
}

// Delete removes a search query by ID.
func (h *SearchqueriesHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		api.BadRequest(c, "id parameter is required")
		return
	}

	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		h.log.Error("failed to delete search query", zap.String("id", id), zap.Error(err))
		api.InternalError(c, err)
		return
	}

	api.OK(c, gin.H{"ok": true, "deleted_id": id})
}

// ListResults returns all processed results for a search query.
func (h *SearchqueriesHandler) ListResults(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		api.BadRequest(c, "id parameter is required")
		return
	}

	results, err := h.repo.ListResultsByQuery(c.Request.Context(), id)
	if err != nil {
		h.log.Error("failed to list search query results", zap.String("id", id), zap.Error(err))
		api.InternalError(c, err)
		return
	}
	api.OK(c, gin.H{"results": results})
}
