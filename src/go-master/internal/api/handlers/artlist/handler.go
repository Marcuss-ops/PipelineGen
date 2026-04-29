package artlist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/artlist"
)

type Handler struct {
	service        *artlist.Service
	nodeScraperDir string
	log            *zap.Logger
}

func NewHandler(
	service *artlist.Service,
	nodeScraperDir string,
	log *zap.Logger,
) *Handler {
	return &Handler{
		service:        service,
		nodeScraperDir: nodeScraperDir,
		log:            log,
	}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering Artlist routes")
	r.GET("/stats", h.Stats)
	r.POST("/search", h.Search)
	r.POST("/sync", h.Sync)
	r.POST("/sync-drive-folder", h.SyncDriveFolder)
	r.POST("/reindex", h.Reindex)
	r.POST("/purge-stale", h.PurgeStale)
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true, "message": "test endpoint works"})
	})

	// Clip lifecycle endpoints
	r.GET("/clips/:id/status", h.GetClipStatus)
	r.POST("/clips/:id/download", h.DownloadClip)
	r.POST("/clips/:id/upload-drive", h.UploadClipToDrive)
	r.POST("/clips/process", h.ProcessClip)
}

// Stats returns statistics about Artlist clips and search terms
func (h *Handler) Stats(c *gin.Context) {
	ctx := c.Request.Context()

	stats, err := h.service.GetStats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("failed to get stats: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// Search searches for Artlist clips
func (h *Handler) Search(c *gin.Context) {
	var req artlist.SearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("invalid request: %v", err),
		})
		return
	}

	if strings.TrimSpace(req.Term) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "term is required",
		})
		return
	}

	if req.Limit <= 0 {
		req.Limit = 8
	}
	if req.Limit > 50 {
		req.Limit = 50
	}

	resp, err := h.service.Search(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("search failed: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// SyncDriveFolder syncs a Google Drive folder to the database
func (h *Handler) SyncDriveFolder(c *gin.Context) {
	var req struct {
		FolderID  string `json:"folder_id" binding:"required"`
		MediaType string `json:"media_type" default:"stock"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request: " + err.Error()})
		return
	}

	if req.MediaType == "" {
		req.MediaType = "stock"
	}

	resp, err := h.service.SyncDriveFolder(c.Request.Context(), req.FolderID, req.MediaType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// searchLive performs a live search using the Node.js scraper
func (h *Handler) searchLive(ctx context.Context, term string, limit int, saveDB bool) ([]map[string]interface{}, error) {
	if strings.TrimSpace(h.nodeScraperDir) == "" {
		return nil, fmt.Errorf("node scraper directory is not configured")
	}

	scraperDir := h.nodeScraperDir
	if absDir, err := filepath.Abs(scraperDir); err == nil {
		scraperDir = absDir
	}
	scriptPath := filepath.Join(scraperDir, "artlist_search.js")

	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	args := []string{
		scriptPath,
		"--term", term,
		"--limit", strconv.Itoa(limit),
	}
	if saveDB {
		args = append(args, "--save-db")
	}

	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = scraperDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("scraper failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return nil, fmt.Errorf("failed to decode scraper response: %w", err)
	}

	// Extract clips from the response
	clipsRaw, ok := payload["clips"]
	if !ok {
		return []map[string]interface{}{}, nil
	}

	clipsJSON, err := json.Marshal(clipsRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal clips: %w", err)
	}

	var clips []map[string]interface{}
	if err := json.Unmarshal(clipsJSON, &clips); err != nil {
		return nil, fmt.Errorf("failed to unmarshal clips: %w", err)
	}

	return clips, nil
}

// Sync syncs Artlist clips for given terms
func (h *Handler) Sync(c *gin.Context) {
	var req artlist.SyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": fmt.Sprintf("invalid request: %v", err),
		})
		return
	}

	if req.Limit <= 0 {
		req.Limit = 20
	}

	ctx := c.Request.Context()

	resp, err := h.service.Sync(ctx, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// Reindex rebuilds local indexes
func (h *Handler) Reindex(c *gin.Context) {
	var req artlist.ReindexRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Mode = "tags" // default mode
	}

	ctx := c.Request.Context()

	resp, err := h.service.Reindex(ctx, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// PurgeStale removes stale data
func (h *Handler) PurgeStale(c *gin.Context) {
	var req artlist.PurgeStaleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req.OlderThanDays = 30 // default
		req.DryRun = true
	}

	ctx := c.Request.Context()

	resp, err := h.service.PurgeStale(ctx, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetClipStatus returns the status of a clip
func (h *Handler) GetClipStatus(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "clip id is required"})
		return
	}

	resp, err := h.service.GetClipStatus(c.Request.Context(), clipID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// DownloadClip downloads a clip from Artlist
func (h *Handler) DownloadClip(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "clip id is required"})
		return
	}

	var req artlist.DownloadClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = artlist.DownloadClipRequest{}
	}

	resp, err := h.service.DownloadClip(c.Request.Context(), clipID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// UploadClipToDrive uploads a clip to Google Drive
func (h *Handler) UploadClipToDrive(c *gin.Context) {
	clipID := c.Param("id")
	if clipID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "clip id is required"})
		return
	}

	var req artlist.UploadClipToDriveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Ignore bind error, use empty request
	}

	resp, err := h.service.UploadClipToDrive(c.Request.Context(), clipID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ProcessClip processes a clip: search → download → upload to Drive → update DB
func (h *Handler) ProcessClip(c *gin.Context) {
	var req artlist.ProcessClipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid request"})
		return
	}

	if req.ClipID == "" && req.Term == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "clip_id or term is required"})
		return
	}

	resp, err := h.service.ProcessClip(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}
