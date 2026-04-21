package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
)

// httpStatusServiceUnavailable is a local constant for 503.
const httpStatusServiceUnavailable = 503

// GetStats godoc
// @Summary Get clip index statistics
// @Description Get statistics about the current clip index. Accessible from remote machines.
// @Tags clip-index
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /clip/index/stats [get]
func (h *ClipIndexHandler) GetStats(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	stats := h.indexer.GetStats()
	lastSync := h.indexer.GetLastSync()

	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"stats":            stats,
		"last_sync":        lastSync,
		"index_age_minutes": time.Since(lastSync).Minutes(),
	})
}

// GetStatus godoc
// @Summary Get clip indexer status
// @Description Get the current status of the clip indexer. Accessible from remote machines.
// @Tags clip-index
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/status [get]
func (h *ClipIndexHandler) GetStatus(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":          false,
			"initialized": false,
			"message":     "Clip indexer not initialized",
		})
		return
	}

	stats := h.indexer.GetStats()
	lastSync := h.indexer.GetLastSync()
	needsSync := h.indexer.NeedsSync(1 * time.Hour)

	c.JSON(http.StatusOK, gin.H{
		"ok":            true,
		"initialized":   true,
		"last_sync":     lastSync,
		"needs_sync":    needsSync,
		"total_clips":   stats.TotalClips,
		"total_folders": stats.TotalFolders,
		"clips_by_group": stats.ClipsByGroup,
	})
}

// GetScannerStatus returns the periodic scanner status
func (h *ClipIndexHandler) GetScannerStatus(c *gin.Context) {
	if h.scanner == nil {
		c.JSON(http.StatusOK, gin.H{
			"ok":      true,
			"scanner": "not_configured",
			"message": "Auto-reindexing is not enabled",
		})
		return
	}

	lastResult := h.scanner.GetLastScanResult()
	status := gin.H{
		"ok":           true,
		"scanner":      "active",
		"last_scan":    lastResult,
		"auto_enabled": true,
	}

	c.JSON(http.StatusOK, status)
}

// Search godoc
// @Summary Search clip index
// @Description Search the clip index with filters. Accessible from remote machines.
// @Tags clip-index
// @Accept json
// @Produce json
// @Param request body clip.SearchRequest true "Search request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /clip/index/search [post]
func (h *ClipIndexHandler) Search(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	var req clip.SearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Apply defaults
	if req.MaxResults == 0 {
		req.MaxResults = 50
	}
	if req.MinScore < 0 {
		req.MinScore = 0
	}

	// Search
	filters := clip.SearchFilters{
		Group:       req.Group,
		MediaType:   req.MediaType,
		FolderID:    req.FolderID,
		MinDuration: req.MinDuration,
		MaxDuration: req.MaxDuration,
		Resolution:  req.Resolution,
		Tags:        req.Tags,
		Limit:       req.MaxResults,
		Offset:      req.Offset,
	}

	results := h.indexer.Search(req.Query, filters)

	// Apply offset and limit
	if req.Offset > 0 && req.Offset < len(results) {
		results = results[req.Offset:]
	}
	if req.MaxResults > 0 && req.MaxResults < len(results) {
		results = results[:req.MaxResults]
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"query":   req.Query,
		"results": results,
		"total":   len(results),
		"filters": filters,
	})
}

// ListClips godoc
// @Summary List all indexed clips
// @Description List all clips in the index with optional filtering. Accessible from remote machines.
// @Tags clip-index
// @Accept json
// @Produce json
// @Param group query string false "Filter by group"
// @Param media_type query string false "Filter by media type: clip or stock"
// @Param query query string false "Search query (matches name, tags, folder)"
// @Param min_duration query number false "Minimum duration in milliseconds"
// @Param max_duration query number false "Maximum duration in milliseconds"
// @Param resolution query string false "Filter by resolution (e.g. 1920x1080)"
// @Param tag query string false "Filter by tag (can be repeated for multiple tags)"
// @Param limit query int false "Max results" default(100)
// @Param offset query int false "Offset" default(0)
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/clips [get]
func (h *ClipIndexHandler) ListClips(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	// Parse query parameters
	group := c.DefaultQuery("group", "")
	mediaType := c.DefaultQuery("media_type", "")
	query := c.DefaultQuery("query", "")
	resolution := c.DefaultQuery("resolution", "")
	minDuration := parseFloatQuery(c, "min_duration", 0)
	maxDuration := parseFloatQuery(c, "max_duration", 0)
	limit := parseIntQuery(c, "limit", 100)
	offset := parseIntQuery(c, "offset", 0)

	// Parse tags (can be repeated: ?tag=nature&tag=4k)
	tags := c.QueryArray("tag")

	// Build filters
	filters := clip.SearchFilters{
		Group:       group,
		MediaType:   mediaType,
		MinDuration: minDuration,
		MaxDuration: maxDuration,
		Resolution:  resolution,
		Tags:        tags,
		Limit:       0, // We handle pagination manually
		Offset:      0,
	}

	// Search (uses indexer search which supports query + filters)
	results := h.indexer.Search(query, filters)

	totalCount := len(results)

	// Apply pagination
	if offset > 0 && offset < len(results) {
		results = results[offset:]
	}
	if limit > 0 && limit < len(results) {
		results = results[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"clips":       results,
		"total":       len(results),
		"total_count": totalCount,
		"group":       group,
		"media_type":  mediaType,
		"query":       query,
		"limit":       limit,
		"offset":      offset,
		"filters": gin.H{
			"resolution":   resolution,
			"min_duration": minDuration,
			"max_duration": maxDuration,
			"tags":         tags,
			"media_type":   mediaType,
		},
	})
}

// GetClip godoc
// @Summary Get a specific clip by ID
// @Description Get details of a specific clip from the index. Accessible from remote machines.
// @Tags clip-index
// @Param id path string true "Clip ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /clip/index/clips/{id} [get]
func (h *ClipIndexHandler) GetClip(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(httpStatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	clipID := c.Param("id")
	index := h.indexer.GetIndex()

	for _, clip := range index.Clips {
		if clip.ID == clipID {
			c.JSON(http.StatusOK, gin.H{
				"ok":   true,
				"clip": clip,
			})
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{
		"ok":      false,
		"error":   "Clip not found",
		"clip_id": clipID,
	})
}

// parseFloatQuery helper: parse float query parameter
func parseFloatQuery(c *gin.Context, key string, defaultVal float64) float64 {
	if val := c.Query(key); val != "" {
		var result float64
		if _, err := fmt.Sscanf(val, "%f", &result); err == nil {
			return result
		}
	}
	return defaultVal
}

// parseIntQuery helper: parse int query parameter
func parseIntQuery(c *gin.Context, key string, defaultVal int) int {
	if val := c.Query(key); val != "" {
		var result int
		if _, err := fmt.Sscanf(val, "%d", &result); err == nil {
			return result
		}
	}
	return defaultVal
}
