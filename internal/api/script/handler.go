// Package script — handler.go carries the shared types consumed by
// ScriptFlowHandler. Handler is a thin wrapper that delegates route
// registration to ScriptFlowHandler.RegisterRoutes. All generation-
// specific endpoints have been removed — use POST /api/generations
// instead.
package script

import "github.com/gin-gonic/gin"

// Handler is a thin wrapper that delegates to ScriptFlowHandler for
// non-generation script routes. All generation-specific endpoints have
// been removed — use POST /api/generations instead.
type Handler struct {
	inner *ScriptFlowHandler
}

// NewHandler creates a Handler that delegates to the given ScriptFlowHandler.
func NewHandler(inner *ScriptFlowHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes registers the non-generation script routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner != nil {
		h.inner.RegisterRoutes(r)
	}
}
