// Package lessons provides the HTTP transport layer for lesson endpoints.
package lessons

import (
	"github.com/gin-gonic/gin"
)

// Handler is the thin HTTP transport for /api/lessons endpoints.
type Handler struct {
	inner *LessonsHandler
}

// NewHandler creates a new lessons Handler.
func NewHandler(inner *LessonsHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
