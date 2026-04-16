// Package handlers provides HTTP handlers for clip management endpoints.
package handlers

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// ClipHandler handles clip-related HTTP requests
type ClipHandler struct {
	driveClient    *drive.Client
	suggester      *clip.Suggester
	rootFolderID   string
	credentialsFile string
	tokenFile      string
	mu             sync.Mutex // Protects suggester and driveClient lazy init
	indexer        *clip.Indexer // Optional: for fast suggestions from in-memory index
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
	}
}
