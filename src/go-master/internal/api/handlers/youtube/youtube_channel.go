package youtube

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// GetChannelVideosRequest represents a channel videos request
type GetChannelVideosRequest struct {
	ChannelURL  string `json:"channel_url"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Limit       int    `json:"limit" binding:"min=0,max=100"`
	UploadDate  string `json:"upload_date"`
}

// GetChannelVideos godoc
// @Summary Get channel videos
// @Description Get videos from a YouTube channel
// @Tags youtube
// @Accept json
// @Produce json
// @Param request body GetChannelVideosRequest true "Channel request"
// @Success 200 {object} map[string]interface{}
// @Router /youtube/remote/channel-videos [post]
func (h *YouTubeHandler) GetChannelVideos(c *gin.Context) {
	var req GetChannelVideosRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.ChannelURL == "" && req.ChannelID == "" && req.ChannelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false,
			"error": "channel_url, channel_id or channel_name required",
		})
		return
	}

	limit := req.Limit
	if limit == 0 {
		limit = 20
	}

	channelURL := resolveChannelURL(req.ChannelURL, req.ChannelID, req.ChannelName)

	videos, err := h.downloader.GetChannelVideos(c.Request.Context(), channelURL, limit)
	if err != nil {
		logger.Error("Failed to get channel videos", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"channel_url": channelURL,
		"channel_id":  req.ChannelID,
		"videos":      videos,
		"count":       len(videos),
	})
}

// GetVideoInfoRequest represents a video info request
type GetVideoInfoRequest struct {
	VideoURL string `json:"video_url"`
	VideoID  string `json:"video_id"`
}

// GetVideoInfo godoc
// @Description Get detailed information about a YouTube video
// @Tags youtube
// @Accept json
// @Produce json
// @Param request body GetVideoInfoRequest true "Video request"
// @Success 200 {object} map[string]interface{}
// @Router /youtube/remote/video-info [post]
func (h *YouTubeHandler) GetVideoInfo(c *gin.Context) {
	var req GetVideoInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.VideoURL == "" && req.VideoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false,
			"error": "video_url or video_id required",
		})
		return
	}

	videoURL := req.VideoURL
	if videoURL == "" {
		videoURL = "https://www.youtube.com/watch?v=" + req.VideoID
	}

	info, err := h.downloader.GetDetailedInfo(c.Request.Context(), videoURL)
	if err != nil {
		logger.Error("Failed to get video info", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"video": info,
	})
}

// GetThumbnailRequest represents a thumbnail request
type GetThumbnailRequest struct {
	VideoURL string `json:"video_url"`
	VideoID  string `json:"video_id"`
	Quality  string `json:"quality"`
}

// GetThumbnail godoc
// @Description Get thumbnail URLs for a YouTube video
// @Tags youtube
// @Accept json
// @Produce json
// @Param request body GetThumbnailRequest true "Thumbnail request"
// @Success 200 {object} map[string]interface{}
// @Router /youtube/remote/thumbnail [post]
func (h *YouTubeHandler) GetThumbnail(c *gin.Context) {
	var req GetThumbnailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.VideoURL == "" && req.VideoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false,
			"error": "video_url or video_id required",
		})
		return
	}

	videoID := req.VideoID
	if videoID == "" {
		videoID = extractVideoID(req.VideoURL)
		if videoID == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok": false,
				"error": "Could not extract video ID from URL",
			})
			return
		}
	}

	quality := req.Quality
	if quality == "" {
		quality = "max"
	}

	thumbnails := map[string]string{
		"maxres":  "https://img.youtube.com/vi/" + videoID + "/maxresdefault.jpg",
		"hq":      "https://img.youtube.com/vi/" + videoID + "/hqdefault.jpg",
		"mq":      "https://img.youtube.com/vi/" + videoID + "/mqdefault.jpg",
		"default": "https://img.youtube.com/vi/" + videoID + "/default.jpg",
	}

	thumbnail := thumbnails["maxres"]
	if t, ok := thumbnails[quality]; ok {
		thumbnail = t
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"video_id":   videoID,
		"thumbnails": thumbnails,
		"thumbnail":  thumbnail,
	})
}
