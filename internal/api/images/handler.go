// Package images provides the HTTP transport layer for image endpoints.
package images

import (
	"github.com/gin-gonic/gin"
)

// Handler is the thin HTTP transport for /api/images endpoints.
type Handler struct {
	inner *ImagesHandler
}

// NewHandler creates a new images Handler.
func NewHandler(inner *ImagesHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
