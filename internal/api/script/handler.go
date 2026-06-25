// Package script — handler.go carries the shared types, interfaces, and
// helpers consumed by ScriptFlowHandler. The legacy Handler wrapper that
// duplicated per-route feature gating was removed in PG-024 (June 2026);
// ScriptFlowHandler.RegisterRoutes is now the single canonical route
// registration for /api/script/.
//
// Phase 2 activation (June 2026) — per-route feature gating:
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
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// FeatureGates is the typed snapshot of the script-feature flags that
// the composition root hands to NewScriptFlowHandler. The shape mirrors
// the canonical `cfg.Features` booleans in `internal/infrastructure/config`
// but is decoupled from the config import so the API package remains
// transport-only (AGENTS.md Pattern 8).
//
// Each route is gated on ONE boolean — see ScriptFlowHandler.RegisterRoutes.
// The composition root wires these from cfg.Features in wire_script.go.
type FeatureGates struct {
	ScriptClipsEnabled  bool
	ScriptDocsEnabled   bool
	ScriptImagesEnabled bool
}

// GenerationService narrows the generation operations consumed by
// ScriptFlowHandler.
type GenerationService interface {
	EnqueueFromClips(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error)
	EnqueueWithImages(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error)
}

// mapErrorToHTTP maps domain errors to HTTP status codes.
func mapErrorToHTTP(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case err == scriptpkg.ErrInvalidPayload:
		return http.StatusBadRequest
	case err == scriptpkg.ErrValidation:
		return http.StatusBadRequest
	case err == scriptpkg.ErrUnsupportedVersion:
		return http.StatusBadRequest
	case err == scriptpkg.ErrUnavailable:
		return http.StatusServiceUnavailable
	case err == scriptpkg.ErrConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

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
