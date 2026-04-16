// Package handlers provides HTTP handlers for clip index management endpoints.
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/storage/jsondb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// ClipIndexHandler handles clip index HTTP requests
type ClipIndexHandler struct {
	indexer       *clip.Indexer
	suggester     *clip.SemanticSuggester
	indexStore    *jsondb.ClipIndexStore
	driveClient   *drive.Client
	rootFolderID  string
	credentialsFile string
	tokenFile     string
	scanner       *clip.IndexScanner // Periodic scanner for auto-reindexing
}

// NewClipIndexHandler creates a new clip index handler
func NewClipIndexHandler(rootFolderID, credentialsFile, tokenFile string, indexStore *jsondb.ClipIndexStore, artlistSrc *clip.ArtlistSource) *ClipIndexHandler {
	h := &ClipIndexHandler{
		rootFolderID:    rootFolderID,
		credentialsFile: credentialsFile,
		tokenFile:       tokenFile,
		indexStore:      indexStore,
	}

	// Initialize Drive client
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := h.initDriveClient(ctx); err != nil {
		logger.Warn("Failed to initialize Drive client for indexer", zap.Error(err))
		// Continue without Drive client - can still serve cached index
	} else {
		// Create indexer
		h.indexer = clip.NewIndexer(h.driveClient, rootFolderID)

		// Set Artlist source if available
		if artlistSrc != nil {
			h.indexer.SetArtlistSource(artlistSrc)
			logger.Info("Artlist source enabled for unified clip suggestions")
		}

		// Load existing index from storage
		if existingIndex, err := indexStore.LoadIndex(); err == nil && existingIndex != nil {
			h.indexer.SetIndex(existingIndex)
			logger.Info("Loaded existing clip index",
				zap.Int("clips", len(existingIndex.Clips)),
				zap.Int("folders", len(existingIndex.Folders)))
		}

		// Create semantic suggester
		h.suggester = clip.NewSemanticSuggester(h.indexer)
	}

	return h
}

// GetIndexer returns the clip indexer instance (may be nil if not initialized)
func (h *ClipIndexHandler) GetIndexer() *clip.Indexer {
	return h.indexer
}

// SetScanner sets the periodic index scanner
func (h *ClipIndexHandler) SetScanner(scanner *clip.IndexScanner) {
	h.scanner = scanner
}

// initDriveClient initializes the Google Drive client
func (h *ClipIndexHandler) initDriveClient(ctx context.Context) error {
	credsFile := h.credentialsFile
	if credsFile == "" {
		credsFile = "credentials.json"
	}

	tokenFile := h.tokenFile
	if tokenFile == "" {
		tokenFile = "token.json"
	}

	config := drive.Config{
		CredentialsFile: credsFile,
		TokenFile:       tokenFile,
		Scopes: []string{
			"https://www.googleapis.com/auth/drive",
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/drive.readonly",
		},
	}

	client, err := drive.NewClient(ctx, config)
	if err != nil {
		return err
	}

	h.driveClient = client
	return nil
}

// RegisterRoutes registers clip index routes (protected — requires auth)
func (h *ClipIndexHandler) RegisterRoutes(rg *gin.RouterGroup) {
	clipIndexGroup := rg.Group("/clip/index")
	{
		// Index management (write operations)
		clipIndexGroup.POST("/scan", h.TriggerScan)
		clipIndexGroup.POST("/scan/incremental", h.IncrementalScan)
		clipIndexGroup.DELETE("/clear", h.ClearIndex)

		// Write suggestions
		clipIndexGroup.POST("/suggest/script", h.SuggestForScript)

		// Cache management
		clipIndexGroup.POST("/cache/clear", h.ClearCache)
	}
}

// RegisterPublicRoutes registers read-only clip index routes (no auth required)
func (h *ClipIndexHandler) RegisterPublicRoutes(rg *gin.RouterGroup) {
	clipIndexGroup := rg.Group("/clip/index")
	{
		// Read-only endpoints
		clipIndexGroup.GET("/stats", h.GetStats)
		clipIndexGroup.GET("/status", h.GetStatus)
		clipIndexGroup.GET("/scanner/status", h.GetScannerStatus)

		// Search and list clips
		clipIndexGroup.POST("/search", h.Search)
		clipIndexGroup.GET("/clips", h.ListClips)
		clipIndexGroup.GET("/clips/:id", h.GetClip)

		// Semantic suggestions (read-only — sentence level)
		clipIndexGroup.POST("/suggest/sentence", h.SuggestForSentence)

		// Similar clips
		clipIndexGroup.POST("/similar", h.SimilarClips)

		// Cache status
		clipIndexGroup.GET("/cache", h.CacheStatus)
	}

	// Public scan endpoints (separate path to avoid conflict with protected routes)
	publicScan := rg.Group("/clip/public")
	{
		publicScan.POST("/scan", h.TriggerScan)
		publicScan.POST("/scan/incremental", h.IncrementalScan)
	}
}

// TriggerScan godoc
// @Summary Trigger a full clip index scan
// @Description Scan Google Drive and rebuild the clip index. Accessible from remote machines.
// @Tags clip-index
// @Accept json
// @Produce json
// @Param force query bool false "Force scan even if recently synced"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /clip/index/scan [post]
func (h *ClipIndexHandler) TriggerScan(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized. Check Drive credentials.",
		})
		return
	}

	// Use scanner if available for consistent results tracking
	if h.scanner != nil {
		result := h.scanner.TriggerManualScan(c.Request.Context())
		if !result.Success {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": result.Error,
				"scan_result": result,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":          true,
			"message":     "Clip index scan completed successfully",
			"stats":       h.indexer.GetStats(),
			"last_sync":   h.indexer.GetLastSync(),
			"scan_result": result,
		})
		return
	}

	force := c.DefaultQuery("force", "false") == "true"

	// Check if scan is needed
	if !force && !h.indexer.NeedsSync(1*time.Hour) {
		lastSync := h.indexer.GetLastSync()
		c.JSON(http.StatusOK, gin.H{
			"ok":        true,
			"message":   "Index is recent, skipping scan",
			"last_sync": lastSync,
			"stats":     h.indexer.GetStats(),
		})
		return
	}

	// Run scan
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	err := h.indexer.ScanAndIndex(ctx)
	if err != nil {
		logger.Error("Failed to scan Drive for clips", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to scan Drive: " + err.Error(),
		})
		return
	}

	// Save to disk
	index := h.indexer.GetIndex()
	if err := h.indexStore.SaveIndex(index); err != nil {
		logger.Error("Failed to save clip index", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to save index: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Clip index scan completed successfully",
		"stats":   h.indexer.GetStats(),
		"last_sync": h.indexer.GetLastSync(),
	})
}

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
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	stats := h.indexer.GetStats()
	lastSync := h.indexer.GetLastSync()

	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"stats":     stats,
		"last_sync": lastSync,
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
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":         false,
			"initialized": false,
			"message":    "Clip indexer not initialized",
		})
		return
	}

	stats := h.indexer.GetStats()
	lastSync := h.indexer.GetLastSync()
	needsSync := h.indexer.NeedsSync(1*time.Hour)

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"initialized": true,
		"last_sync":   lastSync,
		"needs_sync":  needsSync,
		"total_clips": stats.TotalClips,
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
		"ok":            true,
		"scanner":       "active",
		"last_scan":     lastResult,
		"auto_enabled":  true,
	}

	c.JSON(http.StatusOK, status)
}

// ClearIndex godoc
// @Summary Clear the clip index
// @Description Clear the current clip index and delete from disk. Accessible from remote machines.
// @Tags clip-index
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /clip/index/clear [delete]
func (h *ClipIndexHandler) ClearIndex(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	// Clear from memory
	h.indexer.SetIndex(&clip.ClipIndex{
		Version:      "1.0",
		RootFolderID: h.rootFolderID,
		Stats: clip.IndexStats{
			ClipsByGroup: make(map[string]int),
		},
	})

	// Delete from disk
	if err := h.indexStore.DeleteIndex(); err != nil {
		logger.Error("Failed to delete clip index file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to delete index file: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Clip index cleared successfully",
	})
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
		c.JSON(http.StatusServiceUnavailable, gin.H{
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
		c.JSON(http.StatusServiceUnavailable, gin.H{
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

// Helper: parse float query parameter
func parseFloatQuery(c *gin.Context, key string, defaultVal float64) float64 {
	if val := c.Query(key); val != "" {
		var result float64
		if _, err := fmt.Sscanf(val, "%f", &result); err == nil {
			return result
		}
	}
	return defaultVal
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
		c.JSON(http.StatusServiceUnavailable, gin.H{
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

// SuggestForSentence godoc
// @Summary Get clip suggestions for a sentence
// @Description Get intelligent clip suggestions for a sentence from your script. This is the main endpoint for semantic matching.
// @Tags clip-suggestions
// @Accept json
// @Produce json
// @Param request body clip.SentenceSuggestRequest true "Sentence suggest request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /clip/index/suggest/sentence [post]
func (h *ClipIndexHandler) SuggestForSentence(c *gin.Context) {
	if h.suggester == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Semantic suggester not initialized",
		})
		return
	}

	var req clip.SentenceSuggestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Apply defaults
	if req.MaxResults == 0 {
		req.MaxResults = 10
	}
	if req.MinScore < 0 {
		req.MinScore = 20 // Minimum threshold
	}

	// Get suggestions
	suggestions := h.suggester.SuggestForSentence(
		c.Request.Context(),
		req.Sentence,
		req.MaxResults,
		req.MinScore,
		req.MediaType,
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"sentence":    req.Sentence,
		"suggestions": suggestions,
		"total":       len(suggestions),
		"best_score":  getBestScore(suggestions),
	})
}

// SuggestForScript godoc
// @Summary Get clip suggestions for an entire script
// @Description Process an entire script and get clip suggestions for each sentence. Accessible from remote machines.
// @Tags clip-suggestions
// @Accept json
// @Produce json
// @Param request body clip.ScriptSuggestRequest true "Script suggest request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /clip/index/suggest/script [post]
func (h *ClipIndexHandler) SuggestForScript(c *gin.Context) {
	if h.suggester == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Semantic suggester not initialized",
		})
		return
	}

	var req clip.ScriptSuggestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Apply defaults
	if req.MaxResultsPerSentence == 0 {
		req.MaxResultsPerSentence = 5
	}
	if req.MinScore < 0 {
		req.MinScore = 20
	}

	// Get suggestions for entire script
	suggestions := h.suggester.SuggestForScript(
		c.Request.Context(),
		req.Script,
		req.MaxResultsPerSentence,
		req.MinScore,
		req.MediaType,
	)

	// Calculate summary
	totalSentences := len(suggestions)
	totalClips := 0
	for _, s := range suggestions {
		totalClips += len(s.Suggestions)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":                 true,
		"script_length":      len(req.Script),
		"sentences_with_clips": totalSentences,
		"total_clip_suggestions": totalClips,
		"suggestions":        suggestions,
	})
}

// Helper function to get best score from suggestions
func getBestScore(suggestions []clip.SuggestionResult) float64 {
	if len(suggestions) == 0 {
		return 0
	}
	return suggestions[0].Score
}

// SimilarClips finds clips similar to a given clip
// @Summary Find similar clips
// @Description Find clips similar to a given clip by ID
// @Tags Clip Index
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/similar [post]
func (h *ClipIndexHandler) SimilarClips(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	var req clip.SimilarClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	if req.MaxResults == 0 {
		req.MaxResults = 10
	}

	results, err := h.indexer.FindSimilarClips(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to find similar clips: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"clip_id":      req.ClipID,
		"total_found":  len(results),
		"similar_clips": results,
	})
}

// CacheStatus returns cache statistics
// @Summary Get cache status
// @Description Get suggestion cache statistics
// @Tags Clip Index
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/cache [get]
func (h *ClipIndexHandler) CacheStatus(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	cache := h.indexer.GetCache()
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"cache_size": cache.Size(),
		"max_size":  500,
	})
}

// ClearCache clears the suggestion cache
// @Summary Clear cache
// @Description Clear the suggestion cache
// @Tags Clip Index
// @Post /clip/index/cache/clear
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/cache/clear [post]
func (h *ClipIndexHandler) ClearCache(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	h.indexer.GetCache().Clear()
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Cache cleared",
	})
}

// IncrementalScan runs an incremental scan
// @Summary Incremental scan
// @Description Scan only folders modified since last sync
// @Tags Clip Index
// @Post /clip/index/scan/incremental
// @Success 200 {object} map[string]interface{}
// @Router /clip/index/scan/incremental [post]
func (h *ClipIndexHandler) IncrementalScan(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Clip indexer not initialized",
		})
		return
	}

	// Use scanner if available for consistent results tracking
	if h.scanner != nil {
		result := h.scanner.TriggerIncrementalScan(c.Request.Context())
		if !result.Success {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": result.Error,
				"scan_result": result,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":                true,
			"message":           "Incremental scan completed successfully",
			"folders_updated":   result.ClipsChanged,
			"total_clips":       result.TotalClips,
			"total_folders":     result.TotalFolders,
			"stats":             h.indexer.GetStats(),
			"scan_result":       result,
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	folders, clips, err := h.indexer.IncrementalScan(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Incremental scan failed: " + err.Error(),
		})
		return
	}

	// Save to disk
	index := h.indexer.GetIndex()
	if err := h.indexStore.SaveIndex(index); err != nil {
		logger.Error("Failed to save clip index", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":                true,
		"folders_updated":   folders,
		"clips_net_change":  clips,
		"total_clips":       len(index.Clips),
		"total_folders":     len(index.Folders),
		"stats":             h.indexer.GetStats(),
	})
}
