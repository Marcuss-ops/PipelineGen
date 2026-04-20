// Package handlers provides HTTP handlers for clip management endpoints.
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
)

// ClipHandler handles clip-related HTTP requests
type ClipHandler struct {
	driveClient     *drive.Client
	suggester       *clip.Suggester
	rootFolderID    string
	credentialsFile string
	tokenFile       string
	mu              sync.Mutex    // Protects suggester and driveClient lazy init
	indexer         *clip.Indexer // Optional: for fast suggestions from in-memory index
	clipSearch      *clipsearch.Service
	stockDB         *stockdb.StockDB
}

// NewClipHandler creates a new clip handler
func NewClipHandler(rootFolderID, credentialsFile, tokenFile string) *ClipHandler {
	if rootFolderID == "" {
		rootFolderID = "root"
	}

	h := &ClipHandler{
		rootFolderID:    rootFolderID,
		credentialsFile: credentialsFile,
		tokenFile:       tokenFile,
	}

	// Initialize Drive client if credentials exist
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.initDriveClient(ctx); err != nil {
		logger.Warn("Failed to initialize Drive client", zap.Error(err))
	}

	return h
}

// SetIndexer sets the clip indexer for fast in-memory suggestions
func (h *ClipHandler) SetIndexer(indexer *clip.Indexer) {
	h.indexer = indexer
	// If suggester already exists, set the index on it
	h.mu.Lock()
	if h.suggester != nil && indexer != nil {
		h.suggester.SetIndex(indexer.GetIndex())
	}
	h.mu.Unlock()
}

func (h *ClipHandler) SetClipSearch(s *clipsearch.Service) {
	h.clipSearch = s
}

func (h *ClipHandler) SetStockDB(s *stockdb.StockDB) {
	h.stockDB = s
}

// RegisterRoutes registers clip routes (protected — requires auth)
func (h *ClipHandler) RegisterRoutes(rg *gin.RouterGroup) {
	clipGroup := rg.Group("/clip")
	{
		// Write operations
		clipGroup.POST("/create-subfolder", h.CreateSubfolder)
		clipGroup.POST("/download", h.DownloadClip)
		clipGroup.POST("/upload", h.UploadClip)
	}
}

// RegisterPublicRoutes registers read-only clip routes (no auth required)
func (h *ClipHandler) RegisterPublicRoutes(rg *gin.RouterGroup) {
	clipGroup := rg.Group("/clip")
	{
		// Read-only endpoints
		clipGroup.POST("/search-folders", h.SearchFolders)
		clipGroup.POST("/read-folder-clips", h.ReadFolderClips)
		clipGroup.POST("/suggest", h.Suggest)
		clipGroup.POST("/subfolders", h.Subfolders)

		// Additional read endpoints
		clipGroup.GET("/health", h.Health)
		clipGroup.GET("/groups", h.GetGroups)
		clipGroup.GET("", h.GetByKeyword)
	}
}

func (h *ClipHandler) GetByKeyword(c *gin.Context) {
	keyword := c.Query("keyword")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "missing keyword"})
		return
	}
	if h.clipSearch == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "clip search service not configured"})
		return
	}
	maxClips := 3
	if raw := c.Query("max_clips"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxClips = n
		}
	}
	if env := os.Getenv("VELOX_API_MAX_CLIPS_PER_KEYWORD"); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n > 0 {
			maxClips = n
		}
	}

	results, err := h.clipSearch.SearchClipsWithOptions(c.Request.Context(), []string{keyword}, clipsearch.SearchOptions{
		ForceFresh:         true,
		MaxClipsPerKeyword: maxClips,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": err.Error()})
		return
	}
	if len(results) == 0 {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "total_clips": 0, "drive_folder_url": "", "clips": []any{}})
		return
	}
	folderURL := ""
	if results[0].FolderID != "" {
		folderURL = fmt.Sprintf("https://drive.google.com/drive/folders/%s", results[0].FolderID)
	}
	type clipResp struct {
		ID                string  `json:"id"`
		Start             float64 `json:"start"`
		End               float64 `json:"end"`
		Score             float64 `json:"score"`
		TranscriptSnippet string  `json:"transcript_snippet"`
		ThumbnailURL      string  `json:"thumbnail_url"`
		DriveURL          string  `json:"drive_url"`
	}
	clips := make([]clipResp, 0, len(results))
	for _, r := range results {
		clips = append(clips, clipResp{
			ID:                r.DriveID,
			Start:             r.StartSec,
			End:               r.EndSec,
			Score:             r.Score,
			TranscriptSnippet: r.TranscriptSnippet,
			ThumbnailURL:      r.ThumbnailURL,
			DriveURL:          r.DriveURL,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"status":           "ok",
		"total_clips":      len(clips),
		"drive_folder_url": folderURL,
		"clips":            clips,
	})
}
