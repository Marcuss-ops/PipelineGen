// Package books provides the HTTP transport layer for book endpoints.
package books

import (
	"github.com/gin-gonic/gin"
)

// Handler is the thin HTTP transport for /api/books endpoints.
type Handler struct {
	inner *BooksHandler
}

// NewHandler creates a new books Handler.
func NewHandler(inner *BooksHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
