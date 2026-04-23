// Package handlers provides HTTP API handlers for GPU and text generation
package nlp

import (
	"fmt"
	"net/http"
	"strings"

	"velox/go-master/internal/gpu"
	"velox/go-master/internal/textgen"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GPUHandler holds dependencies for GPU API handlers
type GPUHandler struct {
	gpuManager *gpu.Manager
	logger     *zap.Logger
}

// NewGPUHandler creates a new GPU API handler
func NewGPUHandler(gpuMgr *gpu.Manager, logger *zap.Logger) *GPUHandler {
	return &GPUHandler{
		gpuManager: gpuMgr,
		logger:     logger,
	}
}

// GetGPUStatus godoc
// @Summary Get GPU status
// @Description Get current GPU hardware information and health
// @Tags GPU
// @Produce json
// @Success 200 {object} gpu.GPUInfo
// @Failure 500 {object} map[string]string
// @Router /api/gpu/status [get]
func (h *GPUHandler) GetGPUStatus(c *gin.Context) {
	if h.gpuManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "GPU manager not configured",
		})
		return
	}

	// Get selected GPU info
	gpuInfo, err := h.gpuManager.GetSelectedGPU()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":         err.Error(),
			"gpu_available": false,
		})
		return
	}

	healthy := h.gpuManager.IsHealthy(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"gpu_available": true,
		"gpu_info":      gpuInfo,
		"is_healthy":    healthy,
		"summary":       h.gpuManager.GetGPUSummary(),
	})
}

// GetGPUList godoc
// @Summary List all GPUs
// @Description List all detected GPU devices
// @Tags GPU
// @Produce json
// @Success 200 {array} gpu.GPUInfo
// @Failure 500 {object} map[string]string
// @Router /api/gpu/list [get]
func (h *GPUHandler) GetGPUList(c *gin.Context) {
	// This would require adding a method to GPU manager to list all GPUs
	c.JSON(http.StatusOK, gin.H{
		"message": "GPU list endpoint - implement by adding ListGPUs() to manager",
	})
}

// TextGenHandler holds dependencies for text generation API handlers
type TextGenHandler struct {
	generator *textgen.Generator
	logger    *zap.Logger
}

type ScriptGenerationRequest struct {
	Topic       string   `json:"topic" binding:"required"`
	Duration    int      `json:"duration"`
	Language    string   `json:"language"`
	Tone        string   `json:"tone"`
	Keywords    []string `json:"keywords"`
	Structure   []string `json:"structure"`
	UseGPU      bool     `json:"use_gpu"`
	TargetWords int      `json:"target_words"`
	Model       string   `json:"model"`
}

// NewTextGenHandler creates a new text generation API handler
func NewTextGenHandler(gen *textgen.Generator, logger *zap.Logger) *TextGenHandler {
	return &TextGenHandler{
		generator: gen,
		logger:    logger,
	}
}

// GenerateText godoc
// @Summary Generate text with AI
// @Description Generate text using Ollama, OpenAI, or Groq with optional GPU acceleration
// @Tags TextGeneration
// @Accept json
// @Produce json
// @Param request body textgen.GenerationRequest true "Text generation parameters"
// @Success 200 {object} textgen.GenerationResult
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/text/generate [post]
func (h *TextGenHandler) GenerateText(c *gin.Context) {
	var req textgen.GenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.generator.GenerateText(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("Failed to generate text", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// GenerateScript godoc
// @Summary Generate video script
// @Description Generate a structured script for video creation with AI
// @Tags TextGeneration
// @Accept json
// @Produce json
// @Param request body ScriptGenerationRequest true "Script generation parameters"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/script/generate-new [post]
func (h *TextGenHandler) GenerateScript(c *gin.Context) {
	var req ScriptGenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if strings.TrimSpace(req.Topic) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "topic is required"})
		return
	}
	if req.Duration <= 0 {
		req.Duration = 60
	}
	if req.Language == "" {
		req.Language = "english"
	}
	if req.Tone == "" {
		req.Tone = "professional"
	}
	if req.TargetWords <= 0 {
		req.TargetWords = (req.Duration * 140) / 60
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gemma3:4b"
	}

	prompt := buildScriptPrompt(req)
	result, err := h.generator.GenerateText(c.Request.Context(), &textgen.GenerationRequest{
		Provider:    textgen.ProviderOllama,
		Model:       req.Model,
		Prompt:      prompt,
		Temperature: 0.7,
		MaxTokens:   req.TargetWords * 2,
		UseGPU:      req.UseGPU,
	})
	if err != nil {
		h.logger.Error("Failed to generate script", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	wordCount := len(strings.Fields(result.Text))

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"script":       result.Text,
		"word_count":   wordCount,
		"est_duration": int(float64(wordCount) * 60 / 140),
		"model":        result.Model,
	})
}

func buildScriptPrompt(req ScriptGenerationRequest) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("You are an expert script writer. Create a %s script about: %s.\n\n", req.Tone, req.Topic))
	b.WriteString(fmt.Sprintf("Language: %s\n", req.Language))
	b.WriteString(fmt.Sprintf("Target length: approximately %d words\n", req.TargetWords))
	if len(req.Keywords) > 0 {
		b.WriteString(fmt.Sprintf("Keywords to include naturally: %s\n", strings.Join(req.Keywords, ", ")))
	}
	if len(req.Structure) > 0 {
		b.WriteString(fmt.Sprintf("Structure: %s\n", strings.Join(req.Structure, ", ")))
	}
	b.WriteString("\nWrite only the script text. Keep it engaging, natural, and suitable for narration.")
	return b.String()
}

// GetGPUStatusForTextGen godoc
// @Summary Get GPU status for text generation
// @Description Check GPU availability and health for AI text generation
// @Tags TextGeneration
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Router /api/text/gpu-status [get]
func (h *TextGenHandler) GetGPUStatusForTextGen(c *gin.Context) {
	status := h.generator.GetGPUStatus(c.Request.Context())
	c.JSON(http.StatusOK, status)
}

// RegisterRoutes registra tutti gli endpoint GPU
func (h *GPUHandler) RegisterRoutes(protected *gin.RouterGroup) {
	gpuGroup := protected.Group("/gpu")
	{
		gpuGroup.GET("/status", h.GetGPUStatus)
		gpuGroup.GET("/list", h.GetGPUList)
	}
}

// RegisterRoutes registra tutti gli endpoint Text Generation
func (h *TextGenHandler) RegisterRoutes(protected *gin.RouterGroup) {
	textGroup := protected.Group("/text")
	{
		textGroup.POST("/generate", h.GenerateText)
		textGroup.GET("/gpu-status", h.GetGPUStatusForTextGen)
	}

	scriptGroup := protected.Group("/script")
	{
		scriptGroup.POST("/generate-new", h.GenerateScript)
	}
}

// GPUTextGenHandler wrapper per compatibilità con il router
// Combina GPU e Text Generation in un unico handler
type GPUTextGenHandler struct {
	GPU     *GPUHandler
	TextGen *TextGenHandler
}

// NewGPUTextGenHandler crea un handler combinato
func NewGPUTextGenHandler(gpuMgr *gpu.Manager, gen *textgen.Generator, logger *zap.Logger) *GPUTextGenHandler {
	return &GPUTextGenHandler{
		GPU:     NewGPUHandler(gpuMgr, logger),
		TextGen: NewTextGenHandler(gen, logger),
	}
}

// RegisterRoutes registra tutti gli endpoint GPU + Text Generation
func (h *GPUTextGenHandler) RegisterRoutes(protected *gin.RouterGroup) {
	h.GPU.RegisterRoutes(protected)
	h.TextGen.RegisterRoutes(protected)
}
