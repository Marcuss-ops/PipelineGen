// Package clip provides HTTP handlers for clip index management endpoints.
package clip

import (
	"context"
	"net/http"
	"time"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/storage/jsondb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
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

	driveCfg := drive.Config{
		CredentialsFile: credsFile,
		TokenFile:       tokenFile,
		Scopes: []string{
			"https://www.googleapis.com/auth/drive",
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/drive.readonly",
		},
	}

	client, err := drive.NewClient(ctx, driveCfg)
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

// GetStats returns index statistics
func (h *ClipIndexHandler) GetStats(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "Indexer not initialized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "stats": h.indexer.GetStats()})
}

// GetStatus returns index status
func (h *ClipIndexHandler) GetStatus(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "Indexer not initialized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"last_sync": h.indexer.GetLastSync(),
		"stats":     h.indexer.GetStats(),
	})
}

// GetScannerStatus returns periodic scanner status
func (h *ClipIndexHandler) GetScannerStatus(c *gin.Context) {
	if h.scanner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "Scanner not initialized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "last_scan": h.scanner.GetLastScanResult()})
}

// Search searches for clips in the index
func (h *ClipIndexHandler) Search(c *gin.Context) {
	if h.indexer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "Indexer not initialized"})
		return
	}

	var req struct {
		Query   string             `json:"query"`
		Filters clip.SearchFilters `json:"filters"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	results := h.indexer.Search(req.Query, req.Filters)
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"query":   req.Query,
		"results": results,
		"total":   len(results),
	})
}
