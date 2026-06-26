// Package search provides HTTP transport for cross-provider search operations.
// All business logic is delegated to application/assets/search.Service.
//
// Semantic search and clip recommendation have been consolidated into
// internal/application/mediasearch and served at /internal/v1/media/search.
package search

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// Handler is the thin HTTP transport for search operations.
type Handler struct {
	svc *appsearch.Service
	log *zap.Logger
}

// NewHandler creates a SearchHandler.
func NewHandler(svc *appsearch.Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes registers search routes under the given group.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/search", h.Search)
}

// ── Search (GET /search) ──────────────────────────────────────────

type searchRequest struct {
	Q     string `form:"q" binding:"required"`
	Type  string `form:"type"`
	Limit int    `form:"limit,default=20"`
	Sort  string `form:"sort"`
}

func (h *Handler) Search(c *gin.Context) {
	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "search service not wired")
		return
	}
	var req searchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid query: "+err.Error())
		return
	}
	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		apiutil.BadRequest(c, "query parameter 'q' is required")
		return
	}
	limit := defaults.Int(req.Limit, 20)

	result, err := h.svc.Search(c.Request.Context(), appsearch.SearchRequest{
		Query:     req.Q,
		MediaType: req.Type,
		Limit:     limit,
	})
	if err != nil {
		h.log.Error("search failed", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
