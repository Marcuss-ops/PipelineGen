// Package handlers provides HTTP handlers for script generation from clips.
package script

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/scriptclips"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// ScriptFromClipsHandler handles script generation from existing clips
type ScriptFromClipsHandler struct {
	service *scriptclips.ScriptFromClipsService
}

// NewScriptFromClipsHandler creates a new handler
func NewScriptFromClipsHandler(service *scriptclips.ScriptFromClipsService) *ScriptFromClipsHandler {
	return &ScriptFromClipsHandler{
		service: service,
	}
}

// RegisterRoutes registers script-from-clips routes
func (h *ScriptFromClipsHandler) RegisterRoutes(rg *gin.RouterGroup) {
	script := rg.Group("/script")
	{
		script.POST("/generate-from-clips", h.GenerateFromClips)
	}
}

// GenerateFromClips godoc
// @Summary Generate script from existing clips
// @Description Generate a video script based on existing clips in Drive and Artlist, finding 3 Artlist clips per segment
// @Tags script
// @Accept json
// @Produce json
// @Param request body scriptclips.ScriptFromClipsRequest true "Generation request from clips"
// @Success 200 {object} scriptclips.ScriptFromClipsResponse
// @Router /script/generate-from-clips [post]
func (h *ScriptFromClipsHandler) GenerateFromClips(c *gin.Context) {
	var req scriptclips.ScriptFromClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Validate required fields
	if req.Topic == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "topic is required"})
		return
	}

	// Validate duration bounds (10 seconds to 30 minutes)
	if req.TargetDuration <= 0 {
		req.TargetDuration = 60 // default
	} else if req.TargetDuration < 10 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "target_duration must be at least 10 seconds"})
		return
	} else if req.TargetDuration > 1800 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "target_duration cannot exceed 30 minutes (1800 seconds)"})
		return
	}

	// Validate clips per segment
	if req.ClipsPerSegment <= 0 {
		req.ClipsPerSegment = 3 // default
	}

	logger.Info("Generating script from clips",
		zap.String("topic", req.Topic),
		zap.String("language", req.Language),
		zap.Int("target_duration", req.TargetDuration),
		zap.Int("clips_per_segment", req.ClipsPerSegment),
		zap.Bool("use_artlist", req.UseArtlist),
		zap.Bool("use_drive_clips", req.UseDriveClips),
	)

	// Execute the full pipeline
	result, err := h.service.GenerateScriptFromClips(c.Request.Context(), &req)
	if err != nil {
		logger.Error("Script-from-clips generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}
