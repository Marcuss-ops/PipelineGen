package monitoring

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/api/middleware"
	"velox/go-master/internal/service/channelmonitor"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/security"

	"go.uber.org/zap"
)

// ChannelMonitorHandler handles HTTP requests for the channel monitor
type ChannelMonitorHandler struct {
	monitor     *channelmonitor.Monitor
	ytClient    youtube.Client
	driveClient *drive.Client
	ollamaURL   string
	configDir   string       // base directory for config/data files (from cfg.Storage.DataDir)
	mu          sync.RWMutex // protects monitor re-creation
}

// NewChannelMonitorHandler creates a new handler
func NewChannelMonitorHandler(monitor *channelmonitor.Monitor, ytClient youtube.Client, driveClient *drive.Client, ollamaURL, configDir string) *ChannelMonitorHandler {
	return &ChannelMonitorHandler{
		monitor:     monitor,
		ytClient:    ytClient,
		driveClient: driveClient,
		ollamaURL:   ollamaURL,
		configDir:   configDir,
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
		monitorGroup.GET("/channels", h.GetChannels)
		monitorGroup.POST("/channels", h.AddChannel)
		monitorGroup.DELETE("/channels", h.RemoveChannel)
		monitorGroup.GET("/processed", h.GetProcessedVideos)
	}
}

// ProcessSingleVideoRequest represents a request to process a single video
type ProcessSingleVideoRequest struct {
	YouTubeURL string `json:"youtube_url" binding:"required"`
	Category   string `json:"category"`  // optional override
	MaxClips   int    `json:"max_clips"` // optional, default 5
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
	if err := middleware.BindAndValidate(c, &req); err != nil {
		var ve *middleware.ValidationError
		if errors.As(err, &ve) {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": ve.Error()})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "youtube_url is required"})
		}
		return
	}

	// Validate YouTube URL before processing
	if err := security.ValidateDownloadURL(req.YouTubeURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid YouTube URL: " + err.Error()})
		return
	}

	// Extract video ID
	videoID := extractVideoIDFromURL(req.YouTubeURL)
	if videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid YouTube URL"})
		return
	}
	if err := security.ValidateVideoID(videoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid video ID: " + err.Error()})
		return
	}

	log := logger.Get()
	log.Info("Processing single video requested",
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
		h.mu.RLock()
		monitor := h.monitor
		h.mu.RUnlock()

		if monitor == nil {
			log.Error("Monitor not initialized")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		startTime := time.Now()
		result, err := monitor.ProcessVideo(ctx, videoID, req.Category)
		if err != nil {
			log.Error("Async video processing failed", zap.String("video_id", videoID), zap.Error(err))
			return
		}

		log.Info("=== ASYNC PROCESSING COMPLETE ===",
			zap.String("video_id", result.VideoID),
			zap.String("title", result.Title),
			zap.Int("highlights", len(result.Highlights)),
			zap.Int("clips_uploaded", len(result.Clips)),
			zap.String("folder", result.FolderPath),
			zap.Duration("duration", time.Since(startTime)),
		)
	}()
}

// Helper functions
func extractVideoIDFromURL(url string) string {
	// Simple ID extractor, also available in security package or youtube module
	if strings.Contains(url, "v=") {
		parts := strings.Split(url, "v=")
		if len(parts) > 1 {
			id := parts[1]
			if idx := strings.Index(id, "&"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
	}
	if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "youtu.be/")
		if len(parts) > 1 {
			return parts[1]
		}
	}
	return ""
}

// RunOnceRequest represents a manual run request
type RunOnceRequest struct {
	ChannelURL string `json:"channel_url"` // optional: run only for specific channel
	MaxVideos  int    `json:"max_videos"`  // optional: override max videos per channel
}

type addChannelRequest struct {
	URL             string   `json:"url" binding:"required"`
	Category        string   `json:"category"`
	Keywords        []string `json:"keywords"`
	MinViews        int64    `json:"min_views"`
	MaxClipDuration int      `json:"max_clip_duration"`
	MaxVideos       int      `json:"max_videos"`
	FolderName      string   `json:"folder_name"`
}

type removeChannelRequest struct {
	URL string `json:"url" binding:"required"`
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

	if strings.TrimSpace(req.ChannelURL) != "" {
		cfg, err := h.loadMonitorConfig()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		var selected *channelmonitor.ChannelConfig
		for _, ch := range cfg.Channels {
			if strings.EqualFold(strings.TrimSpace(ch.URL), strings.TrimSpace(req.ChannelURL)) {
				cp := ch
				selected = &cp
				break
			}
		}
		if selected == nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "channel_url not found in monitor config",
			})
			return
		}
		tempCfg := *cfg
		tempCfg.Channels = []channelmonitor.ChannelConfig{*selected}
		monitor = channelmonitor.NewMonitor(tempCfg, h.ytClient, h.driveClient, h.ollamaURL)
	}

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
		VideoID    string `json:"video_id"`
		Title      string `json:"title"`
		Channel    string `json:"channel"`
		Highlights int    `json:"highlights"`
		ClipsCount int    `json:"clips_count"`
		FolderPath string `json:"folder_path"`
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
		"ok":               true,
		"videos_processed": len(results),
		"videos":           summaries,
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

	channelsCount := 0
	if cfg, err := h.loadMonitorConfig(); err == nil {
		channelsCount = len(cfg.Channels)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"status": gin.H{
			"running":             true,
			"channels_configured": channelsCount,
			"last_run":            "see logs for details",
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
	cfg, err := h.loadMonitorConfig()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"ok": true,
			"config": gin.H{
				"channels": []string{},
				"message":  "No config file found, using defaults",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"config": gin.H{
			"channels":          cfg.Channels,
			"check_interval":    cfg.CheckInterval.String(),
			"ytdlp_path":        cfg.YtDlpPath,
			"cookies_path":      cfg.CookiesPath,
			"max_clip_duration": cfg.MaxClipDuration,
			"ollama_url":        cfg.OllamaURL,
		},
	})
}

// GetChannels returns configured channels for monitor cron.
func (h *ChannelMonitorHandler) GetChannels(c *gin.Context) {
	cfg, err := h.loadMonitorConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"channels": cfg.Channels,
		"count":    len(cfg.Channels),
	})
}

// AddChannel adds or updates a channel in channel_monitor_config.json.
func (h *ChannelMonitorHandler) AddChannel(c *gin.Context) {
	var req addChannelRequest
	if err := middleware.BindAndValidate(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	cfg, err := h.loadMonitorConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	entry := channelmonitor.ChannelConfig{
		URL:             strings.TrimSpace(req.URL),
		Category:        strings.TrimSpace(req.Category),
		Keywords:        req.Keywords,
		MinViews:        req.MinViews,
		MaxClipDuration: req.MaxClipDuration,
		MaxVideos:       req.MaxVideos,
		FolderName:      strings.TrimSpace(req.FolderName),
	}
	if entry.Category == "" {
		entry.Category = "Discovery"
	}
	if entry.MaxClipDuration <= 0 {
		entry.MaxClipDuration = cfg.MaxClipDuration
		if entry.MaxClipDuration <= 0 {
			entry.MaxClipDuration = 60
		}
	}
	if entry.MaxVideos <= 0 {
		entry.MaxVideos = 5
	}

	updated := false
	for i := range cfg.Channels {
		if strings.EqualFold(strings.TrimSpace(cfg.Channels[i].URL), entry.URL) {
			cfg.Channels[i] = entry
			updated = true
			break
		}
	}
	if !updated {
		cfg.Channels = append(cfg.Channels, entry)
	}
	if err := h.saveMonitorConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"updated": updated,
		"channel": entry,
		"count":   len(cfg.Channels),
	})
}

// RemoveChannel removes a channel by URL from channel_monitor_config.json.
func (h *ChannelMonitorHandler) RemoveChannel(c *gin.Context) {
	var req removeChannelRequest
	if err := middleware.BindAndValidate(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	cfg, err := h.loadMonitorConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	target := strings.TrimSpace(req.URL)
	filtered := make([]channelmonitor.ChannelConfig, 0, len(cfg.Channels))
	removed := false
	for _, ch := range cfg.Channels {
		if strings.EqualFold(strings.TrimSpace(ch.URL), target) {
			removed = true
			continue
		}
		filtered = append(filtered, ch)
	}
	if !removed {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "channel not found"})
		return
	}
	cfg.Channels = filtered
	if err := h.saveMonitorConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"removed": true,
		"count":   len(cfg.Channels),
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
		"ok":                    true,
		"processed_videos_file": filepath.Join(h.configDir, "channel_monitor_processed.json"),
		"message":               fmt.Sprintf("Check %s for full list", filepath.Join(h.configDir, "channel_monitor_processed.json")),
	})
}

func (h *ChannelMonitorHandler) loadMonitorConfig() (*channelmonitor.MonitorConfig, error) {
	return channelmonitor.LoadConfigWithDefaults(filepath.Join(h.configDir, "channel_monitor_config.json"))
}

func (h *ChannelMonitorHandler) saveMonitorConfig(cfg *channelmonitor.MonitorConfig) error {
	return channelmonitor.SaveConfig(filepath.Join(h.configDir, "channel_monitor_config.json"), cfg)
}
