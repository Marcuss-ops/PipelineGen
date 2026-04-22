// Package handlers provides HTTP handlers for the API.
package video

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/pipeline"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// VideoHandler handles video processing HTTP requests
type VideoHandler struct {
	pipelineService *pipeline.VideoCreationService
}

// NewVideoHandler creates a new video handler
func NewVideoHandler(pipelineService *pipeline.VideoCreationService) (*VideoHandler, error) {
	if pipelineService == nil {
		return nil, fmt.Errorf("pipeline service cannot be nil")
	}
	return &VideoHandler{
		pipelineService: pipelineService,
	}, nil
}

// RegisterRoutes registers video processing routes
func (h *VideoHandler) RegisterRoutes(rg *gin.RouterGroup) {
	videoGroup := rg.Group("/video")
	{
		videoGroup.POST("/create-master", h.CreateMaster)
		videoGroup.GET("/health", h.Health)
		videoGroup.GET("/info", h.GetInfo)
	}
}

// CreateMasterRequest represents the request for video creation
type CreateMasterRequest struct {
	VideoName    string `json:"video_name" binding:"required"`
	ProjectName  string `json:"project_name"`
	ScriptText   string `json:"script_text"`
	YouTubeURL   string `json:"youtube_url"`
	Source       string `json:"source"`
	Language     string `json:"language"`
	Duration     int    `json:"duration"`
	DriveFolder  string `json:"voiceover_drive_folder"`
	SkipGDocs    bool   `json:"skip_gdocs"`
	EntityCount  int    `json:"entity_count"`
}

// CreateMaster godoc
// @Summary Create master video
// @Description Main endpoint for video creation with script generation, voiceover, and worker dispatch
// @Tags video
// @Accept json
// @Produce json
// @Param request body CreateMasterRequest true "Master creation request"
// @Success 202 {object} map[string]interface{}
// @Router /video/create-master [post]
func (h *VideoHandler) CreateMaster(c *gin.Context) {
	var req CreateMasterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Validate duration at API layer
	if req.Duration < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "duration cannot be negative"})
		return
	}
	if req.Duration > 7200 { // Max 2 hours
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "duration cannot exceed 7200 seconds (2 hours)"})
		return
	}

	logger.Info("CreateMaster request",
		zap.String("video_name", req.VideoName),
		zap.String("project", req.ProjectName),
		zap.String("language", req.Language),
		zap.Int("duration", req.Duration),
		zap.Int("entity_count", req.EntityCount),
	)

	// Convert to pipeline request
	pipelineReq := &pipeline.VideoCreationRequest{
		VideoName:   req.VideoName,
		ProjectName: req.ProjectName,
		ScriptText:  req.ScriptText,
		YouTubeURL:  req.YouTubeURL,
		Source:      req.Source,
		Language:    req.Language,
		Duration:    req.Duration,
		DriveFolder: req.DriveFolder,
		SkipGDocs:   req.SkipGDocs,
		EntityCount: req.EntityCount,
	}

	// Delegate to pipeline service
	result, err := h.pipelineService.CreateMaster(c.Request.Context(), pipelineReq)
	if err != nil {
		logger.Error("Video creation pipeline failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Video creation failed: " + err.Error(),
		})
		return
	}

	// Return response
	c.JSON(http.StatusAccepted, gin.H{
		"ok":           true,
		"job_id":       result.JobID,
		"video_name":   result.VideoName,
		"project_name": result.ProjectName,
		"status":       result.Status,
		"script": gin.H{
			"generated":   result.ScriptGenerated,
			"script_text": result.ScriptText,
			"word_count":  result.ScriptWordCount,
			"model":       result.ScriptModel,
		},
		"entities": gin.H{
			"total_segments":       result.EntityAnalysis.TotalSegments,
			"entity_count_per_segment": result.EntityAnalysis.EntityCountPerSegment,
			"total_entities":     result.EntityAnalysis.TotalEntities,
			"segment_entities":   result.EntityAnalysis.SegmentEntities,
		},
		"voiceover": gin.H{
			"generated": len(result.VoiceoverResults) > 0,
			"items":     result.VoiceoverResults,
		},
		"video": gin.H{
			"created":    result.VideoCreated,
			"output":     result.VideoOutput,
			"processing": result.VideoCreated,
		},
	})
}

// Health godoc
// @Summary Health check
// @Description Check if video processing service is healthy
// @Tags video
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /video/health [get]
func (h *VideoHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"status":  "healthy",
		"service": "video-processor",
	})
}

// GetInfo godoc
// @Summary Get video service info
// @Description Get information about video processing capabilities
// @Tags video
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /video/info [get]
func (h *VideoHandler) GetInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"service": gin.H{
			"name":    "velox-video-processor",
			"version": "1.0.0-go",
			"backend": "rust",
		},
		"capabilities": []string{
			"video_processing",
			"stock_clip_generation",
			"effects_application",
			"audio_mixing",
			"script_generation",
			"voiceover_generation",
		},
		"endpoints": []string{
			"/api/video/process",
			"/api/video/generate",
			"/api/video/create-master",
			"/api/video/effects",
			"/api/video/audio/mix",
			"/api/video/audio/voiceover",
		},
	})
}
