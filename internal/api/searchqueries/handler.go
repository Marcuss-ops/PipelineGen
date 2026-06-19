// Package searchqueries provides the HTTP transport layer for search_queries CRUD.
package searchqueries

import (
	"github.com/gin-gonic/gin"
	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// Handler is the thin HTTP transport for /api/search-queries endpoints.
type Handler struct {
	inner *api.SearchqueriesHandler
}

// NewHandler creates a new searchqueries Handler.
func NewHandler(inner *api.SearchqueriesHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
