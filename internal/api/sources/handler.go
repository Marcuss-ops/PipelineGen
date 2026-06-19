// Package sources provides the HTTP transport layer for media/sources endpoints.
//
// This package contains the SourcesHandler (business logic) and RouteHandler
// (thin HTTP transport wrapper). Files migrated from package api used *Handler
// as receiver for SourcesHandler methods; the type alias below preserves that.
package sources

import (
	"github.com/gin-gonic/gin"
)

// Handler is a type alias for SourcesHandler, preserving backward compatibility
// for the 30+ handler_sources_*.go files that use *Handler as receiver.
type Handler = SourcesHandler

// RouteHandler is the thin HTTP transport for /api/media endpoints.
// It delegates all routes to the inner SourcesHandler which owns the
// business logic (clip CRUD, voiceover, search, Drive sync, enrichment).
type RouteHandler struct {
	inner *SourcesHandler
}

// NewRouteHandler creates a new RouteHandler wrapping the SourcesHandler.
func NewRouteHandler(inner *SourcesHandler) *RouteHandler {
	return &RouteHandler{inner: inner}
}

// Inner returns the inner SourcesHandler for cross-injections (e.g. images handler).
func (h *RouteHandler) Inner() *SourcesHandler {
	return h.inner
}

// RegisterRoutes delegates all route registration to the inner handler.
func (h *RouteHandler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
