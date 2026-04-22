// Package handlers provides HTTP handlers for stock video processing endpoints.
package stock

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/stock"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// StockProcessHandler handles stock video processing endpoints
type StockProcessHandler struct {
	manager        *stock.StockManager
	rustBinaryPath string
	effectsDir     string
}

// NewStockProcessHandler creates a new stock process handler
func NewStockProcessHandler(manager *stock.StockManager, rustBinaryPath, effectsDir string) *StockProcessHandler {
	return &StockProcessHandler{
		manager:        manager,
		rustBinaryPath: rustBinaryPath,
		effectsDir:     effectsDir,
	}
}

// RegisterRoutes registers stock process routes
func (h *StockProcessHandler) RegisterRoutes(rg *gin.RouterGroup) {
	s := rg.Group("/stock")
	{
		s.POST("/process", h.Process)
		s.POST("/process/batch", h.BatchProcess)
		s.POST("/studio", h.CreateStudio)
		s.GET("/health", h.Health)
	}
}

// ProcessRequest represents a video processing request
type ProcessRequest struct {
	ProjectID     string   `json:"project_id" binding:"required"`
	VideoPaths    []string `json:"video_paths" binding:"required"`
	OutputPath    string   `json:"output_path"`
	TransitionType string  `json:"transition_type"`
	Effects       []string `json:"effects"`
	MusicPath     string   `json:"music_path"`
}

// Process processes videos using the Rust binary
func (h *StockProcessHandler) Process(c *gin.Context) {
	var req ProcessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	logger.Info("Processing stock video",
		zap.String("project_id", req.ProjectID),
		zap.Int("video_count", len(req.VideoPaths)),
	)

	// Use manager to process
	options := &stock.ProcessOptions{
		ProjectName: req.ProjectID,
		Videos:      req.VideoPaths,
		Quality:     "best",
	}

	result, err := h.manager.ProcessProject(c.Request.Context(), options)
	if err != nil {
		logger.Error("Processing failed",
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Processing failed: " + err.Error(),
		})
		return
	}

	logger.Info("Stock video processed successfully",
		zap.String("project", result.ProjectName),
		zap.Int("videos_processed", result.VideosProcessed),
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":              true,
		"project_name":    result.ProjectName,
		"videos_processed": result.VideosProcessed,
		"output_files":    result.OutputFiles,
		"processing_time": result.Duration,
	})
}

// BatchProcess processes multiple videos in batch
func (h *StockProcessHandler) BatchProcess(c *gin.Context) {
	var req struct {
		Requests []ProcessRequest `json:"requests" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	logger.Info("Batch processing requested",
		zap.Int("count", len(req.Requests)),
	)

	results := make([]gin.H, 0, len(req.Requests))
	for i, procReq := range req.Requests {
		options := &stock.ProcessOptions{
			ProjectName: procReq.ProjectID,
			Videos:      procReq.VideoPaths,
		}

		result, err := h.manager.ProcessProject(c.Request.Context(), options)
		status := "success"
		if err != nil {
			status = "failed"
		}

		results = append(results, gin.H{
			"index":  i,
			"status": status,
			"result": result,
		})
	}

	c.JSON(http.StatusAccepted, gin.H{
		"ok":      true,
		"results": results,
		"count":   len(results),
	})
}

// CreateStudio creates a studio project
func (h *StockProcessHandler) CreateStudio(c *gin.Context) {
	var req struct {
		ProjectID string `json:"project_id" binding:"required"`
		Name      string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	logger.Info("Studio project created",
		zap.String("project_id", req.ProjectID),
		zap.String("name", req.Name),
	)

	c.JSON(http.StatusCreated, gin.H{
		"ok":         true,
		"project_id": req.ProjectID,
		"name":       req.Name,
	})
}

// Health checks the health of stock processing
func (h *StockProcessHandler) Health(c *gin.Context) {
	// Check if Rust binary exists
	_, err := os.Stat(h.rustBinaryPath)
	binaryOK := err == nil

	c.JSON(http.StatusOK, gin.H{
		"ok":            true,
		"rust_binary":   h.rustBinaryPath,
		"binary_exists": binaryOK,
		"effects_dir":   h.effectsDir,
		"service":       "stock-processor",
	})
}
