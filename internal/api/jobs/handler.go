// Package jobs provides the HTTP transport layer for job endpoints.
package jobs

import (
	"github.com/gin-gonic/gin"
	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// Handler is the thin HTTP transport for /api/jobs endpoints.
type Handler struct {
	inner *api.JobsHandler
}

// NewHandler creates a new jobs Handler.
func NewHandler(inner *api.JobsHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
