// Package handlers provides HTTP API handlers for GPU and text generation
package handlers

import (
	"net/http"

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
// @Param request body textgen.ScriptRequest true "Script generation parameters"
// @Success 200 {object} textgen.ScriptResult
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/script/generate-new [post]
func (h *TextGenHandler) GenerateScript(c *gin.Context) {
	var req textgen.ScriptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.generator.GenerateScript(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("Failed to generate script", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
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
	GPU       *GPUHandler
	TextGen   *TextGenHandler
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
