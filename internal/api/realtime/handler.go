// Package realtime provides the HTTP transport layer for realtime endpoints.
package realtime

import (
	"github.com/gin-gonic/gin"
)

// Handler is the thin HTTP transport for realtime endpoints.
// It wraps the MatchHandler implementation that lives in impl.go (same package).
type Handler struct {
	inner *MatchHandler
}

// NewHandler creates a new realtime Handler wrapping the MatchHandler impl.
func NewHandler(inner *MatchHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates to the inner MatchHandler implementation.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner == nil {
		return
	}
	h.inner.RegisterRoutes(r)
}
