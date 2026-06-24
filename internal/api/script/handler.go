// Package script — handler.go defines the Handler type and NewHandler
// constructor for the legacy generation endpoints (GenerateFromClips,
// GenerateBatch). The full ScriptFlowHandler is wired separately.
package script

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// GenerationService narrows the generation operations for the Handler.
type GenerationService interface {
	EnqueueFromClips(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error)
	EnqueueWithImages(ctx context.Context, spec scriptpkg.GenerationSpec) (*scripts.FromClipsResult, error)
}

// Handler owns the legacy generation endpoints.
type Handler struct {
	inner *ScriptFlowHandler
	gen   GenerationService
}

// NewHandler creates a new Handler.
func NewHandler(inner *ScriptFlowHandler, gen GenerationService) *Handler {
	return &Handler{inner: inner, gen: gen}
}

// RegisterRoutes registers legacy script routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate-from-clips", h.GenerateFromClips)
	r.POST("/generate-with-images", h.GenerateWithImages)
	r.POST("/generate-batch", h.GenerateBatch)
	r.GET("/generate-batch/progress", h.GetBatchProgress)
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
	// Delegate to the full handler
	h.inner.ExecuteBatchGeneration(c.Request.Context(), nil, nil)
}

// GetBatchProgress handles GET /generate-batch/progress.
func (h *Handler) GetBatchProgress(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "progress": 0})
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
