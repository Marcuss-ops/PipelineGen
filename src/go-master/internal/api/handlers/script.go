// Package handlers provides HTTP handlers for script generation endpoints.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// ScriptHandler handles script generation HTTP requests
type ScriptHandler struct {
	generator *ollama.Generator
	client    *ollama.Client
}

// NewScriptHandler creates a new script handler
func NewScriptHandler(generator *ollama.Generator, client *ollama.Client) *ScriptHandler {
	return &ScriptHandler{
		generator: generator,
		client:    client,
	}
}

// RegisterRoutes registers script generation routes
func (h *ScriptHandler) RegisterRoutes(rg *gin.RouterGroup) {
	script := rg.Group("/script")
	{
		script.POST("/generate", h.GenerateFromText)
		script.POST("/from-youtube", h.GenerateFromYouTube)
		script.POST("/regenerate", h.Regenerate)
		script.GET("/models", h.ListModels)
		script.GET("/health", h.CheckHealth)
	}
}

// GenerateFromText godoc
// @Summary Generate script from text
// @Description Generate a video script from source text using Ollama
// @Tags script
// @Accept json
// @Produce json
// @Param request body ollama.TextGenerationRequest true "Generation request"
// @Success 200 {object} map[string]interface{}
// @Router /script/generate [post]
func (h *ScriptHandler) GenerateFromText(c *gin.Context) {
	var req ollama.TextGenerationRequest
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

	logger.Info("Generating script from text",
		zap.String("title", req.Title),
		zap.String("language", req.Language),
		zap.Int("duration", req.Duration),
	)

	result, err := h.generator.GenerateFromText(c.Request.Context(), &req)
	if err != nil {
		logger.Error("Script generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"script":       result.Script,
		"word_count":   result.WordCount,
		"est_duration": result.EstDuration,
		"model":        result.Model,
	})
}

// GenerateFromYouTube godoc
// @Summary Generate script from YouTube
// @Description Generate a video script from YouTube URL using Ollama
// @Tags script
// @Accept json
// @Produce json
// @Param request body ollama.YouTubeGenerationRequest true "YouTube generation request"
// @Success 200 {object} map[string]interface{}
// @Router /script/from-youtube [post]
func (h *ScriptHandler) GenerateFromYouTube(c *gin.Context) {
	var req ollama.YouTubeGenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Validate required fields
	if req.YouTubeURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "youtube_url is required"})
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

	logger.Info("Generating script from YouTube",
		zap.String("url", req.YouTubeURL),
		zap.String("title", req.Title),
	)

	// This endpoint is not yet implemented - return 501 Not Implemented
	c.JSON(http.StatusNotImplemented, gin.H{
		"ok":    false,
		"error": "YouTube transcript download is not yet implemented. Use /script/from-transcript endpoint with a pre-fetched transcript instead.",
	})
}

// GenerateFromYouTubeTranscriptRequest request with pre-fetched transcript
type GenerateFromYouTubeTranscriptRequest struct {
	YouTubeURL string `json:"youtube_url"`
	Transcript string `json:"transcript" binding:"required"`
	Title      string `json:"title" binding:"required"`
	Language   string `json:"language"`
	Duration   int    `json:"duration"`
	Model      string `json:"model"`
}

// GenerateFromYouTubeTranscript godoc
// @Summary Generate script from YouTube transcript
// @Description Generate a video script from YouTube transcript using Ollama
// @Tags script
// @Accept json
// @Produce json
// @Param request body GenerateFromYouTubeTranscriptRequest true "Transcript generation request"
// @Success 200 {object} map[string]interface{}
// @Router /script/from-transcript [post]
func (h *ScriptHandler) GenerateFromYouTubeTranscript(c *gin.Context) {
	var req GenerateFromYouTubeTranscriptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Validate required fields
	if req.Transcript == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "transcript is required"})
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

	logger.Info("Generating script from YouTube transcript",
		zap.String("title", req.Title),
		zap.Int("transcript_len", len(req.Transcript)),
	)

	// Build YouTube generation request
	ytReq := &ollama.YouTubeGenerationRequest{
		YouTubeURL: req.YouTubeURL,
		Title:      req.Title,
		Language:   req.Language,
		Duration:   req.Duration,
		Model:      req.Model,
	}

	result, err := h.generator.GenerateFromYouTubeTranscript(c.Request.Context(), req.Transcript, ytReq)
	if err != nil {
		logger.Error("Transcript script generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"script":       result.Script,
		"word_count":   result.WordCount,
		"est_duration": result.EstDuration,
		"model":        result.Model,
	})
}

// Regenerate godoc
// @Summary Regenerate script
// @Description Regenerate an existing script with improvements
// @Tags script
// @Accept json
// @Produce json
// @Param request body ollama.RegenerationRequest true "Regeneration request"
// @Success 200 {object} map[string]interface{}
// @Router /script/regenerate [post]
func (h *ScriptHandler) Regenerate(c *gin.Context) {
	var req ollama.RegenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Validate required fields
	if req.OriginalScript == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "original_script is required"})
		return
	}

	logger.Info("Regenerating script",
		zap.String("title", req.Title),
		zap.String("tone", req.Tone),
	)

	result, err := h.generator.Regenerate(c.Request.Context(), &req)
	if err != nil {
		logger.Error("Script regeneration failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"script":       result.Script,
		"word_count":   result.WordCount,
		"est_duration": result.EstDuration,
		"model":        result.Model,
	})
}

// ListModels godoc
// @Summary List available models
// @Description List all available Ollama models
// @Tags script
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /script/models [get]
func (h *ScriptHandler) ListModels(c *gin.Context) {
	models, err := h.generator.ListModels(c.Request.Context())
	if err != nil {
		logger.Error("Failed to list models", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"models": models,
		"count":  len(models),
	})
}

// CheckHealth godoc
// @Summary Check Ollama health
// @Description Check if Ollama service is reachable
// @Tags script
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /script/health [get]
func (h *ScriptHandler) CheckHealth(c *gin.Context) {
	healthy := h.client.CheckHealth(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"healthy": healthy,
		"service": "ollama",
	})
}

// ScriptSummarizeRequest request for text summarization (script-specific)
type ScriptSummarizeRequest struct {
	Text     string `json:"text" binding:"required"`
	MaxWords int    `json:"max_words"`
}

// Summarize godoc
// @Summary Summarize text
// @Description Summarize text using Ollama
// @Tags script
// @Accept json
// @Produce json
// @Param request body SummarizeRequest true "Summarize request"
// @Success 200 {object} map[string]interface{}
// @Router /script/summarize [post]
func (h *ScriptHandler) Summarize(c *gin.Context) {
	var req ScriptSummarizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "text is required"})
		return
	}

	maxWords := req.MaxWords
	if maxWords == 0 {
		maxWords = 100
	}

	summary, err := h.client.Summarize(c.Request.Context(), req.Text, maxWords)
	if err != nil {
		logger.Error("Summarization failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"summary": summary,
	})
}