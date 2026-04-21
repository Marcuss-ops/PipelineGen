// Package handlers provides HTTP handlers for stock video search endpoints.
package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/stock"
	"velox/go-master/pkg/logger"
)

// StockSearchHandler handles stock video search endpoints
type StockSearchHandler struct {
	manager     *stock.StockManager
	downloadDir string
}

// NewStockSearchHandler creates a new stock search handler
func NewStockSearchHandler(manager *stock.StockManager) *StockSearchHandler {
	return NewStockSearchHandlerWithDownloadDir(manager, "/tmp/velox/downloads")
}

// NewStockSearchHandlerWithDownloadDir creates a stock search handler with a custom download directory
func NewStockSearchHandlerWithDownloadDir(manager *stock.StockManager, downloadDir string) *StockSearchHandler {
	if downloadDir == "" {
		downloadDir = "/tmp/velox/downloads"
	}
	return &StockSearchHandler{manager: manager, downloadDir: downloadDir}
}

// RegisterRoutes registers stock search routes
func (h *StockSearchHandler) RegisterRoutes(rg *gin.RouterGroup) {
	s := rg.Group("/stock")
	{
		s.GET("/search", h.Search)
		s.GET("/search/youtube", h.SearchYouTube)
		s.GET("/search/youtube/top", h.SearchYouTubeTop)
		s.POST("/search/download", h.DownloadVideo)
	}
}

// Search searches for stock videos
func (h *StockSearchHandler) Search(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Query parameter 'q' is required",
		})
		return
	}

	maxResults := 20
	if max := c.Query("max"); max != "" {
		if parsed, err := strconv.Atoi(max); err == nil && parsed > 0 {
			maxResults = parsed
		}
	}

	// Search stock videos
	results, err := h.manager.SearchYouTube(c.Request.Context(), query, maxResults)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Search failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"query":   query,
		"results": results,
		"count":   len(results),
	})
}

// SearchYouTube searches YouTube for videos
func (h *StockSearchHandler) SearchYouTube(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Query parameter 'q' is required",
		})
		return
	}

	maxResults := 10
	if max := c.Query("max"); max != "" {
		if parsed, err := strconv.Atoi(max); err == nil && parsed > 0 {
			maxResults = parsed
		}
	}

	logger.Info("YouTube search requested",
		zap.String("query", query),
		zap.Int("max_results", maxResults),
	)

	results, err := h.manager.SearchYouTube(c.Request.Context(), query, maxResults)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "YouTube search failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"query":    query,
		"results":  results,
		"count":    len(results),
		"platform": "youtube",
	})
}

// SearchYouTubeTop searches YouTube by keyword and returns the most viewed videos in a time window.
// Query params:
// - q: required keyword
// - max: optional (default 10, max 50)
// - period: optional one of hour|today|week|month|year (default week)
// - duration: optional one of short|medium|long
func (h *StockSearchHandler) SearchYouTubeTop(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Query parameter 'q' is required"})
		return
	}

	maxResults := 10
	if max := c.Query("max"); max != "" {
		if parsed, err := strconv.Atoi(max); err == nil && parsed > 0 {
			maxResults = parsed
		}
	}
	if maxResults > 50 {
		maxResults = 50
	}

	period := c.DefaultQuery("period", "week")
	duration := c.Query("duration")
	logger.Info("YouTube top search requested",
		zap.String("query", query),
		zap.Int("max_results", maxResults),
		zap.String("period", period),
		zap.String("duration", duration),
	)

	results, err := h.manager.SearchYouTubeWithOptions(c.Request.Context(), query, stock.SearchYouTubeOptions{
		MaxResults: maxResults,
		SortBy:     "views",
		UploadDate: period,
		Duration:   duration,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "YouTube top search failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"query":     query,
		"period":    period,
		"duration":  duration,
		"results":   results,
		"count":     len(results),
		"platform":  "youtube",
		"sorted_by": "views",
	})
}

// StockDownloadRequest represents a stock video download request
type StockDownloadRequest struct {
	URL       string `json:"url" binding:"required"`
	ProjectID string `json:"project_id"`
	Quality   string `json:"quality"`
}

// DownloadVideo downloads a video video
func (h *StockSearchHandler) DownloadVideo(c *gin.Context) {
	var req StockDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	if req.Quality == "" {
		req.Quality = "best"
	}

	logger.Info("Video download requested",
		zap.String("url", req.URL),
		zap.String("project_id", req.ProjectID),
		zap.String("quality", req.Quality),
	)

	// Download the video
	outputPath, err := h.manager.DownloadVideo(
		c.Request.Context(),
		req.URL,
		h.downloadDir,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Download failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":            true,
		"file":          outputPath,
		"quality":       req.Quality,
		"downloaded_at": time.Now(),
	})
}
