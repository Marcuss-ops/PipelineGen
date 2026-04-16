package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/service/channelmonitor"
	"velox/go-master/internal/youtube"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// ChannelMonitorHandler handles HTTP requests for the channel monitor
type ChannelMonitorHandler struct {
	monitor     *channelmonitor.Monitor
	ytClient    youtube.Client
	driveClient *drive.Client
	ollamaURL   string
	mu          sync.RWMutex // protects monitor re-creation
}

// NewChannelMonitorHandler creates a new handler
func NewChannelMonitorHandler(monitor *channelmonitor.Monitor, ytClient youtube.Client, driveClient *drive.Client, ollamaURL string) *ChannelMonitorHandler {
	return &ChannelMonitorHandler{
		monitor:     monitor,
		ytClient:    ytClient,
		driveClient: driveClient,
		ollamaURL:   ollamaURL,
	}
}

// RegisterRoutes registers all monitor endpoints
func (h *ChannelMonitorHandler) RegisterRoutes(r *gin.RouterGroup) {
	monitorGroup := r.Group("/monitor")
	{
		monitorGroup.POST("/run", h.RunOnce)
		monitorGroup.POST("/process-video", h.ProcessSingleVideo)
		monitorGroup.GET("/status", h.GetStatus)
		monitorGroup.GET("/config", h.GetConfig)
		monitorGroup.GET("/processed", h.GetProcessedVideos)
	}
}

// ProcessSingleVideoRequest represents a request to process a single video
type ProcessSingleVideoRequest struct {
	YouTubeURL string `json:"youtube_url" binding:"required"`
	Category   string `json:"category"`   // optional override
	MaxClips   int    `json:"max_clips"`  // optional, default 5
}

// ProcessSingleVideo godoc
// @Summary Process a single YouTube video
// @Description Extract highlights, find protagonist, download clips, upload to Drive
// @Tags monitor
// @Accept json
// @Produce json
// @Param request body ProcessSingleVideoRequest true "Video URL and options"
// @Success 200 {object} map[string]interface{}
// @Router /monitor/process-video [post]
func (h *ChannelMonitorHandler) ProcessSingleVideo(c *gin.Context) {
	var req ProcessSingleVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "youtube_url is required"})
		return
	}

	// Extract video ID
	videoID := extractVideoIDFromURL(req.YouTubeURL)
	if videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid YouTube URL"})
		return
	}

	// Use background context for async processing (request context dies after response)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	log := logger.Get()
	log.Info("Processing single video",
		zap.String("url", req.YouTubeURL),
		zap.String("video_id", videoID),
	)

	// Send immediate acknowledgment
	c.JSON(http.StatusAccepted, gin.H{
		"ok":       true,
		"video_id": videoID,
		"status":   "processing",
		"message":  "Video processing started. Check logs for progress.",
	})

	// Process in background
	go func() {
		h.processVideoAsync(ctx, videoID, req, log)
	}()
}

func (h *ChannelMonitorHandler) processVideoAsync(ctx context.Context, videoID string, req ProcessSingleVideoRequest, log *zap.Logger) {
	startTime := time.Now()
	log.Info("=== ASYNC PROCESSING STARTED ===",
		zap.String("video_id", videoID),
	)

	// Get video metadata with its own timeout
	metaCtx, metaCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	videoInfo, err := h.ytClient.GetVideo(metaCtx, videoID)
	metaCancel()

	var videoTitle string
	if err != nil {
		// Fallback: get title via yt-dlp --dump-json with its own timeout
		log.Warn("API GetVideo failed, using yt-dlp fallback", zap.Error(err))
		dlCtx, dlCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		videoTitle = h.getVideoTitleFallback(dlCtx, videoID)
		dlCancel()
		log.Info("Fallback title extracted", zap.String("title", videoTitle))
	} else {
		videoTitle = videoInfo.Title
	}

	// Determine category
	category := req.Category
	if category == "" {
		category = "HipHop" // default for unknown
	}

	// Get channel config for defaults
	cfg, _ := channelmonitor.LoadConfigWithDefaults("data/channel_monitor_config.json")
	maxClipDuration := cfg.MaxClipDuration
	if maxClipDuration == 0 {
		maxClipDuration = 60
	}

	// Build a minimal channel config for this video
	chConfig := channelmonitor.ChannelConfig{
		URL:             "",
		Category:        category,
		Keywords:        []string{},
		MinViews:        0,
		MaxClipDuration: maxClipDuration,
	}

	// Get monitor reference
	h.mu.RLock()
	monitor := h.monitor
	h.mu.RUnlock()

	if monitor == nil {
		log.Error("Monitor not initialized")
		return
	}

	// Extract transcript with its own timeout
	transCtx, transCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	transcript, err := monitor.ExtractTranscript(transCtx, videoID)
	transCancel()
	if err != nil {
		log.Warn("Transcript extraction failed", zap.Error(err))
		return
	}
	log.Info("Transcript extracted", zap.Int("length", len(transcript)))

	// Find highlights
	highlights := monitor.FindHighlights(transcript)
	log.Info("Highlights found", zap.Int("count", len(highlights)))

	// Resolve folder with its own timeout
	folderCtx, folderCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	folderPath, folderID, folderExisted, err := monitor.ResolveFolder(folderCtx, chConfig, videoTitle)
	folderCancel()
	if err != nil {
		log.Warn("Folder resolution failed", zap.Error(err))
		return
	}
	log.Info("Folder resolved", zap.String("path", folderPath), zap.String("id", folderID))

	// Download and upload clips with its own timeout
	clipCtx, clipCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	clips, err := monitor.DownloadAndUploadClips(clipCtx, youtube.SearchResult{
		ID:    videoID,
		Title: videoTitle,
	}, highlights, folderID, folderPath, folderExisted, maxClipDuration)
	clipCancel()
	if err != nil {
		log.Warn("Clip download/upload failed", zap.Error(err))
	}

	log.Info("=== ASYNC PROCESSING COMPLETE ===",
		zap.String("video_id", videoID),
		zap.String("title", videoTitle),
		zap.Int("highlights", len(highlights)),
		zap.Int("clips_uploaded", len(clips)),
		zap.String("folder", folderPath),
		zap.String("folder_url", drive.GetFolderLink(folderID)),
		zap.Duration("duration", time.Since(startTime)),
	)
}

// Helper functions
func extractVideoIDFromURL(url string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:v=|/v/|/embed/|youtu\.be/)([a-zA-Z0-9_-]{11})`),
	}
	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(url)
		if len(matches) > 1 {
			return matches[1]
		}
	}
	return ""
}

// getVideoTitleFallback uses yt-dlp to get video title when API fails
func (h *ChannelMonitorHandler) getVideoTitleFallback(ctx context.Context, videoID string) string {
	ytdlpPath := h.findYtDlpLocal()
	if ytdlpPath == "" {
		return fmt.Sprintf("Video_%s", videoID)
	}
	url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	cmd := exec.CommandContext(ctx, ytdlpPath, "--dump-json", "--no-warnings", url)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log := logger.Get()
		log.Warn("yt-dlp title fallback failed", zap.Error(err))
		return fmt.Sprintf("Video_%s", videoID)
	}

	var info map[string]interface{}
	if err := json.Unmarshal(output, &info); err != nil {
		return fmt.Sprintf("Video_%s", videoID)
	}

	if title, ok := info["title"].(string); ok {
		return title
	}
	return fmt.Sprintf("Video_%s", videoID)
}

// RunOnceRequest represents a manual run request
type RunOnceRequest struct {
	ChannelURL string `json:"channel_url"` // optional: run only for specific channel
	MaxVideos  int    `json:"max_videos"`   // optional: override max videos per channel
}

// RunOnce godoc
// @Summary Run channel monitor manually
// @Description Trigger a single monitoring cycle
// @Tags monitor
// @Accept json
// @Produce json
// @Param request body RunOnceRequest false "Optional: specify channel URL or max videos"
// @Success 200 {object} map[string]interface{}
// @Router /monitor/run [post]
func (h *ChannelMonitorHandler) RunOnce(c *gin.Context) {
	h.mu.RLock()
	monitor := h.monitor
	h.mu.RUnlock()

	if monitor == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Monitor not initialized"})
		return
	}

	var req RunOnceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Ignore bind errors — body is optional
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	results, err := monitor.RunOnce(ctx)
	if err != nil {
		logger.Error("Monitor run failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	// Build summary
	type VideoSummary struct {
		VideoID      string `json:"video_id"`
		Title        string `json:"title"`
		Channel      string `json:"channel"`
		Highlights   int    `json:"highlights"`
		ClipsCount   int    `json:"clips_count"`
		FolderPath   string `json:"folder_path"`
	}

	var summaries []VideoSummary
	for _, r := range results {
		summaries = append(summaries, VideoSummary{
			VideoID:    r.VideoID,
			Title:      r.Title,
			Channel:    r.Channel,
			Highlights: len(r.Highlights),
			ClipsCount: len(r.Clips),
			FolderPath: r.FolderPath,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"videos_processed": len(results),
		"videos":         summaries,
	})
}

// GetStatus godoc
// @Summary Get monitor status
// @Description Get current monitor configuration and last run info
// @Tags monitor
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /monitor/status [get]
func (h *ChannelMonitorHandler) GetStatus(c *gin.Context) {
	h.mu.RLock()
	monitor := h.monitor
	h.mu.RUnlock()

	if monitor == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Monitor not initialized"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"status": gin.H{
			"running": true,
			"channels_configured": 3, // will be dynamic later
			"last_run": "see logs for details",
		},
	})
}

// GetConfig godoc
// @Summary Get monitor configuration
// @Description Get current channel monitor configuration
// @Tags monitor
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /monitor/config [get]
func (h *ChannelMonitorHandler) GetConfig(c *gin.Context) {
	cfg, err := channelmonitor.LoadConfigWithDefaults("data/channel_monitor_config.json")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"ok": true,
			"config": gin.H{
				"channels": []string{},
				"message": "No config file found, using defaults",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"config": gin.H{
			"channels":             cfg.Channels,
			"check_interval":       cfg.CheckInterval.String(),
			"ytdlp_path":          cfg.YtDlpPath,
			"cookies_path":        cfg.CookiesPath,
			"max_clip_duration":   cfg.MaxClipDuration,
			"ollama_url":          cfg.OllamaURL,
		},
	})
}

// GetProcessedVideos godoc
// @Summary Get list of processed videos
// @Description Get all videos that have been processed by the monitor
// @Tags monitor
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /monitor/processed [get]
func (h *ChannelMonitorHandler) GetProcessedVideos(c *gin.Context) {
	h.mu.RLock()
	monitor := h.monitor
	h.mu.RUnlock()

	if monitor == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Monitor not initialized"})
		return
	}

	// Return the processed videos log file content
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"processed_videos_file": "data/channel_monitor_processed.json",
		"message": "Check data/channel_monitor_processed.json for full list",
	})
}

// findYtDlpLocal finds the yt-dlp executable path
func (h *ChannelMonitorHandler) findYtDlpLocal() string {
	paths := []string{
		"yt-dlp",
		"/usr/local/bin/yt-dlp",
		"/usr/bin/yt-dlp",
	}
	for _, path := range paths {
		cmd := exec.Command(path, "--version")
		if err := cmd.Run(); err == nil {
			return path
		}
	}
	return ""
}
