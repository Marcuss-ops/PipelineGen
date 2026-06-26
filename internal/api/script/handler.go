// Package script — handler.go defines the shared types FeatureGates,
// GenerationService, and mapErrorToHTTP consumed by ScriptFlowHandler.
// All route registrations and endpoint implementations live directly
// on ScriptFlowHandler (handler_flow.go).
//
// Per-route feature gating:
//   - /generate-from-clips   → ScriptClipsEnabled
//   - /generate-with-images  → ScriptImagesEnabled
//   - /generate-batch        → ScriptDocsEnabled (creates Google Doc)
//   - /generate-batch/progress → ScriptDocsEnabled
//
// Each route is gated individually so an operator can enable script-
// clips without enabling script-docs, and vice versa. The gate state
// is passed in via the typed FeatureGates struct (no infrastructure/
// config dependency introduced into the transport package — AGENTS.md
// Pattern 8 forbids `internal/api/**` from importing `internal/
// infrastructure/config`).
package script

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// FeatureGates is the typed snapshot of the script-feature flags that
// the composition root hands to NewScriptFlowHandler via ScriptFlowDeps.
// The shape mirrors the canonical `cfg.Features` booleans in
// `internal/infrastructure/config` but is decoupled from the config
// import so the API package remains transport-only (AGENTS.md Pattern 8).
type FeatureGates struct {
	ScriptClipsEnabled  bool
	ScriptDocsEnabled   bool
	ScriptImagesEnabled bool
}

// GenerationService narrows the generation operations for the Handler.
type GenerationService interface {
	EnqueueFromClips(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error)
	EnqueueWithImages(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error)
}

// Handler is a thin wrapper that delegates to ScriptFlowHandler for
// script routes. Generation-specific endpoints are mounted directly
// via ScriptFlowHandler.RegisterRoutes.
type Handler struct {
	inner *ScriptFlowHandler
}

// NewHandler creates a Handler that delegates to the given ScriptFlowHandler.
func NewHandler(inner *ScriptFlowHandler) *Handler {
	return &Handler{inner: inner}
}

// RegisterRoutes delegates route registration to the inner ScriptFlowHandler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.inner != nil {
		h.inner.RegisterRoutes(r)
	}
}

// mapErrorToHTTP maps domain-level script errors to HTTP status codes.
func mapErrorToHTTP(err error) int {
	switch {
	case errors.Is(err, scriptpkg.ErrInvalidPayload):
		return http.StatusBadRequest
	case errors.Is(err, scriptpkg.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, scriptpkg.ErrUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, scriptpkg.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
