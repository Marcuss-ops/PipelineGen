// Package handlers provides HTTP handlers for YouTube endpoints.
package handlers

import (
	"github.com/gin-gonic/gin"
	"velox/go-master/internal/youtube"
)

// YouTubeHandler handles YouTube HTTP requests
type YouTubeHandler struct {
	downloader *youtube.Downloader
}

// NewYouTubeHandler creates a new YouTube handler
func NewYouTubeHandler(tempDir string) *YouTubeHandler {
	return &YouTubeHandler{
		downloader: youtube.NewDownloader(tempDir),
	}
}

// RegisterRoutes registers YouTube routes
func (h *YouTubeHandler) RegisterRoutes(rg *gin.RouterGroup) {
	yt := rg.Group("/youtube")
	{
		// Subtitles
		yt.GET("/subtitles", h.GetSubtitles)

		// Search
		yt.POST("/search", h.SearchVideos)
		yt.POST("/search/interviews", h.SearchInterviews)

		// Remote endpoints (compatibility with Python API)
		yt.POST("/remote/search", h.RemoteSearch)
		yt.POST("/remote/channel-videos", h.GetChannelVideos)
		yt.POST("/remote/video-info", h.GetVideoInfo)
		yt.POST("/remote/thumbnail", h.GetThumbnail)
		yt.POST("/remote/trending", h.GetTrending)
		yt.POST("/remote/channel-analytics", h.GetChannelAnalytics)
		yt.POST("/remote/related-videos", h.GetRelatedVideos)

		// Stock search
		yt.POST("/stock/search", h.StockSearchYouTube)
	}
}
