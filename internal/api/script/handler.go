// Package script provides the HTTP transport layer for script generation endpoints.
//
// This package is intentionally thin — it contains only the Handler struct,
// route registration, and request/response DTOs. Business orchestration
// (generation, curation, scene planning, document building) lives in
// internal/application/scriptflow/.
//
// See docs/api-package-boundaries.md for the full architecture.
package script

import (
	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// Handler is the HTTP transport for /api/script endpoints.
// It owns no business logic — all orchestration is delegated
// to the inner ScriptFlowHandler (which will eventually be
// replaced by use-case interfaces from application/scriptflow/).
type Handler struct {
	inner *api.ScriptFlowHandler
}

// NewHandler creates a new script Handler wrapping the existing
// ScriptFlowHandler. After PR 3-4, this will be replaced by
// use-case interfaces (GenerateFromClipsUseCase, etc.).
func NewHandler(inner *api.ScriptFlowHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates route registration to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
