// Package handlers provides HTTP handlers for script generation with clips.
package script

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/scriptclips"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// ScriptClipsHandler handles script generation with automatic clip mapping
type ScriptClipsHandler struct {
	service *scriptclips.ScriptClipsService
}

// NewScriptClipsHandler creates a new handler
func NewScriptClipsHandler(service *scriptclips.ScriptClipsService) *ScriptClipsHandler {
	return &ScriptClipsHandler{
		service: service,
	}
}

// RegisterRoutes registers script+clips routes
func (h *ScriptClipsHandler) RegisterRoutes(rg *gin.RouterGroup) {
	script := rg.Group("/script")
	{
		script.POST("/generate-with-clips", h.GenerateWithClips)
	}
}

// GenerateWithClips godoc
// @Summary Generate script with automatic clip mapping
// @Description Generate a video script, extract entities, find/download stock clips, and upload to Drive
// @Tags script
// @Accept json
// @Produce json
// @Param request body scriptclips.ScriptClipsRequest true "Generation request with clips"
// @Success 200 {object} scriptclips.ScriptClipsResponse
// @Router /script/generate-with-clips [post]
func (h *ScriptClipsHandler) GenerateWithClips(c *gin.Context) {
	var req scriptclips.ScriptClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Validate required fields
	if req.SourceText == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "source_text is required"})
		return
	}
	if req.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "title is required"})
		return
	}

	// Validate duration bounds (10 seconds to 60 minutes)
	if req.Duration <= 0 {
		req.Duration = 60 // default
	} else if req.Duration < 10 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "duration must be at least 10 seconds"})
		return
	} else if req.Duration > 3600 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "duration cannot exceed 60 minutes (3600 seconds)"})
		return
	}

	// Validate entity count
	if req.EntityCountPerSegment <= 0 {
		req.EntityCountPerSegment = 12 // default
	}

	logger.Info("Generating script with clips",
		zap.String("title", req.Title),
		zap.String("language", req.Language),
		zap.Int("duration", req.Duration),
		zap.Int("entity_count", req.EntityCountPerSegment),
	)

	// Use background context with timeout instead of request context
	// This prevents cancellation when the client disconnects
	// 30 minutes timeout for full pipeline (script + entities + clips + uploads)
	pipelineCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Execute the full pipeline with detached context
	result, err := h.service.GenerateScriptWithClips(pipelineCtx, &req)
	if err != nil {
		logger.Error("Script+clips generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}
