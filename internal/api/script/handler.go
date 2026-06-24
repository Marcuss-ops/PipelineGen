// Package script — handler.go defines the Handler type and NewHandler
// constructor for the legacy generation endpoints (GenerateFromClips,
// GenerateBatch). The full ScriptFlowHandler is wired separately.
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
// the composition root hands to NewHandler. The shape mirrors the
// canonical `cfg.Features` booleans in `internal/infrastructure/config`
// but is decoupled from the config import so the API package remains
// transport-only (AGENTS.md Pattern 8).
//
// Each route is gated on ONE boolean — see RegisterRoutes. The
// composition root wires these from cfg.Features in wire_script.go.
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

// Handler owns the legacy generation endpoints.
type Handler struct {
	inner *ScriptFlowHandler
	gen   GenerationService
	gates FeatureGates
}

// NewHandler creates a new Handler.
//
// gates is the typed feature-flag snapshot; composition root populates
// it from cfg.Features. When all three booleans are false the handler
// still mounts (so legacy /api/script/* returns 404 not 500) but every
// route emits a uniform 404. This is intentional — the module-level
// enabledFn in wire_script.go decides whether the entire /script
// router group is exposed at all; the per-route gates decide which
// inner endpoints respond.
func NewHandler(inner *ScriptFlowHandler, gen GenerationService, gates FeatureGates) *Handler {
	return &Handler{inner: inner, gen: gen, gates: gates}
}

// RegisterRoutes registers legacy script routes with per-route feature
// gating. Routes whose feature flag is false are NOT registered at all
// (gin returns 404 for unmatched paths). This is the canonical pattern
// for grouping endpoints under shared prefixes but disabling individual
// routes per-feature.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	if h.gates.ScriptClipsEnabled {
		r.POST("/generate-from-clips", h.GenerateFromClips)
	}
	if h.gates.ScriptImagesEnabled {
		r.POST("/generate-with-images", h.GenerateWithImages)
	}
	if h.gates.ScriptDocsEnabled {
		r.POST("/generate-batch", h.GenerateBatch)
		r.GET("/generate-batch/progress", h.GetBatchProgress)
	}
	if h.inner != nil {
		h.inner.RegisterRoutesRemaining(r)
	}
}

// GenerateFromClips handles POST /generate-from-clips.
func (h *Handler) GenerateFromClips(c *gin.Context) {
	if h.gen == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "generation service not initialized"})
		return
	}
	var spec scriptpkg.GenerationSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload"})
		return
	}
	result, err := h.gen.EnqueueFromClips(c.Request.Context(), spec)
	if err != nil {
		status := mapErrorToHTTP(err)
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": result.JobID, "status": result.JobStatus})
}

// GenerateWithImages handles POST /generate-with-images.
func (h *Handler) GenerateWithImages(c *gin.Context) {
	if h.gen == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "generation service not initialized"})
		return
	}
	var spec scriptpkg.GenerationSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid payload"})
		return
	}
	result, err := h.gen.EnqueueWithImages(c.Request.Context(), spec)
	if err != nil {
		status := mapErrorToHTTP(err)
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": result.JobID, "status": result.JobStatus})
}

// GenerateBatch handles POST /generate-batch.
func (h *Handler) GenerateBatch(c *gin.Context) {
	if h.inner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "batch generation not initialized"})
		return
	}
	h.inner.GenerateBatch(c)
}

// GetBatchProgress handles GET /generate-batch/progress.
func (h *Handler) GetBatchProgress(c *gin.Context) {
	if h.inner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "batch generation not initialized"})
		return
	}
	h.inner.GetBatchProgress(c)
}

// mapErrorToHTTP maps domain errors to HTTP status codes.
func mapErrorToHTTP(err error) int {
	// Map known sentinel errors
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
