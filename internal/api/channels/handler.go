// Package channels provides the HTTP transport layer for category_channels CRUD.
package channels

import (
	"github.com/gin-gonic/gin"
)

// Handler is the thin HTTP transport for /api/channels endpoints.
type Handler struct {
	inner *ChannelsHandler
}

// NewHandler creates a new channels Handler.
func NewHandler(inner *ChannelsHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
