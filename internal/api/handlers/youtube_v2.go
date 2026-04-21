// Package handlers provides HTTP API handlers for YouTube v2 operations
package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"velox/go-master/internal/gpu"
	"velox/go-master/internal/textgen"
	youtube "velox/go-master/internal/youtube"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// YouTubeV2Handler holds dependencies for YouTube API v2 handlers
type YouTubeV2Handler struct {
	Client        youtube.Client
	GPUManager    *gpu.Manager
	TextGenerator *textgen.Generator
	Logger        *zap.Logger
}

// NewYouTubeV2Handler creates a new YouTube v2 API handler
func NewYouTubeV2Handler(client youtube.Client, gpuMgr *gpu.Manager, textGen *textgen.Generator, logger *zap.Logger) *YouTubeV2Handler {
	return &YouTubeV2Handler{
		Client:        client,
		GPUManager:    gpuMgr,
		TextGenerator: textGen,
		Logger:        logger,
	}
}

// GetVideoInfoV2 godoc
// @Summary Get video information (v2)
// @Description Fetch metadata about a YouTube video without downloading
// @Tags YouTube
// @Accept json
// @Produce json
// @Param video_id query string true "Video ID or URL"
// @Success 200 {object} youtube.VideoInfo
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/youtube/v2/video/info [get]
func (h *YouTubeV2Handler) GetVideoInfoV2(c *gin.Context) {
	videoID := c.Query("video_id")
	if videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "video_id is required"})
		return
	}

	info, err := h.Client.GetVideo(c.Request.Context(), videoID)
	if err != nil {
		h.Logger.Error("Failed to get video info", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, info)
}

// DownloadVideoV2 godoc
// @Summary Download YouTube video (v2)
// @Description Download a YouTube video with configurable quality and format
// @Tags YouTube
// @Accept json
// @Produce json
// @Param request body youtube.DownloadRequest true "Download parameters"
// @Success 200 {object} youtube.DownloadResult
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/youtube/v2/download [post]
func (h *YouTubeV2Handler) DownloadVideoV2(c *gin.Context) {
	var req youtube.DownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.Client.Download(c.Request.Context(), &req)
	if err != nil {
		h.Logger.Error("Failed to download video", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// SearchVideosV2 godoc
// @Summary Search YouTube videos (v2)
// @Description Search for videos on YouTube
// @Tags YouTube
// @Accept json
// @Produce json
// @Param query query string true "Search query"
// @Param max_results query int false "Maximum results (default 10)"
// @Param sort_by query string false "Sort by: relevance|views|date|rating"
// @Param upload_date query string false "Time filter: hour|today|week|month|year"
// @Param duration query string false "Duration filter: short|medium|long"
// @Success 200 {array} youtube.SearchResult
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/youtube/v2/search [get]
func (h *YouTubeV2Handler) SearchVideosV2(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}

	maxResults := 10
	if raw := strings.TrimSpace(c.Query("max_results")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "max_results must be a positive integer"})
			return
		}
		maxResults = parsed
	}

	opts := &youtube.SearchOptions{
		MaxResults: maxResults,
		SortBy:     strings.TrimSpace(c.DefaultQuery("sort_by", "relevance")),
		UploadDate: strings.TrimSpace(c.Query("upload_date")),
		Duration:   strings.TrimSpace(c.Query("duration")),
	}

	results, err := h.Client.Search(c.Request.Context(), query, opts)
	if err != nil {
		h.Logger.Error("Failed to search videos", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, results)
}

// GetSubtitlesV2 godoc
// @Summary Get video subtitles (v2)
// @Description Extract subtitles from a YouTube video
// @Tags YouTube
// @Accept json
// @Produce json
// @Param video_id query string true "Video ID"
// @Param language query string false "Language code (default: en)"
// @Success 200 {object} youtube.SubtitleInfo
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/youtube/v2/subtitles [get]
func (h *YouTubeV2Handler) GetSubtitlesV2(c *gin.Context) {
	videoID := c.Query("video_id")
	if videoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "video_id is required"})
		return
	}

	lang := c.DefaultQuery("language", "en")

	subtitles, err := h.Client.GetSubtitles(c.Request.Context(), videoID, lang)
	if err != nil {
		h.Logger.Error("Failed to get subtitles", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, subtitles)
}

// GetTranscriptV2 godoc
// @Summary Get video transcript (v2)
// @Description Extract transcript (subtitles as plain text) from a YouTube video URL
// @Tags YouTube
// @Accept json
// @Produce json
// @Param url query string true "Video URL"
// @Param language query string false "Language code (default: en)"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/youtube/v2/transcript [get]
func (h *YouTubeV2Handler) GetTranscriptV2(c *gin.Context) {
	url := c.Query("url")
	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url is required"})
		return
	}

	lang := c.DefaultQuery("language", "en")

	transcript, err := h.Client.GetTranscript(c.Request.Context(), url, lang)
	if err != nil {
		h.Logger.Error("Failed to get transcript", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"url":        url,
		"language":   lang,
		"transcript": transcript,
	})
}

// CheckHealthV2 godoc
// @Summary Check YouTube client health (v2)
// @Description Verify that yt-dlp is available and working
// @Tags YouTube
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/youtube/v2/health [get]
func (h *YouTubeV2Handler) CheckHealthV2(c *gin.Context) {
	err := h.Client.CheckAvailable(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"backend": "ytdlp",
	})
}

// RegisterRoutes registra tutti gli endpoint YouTube V2
func (h *YouTubeV2Handler) RegisterRoutes(protected *gin.RouterGroup) {
	ytV2 := protected.Group("/youtube/v2")
	{
		ytV2.GET("/video/info", h.GetVideoInfoV2)
		ytV2.POST("/download", h.DownloadVideoV2)
		ytV2.GET("/search", h.SearchVideosV2)
		ytV2.GET("/subtitles", h.GetSubtitlesV2)
		ytV2.GET("/transcript", h.GetTranscriptV2)
		ytV2.GET("/health", h.CheckHealthV2)
	}
}
