// Package handlers provides HTTP handlers for stock orchestrator.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/stockorchestrator"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// StockOrchestratorHandler handles the full stock video pipeline
type StockOrchestratorHandler struct {
	service *stockorchestrator.StockOrchestratorService
}

// NewStockOrchestratorHandler creates a new handler
func NewStockOrchestratorHandler(service *stockorchestrator.StockOrchestratorService) *StockOrchestratorHandler {
	return &StockOrchestratorHandler{
		service: service,
	}
}

// RegisterRoutes registers stock orchestrator routes
func (h *StockOrchestratorHandler) RegisterRoutes(rg *gin.RouterGroup) {
	stock := rg.Group("/stock")
	{
		stock.POST("/orchestrate", h.Orchestrate)
		stock.POST("/orchestrate/batch", h.OrchestrateBatch)
	}
}

// Orchestrate godoc
// @Summary Execute full stock video pipeline
// @Description Search YouTube → Download → Extract Entities → Upload to Drive with folders
// @Tags stock
// @Accept json
// @Produce json
// @Param request body stockorchestrator.StockOrchestratorRequest true "Orchestration request"
// @Success 200 {object} stockorchestrator.StockOrchestratorResponse
// @Router /stock/orchestrate [post]
func (h *StockOrchestratorHandler) Orchestrate(c *gin.Context) {
	var req stockorchestrator.StockOrchestratorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Validate required fields
	if req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "query is required"})
		return
	}

	// Validate max_videos
	if req.MaxVideos <= 0 {
		req.MaxVideos = 5
	} else if req.MaxVideos > 20 {
		req.MaxVideos = 20 // Limit to prevent abuse
	}

	logger.Info("Stock orchestration requested",
		zap.String("query", req.Query),
		zap.Int("max_videos", req.MaxVideos),
		zap.String("quality", req.Quality),
		zap.Bool("extract_entities", req.ExtractEntities),
		zap.Bool("upload_to_drive", req.UploadToDrive),
	)

	// Execute full pipeline
	result, err := h.service.ExecuteFullPipeline(c.Request.Context(), &req)
	if err != nil {
		logger.Error("Stock orchestration failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// OrchestrateBatch handles multiple queries
func (h *StockOrchestratorHandler) OrchestrateBatch(c *gin.Context) {
	var req struct {
		Queries []stockorchestrator.StockOrchestratorRequest `json:"queries" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	results := make([]gin.H, 0, len(req.Queries))
	for i, q := range req.Queries {
		result, err := h.service.ExecuteFullPipeline(c.Request.Context(), &q)
		status := "success"
		if err != nil {
			status = "failed"
			logger.Error("Batch orchestration failed", zap.Int("index", i), zap.Error(err))
		}

		results = append(results, gin.H{
			"index":  i,
			"query":  q.Query,
			"status": status,
			"result": result,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"results": results,
		"count":   len(results),
	})
}
