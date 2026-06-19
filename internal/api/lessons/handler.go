// Package lessons provides the HTTP transport layer for lesson endpoints.
package lessons

import (
	"github.com/gin-gonic/gin"
	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// Handler is the thin HTTP transport for /api/lessons endpoints.
type Handler struct {
	inner *api.LessonsHandler
}

// NewHandler creates a new lessons Handler.
func NewHandler(inner *api.LessonsHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
