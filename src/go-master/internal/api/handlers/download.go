// Package handlers provides HTTP handlers for the API.
package handlers

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/download"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// DownloaderInterface defines the interface for download operations
type DownloaderInterface interface {
	Download(ctx context.Context, url string) (*download.DownloadResult, error)
	ListDownloads() (map[download.Platform][]string, error)
	GetPlatformFolder(platform download.Platform) string
}

// DownloadHandler handles video download HTTP requests
type DownloadHandler struct {
	downloader     *download.Downloader
	mockDownloader DownloaderInterface
}

// NewDownloadHandler creates a new download handler
func NewDownloadHandler(downloader *download.Downloader) *DownloadHandler {
	return &DownloadHandler{downloader: downloader}
}

// RegisterRoutes registers download routes
func (h *DownloadHandler) RegisterRoutes(rg *gin.RouterGroup) {
	downloadGroup := rg.Group("/download")
	{
		downloadGroup.POST("", h.Download)
		downloadGroup.GET("/platforms", h.ListPlatforms)
		downloadGroup.GET("/library", h.ListDownloads)
		downloadGroup.GET("/library/:platform", h.ListPlatformDownloads)
		downloadGroup.DELETE("/library/:platform/:videoID", h.DeleteDownload)
	}
}

// DownloadRequest represents a download request
type DownloadRequest struct {
	URL string `json:"url" binding:"required"`
}

// Download downloads a video from YouTube or TikTok
// @Summary Download video
// @Description Download video from YouTube or TikTok
// @Tags download
// @Accept json
// @Produce json
// @Param request body DownloadRequest true "Download request"
// @Success 200 {object} map[string]interface{}
// @Router /download [post]
func (h *DownloadHandler) Download(c *gin.Context) {
	var req DownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	platform := download.DetectPlatform(req.URL)
	if platform == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Unsupported URL. Supported platforms: YouTube, TikTok",
		})
		return
	}

	logger.Info("Video download requested",
		zap.String("platform", string(platform)),
		zap.String("url", req.URL),
	)

	// Use mock downloader if available, otherwise use real downloader
	var result *download.DownloadResult
	var err error
	if h.mockDownloader != nil {
		result, err = h.mockDownloader.Download(c.Request.Context(), req.URL)
	} else {
		result, err = h.downloader.Download(c.Request.Context(), req.URL)
	}

	if err != nil {
		logger.Error("Download failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Download failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"platform": result.Platform,
		"video_id": result.VideoID,
		"title":    result.Title,
		"file":     result.FilePath,
		"duration": result.Duration,
		"author":   result.Author,
	})
}

// ListPlatforms lists supported download platforms
// @Summary List platforms
// @Description List supported video platforms
// @Tags download
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /download/platforms [get]
func (h *DownloadHandler) ListPlatforms(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"platforms": []gin.H{
			{
				"name":        "YouTube",
				"code":        "youtube",
				"url_pattern": "youtube.com/watch?v=... | youtu.be/...",
				"features":    []string{"1080p", "4K", "Playlist", "Metadata"},
			},
			{
				"name":        "TikTok",
				"code":        "tiktok",
				"url_pattern": "tiktok.com/@user/video/... | vm.tiktok.com/...",
				"features":    []string{"No watermark", "HD", "Metadata"},
			},
		},
	})
}

// ListDownloads lists all downloaded videos
// @Summary List downloads
// @Description List all downloaded videos
// @Tags download
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /download/library [get]
func (h *DownloadHandler) ListDownloads(c *gin.Context) {
	var result map[download.Platform][]string
	var err error

	if h.mockDownloader != nil {
		result, err = h.mockDownloader.ListDownloads()
	} else {
		result, err = h.downloader.ListDownloads()
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to list downloads: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"downloads": result,
	})
}

// ListPlatformDownloads lists downloads for a specific platform
func (h *DownloadHandler) ListPlatformDownloads(c *gin.Context) {
	platform := c.Param("platform")

	var folder string
	if h.mockDownloader != nil {
		folder = h.mockDownloader.GetPlatformFolder(download.Platform(platform))
	} else {
		folder = h.downloader.GetPlatformFolder(download.Platform(platform))
	}

	if _, err := os.Stat(folder); os.IsNotExist(err) {
		c.JSON(http.StatusOK, gin.H{
			"ok":      true,
			"videos":  []string{},
			"count":   0,
		})
		return
	}

	files, err := filepath.Glob(filepath.Join(folder, "*", "*"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to list downloads: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"videos":  files,
		"count":   len(files),
	})
}

// DeleteDownload deletes a downloaded video
func (h *DownloadHandler) DeleteDownload(c *gin.Context) {
	platform := c.Param("platform")
	videoID := c.Param("videoID")

	var folder string
	if h.mockDownloader != nil {
		folder = h.mockDownloader.GetPlatformFolder(download.Platform(platform))
	} else {
		folder = h.downloader.GetPlatformFolder(download.Platform(platform))
	}
	videoFolder := filepath.Join(folder, videoID)

	if err := os.RemoveAll(videoFolder); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Failed to delete download: " + err.Error(),
		})
		return
	}

	logger.Info("Download deleted",
		zap.String("platform", platform),
		zap.String("video_id", videoID),
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Download deleted",
	})
}
