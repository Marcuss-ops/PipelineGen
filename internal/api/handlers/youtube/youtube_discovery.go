package youtube

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// NOTE: youtube_discovery.go handlers are temporarily disabled
// They need migration to the new Client interface

// GetTrending godoc - DISABLED (needs migration)
func (h *YouTubeHandler) GetTrending(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "GetTrending disabled - migration to new client pending"})
}

// GetChannelAnalytics godoc - DISABLED (needs migration)
func (h *YouTubeHandler) GetChannelAnalytics(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "GetChannelAnalytics disabled - migration to new client pending"})
}

// GetRelatedVideos godoc - DISABLED (needs migration)
func (h *YouTubeHandler) GetRelatedVideos(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "GetRelatedVideos disabled - migration to new client pending"})
}

// resolveChannelURL helper is kept as-is for when these are re-enabled
