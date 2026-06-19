// Package searchqueries provides the HTTP transport layer for search_queries CRUD.
package searchqueries

import (
	"github.com/gin-gonic/gin"
)

// Handler is the thin HTTP transport for /api/search-queries endpoints.
type Handler struct {
	inner *SearchqueriesHandler
}

// NewHandler creates a new searchqueries Handler.
func NewHandler(inner *SearchqueriesHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
