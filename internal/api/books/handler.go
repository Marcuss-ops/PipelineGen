// Package books provides the HTTP transport layer for book endpoints.
package books

import (
	"github.com/gin-gonic/gin"
	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// Handler is the thin HTTP transport for /api/books endpoints.
type Handler struct {
	inner *api.BooksHandler
}

// NewHandler creates a new books Handler.
func NewHandler(inner *api.BooksHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
