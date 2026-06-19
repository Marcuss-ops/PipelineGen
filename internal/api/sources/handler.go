// Package sources provides the HTTP transport layer for media/sources endpoints.
//
// This package is intentionally thin — it contains only the Handler struct
// and route registration. Business logic (clip CRUD, voiceover, search,
// Drive sync, enrichment) lives in the existing domain packages under
// internal/sources/, internal/media/, and internal/upload/.
//
// See docs/api-package-boundaries.md for the full architecture.
package sources

import (
	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
)

// Handler is the HTTP transport for /api/media endpoints.
// It delegates all routes to the inner SourcesHandler which owns the
// business logic (clip CRUD, voiceover, search, Drive sync, enrichment).
type Handler struct {
	inner *api.SourcesHandler
}

// NewHandler creates a new sources Handler wrapping the existing SourcesHandler.
func NewHandler(inner *api.SourcesHandler) *Handler {
	return &Handler{inner: inner}
}

// Inner returns the inner SourcesHandler for cross-injections (e.g. images handler).
func (h *Handler) Inner() *api.SourcesHandler {
	return h.inner
}

// RegisterRoutes delegates all route registration to the inner handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
