package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// RemoteSearchRequest represents a remote search request
type RemoteSearchRequest struct {
	Query       string `json:"query"`
	ChannelURL  string `json:"channel_url"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	VideoURL    string `json:"video_url"`
	VideoID     string `json:"video_id"`
	Limit       int    `json:"limit"`
	UploadDate  string `json:"upload_date"`
	SortBy      string `json:"sort_by"`
}

// RemoteSearch godoc
// @Description Global search endpoint that auto-detects query, channel, or video
// @Tags youtube
// @Accept json
// @Produce json
// @Param request body RemoteSearchRequest true "Search request"
// @Success 200 {object} map[string]interface{}
// @Router /youtube/remote/search [post]
func (h *YouTubeHandler) RemoteSearch(c *gin.Context) {
	var req RemoteSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	limit := req.Limit
	if limit == 0 {
		limit = 20
	}

	var videos []youtube.SearchResult
	var searchType string
	var err error

	// Priority: 1) video related, 2) channel, 3) query
	if req.VideoURL != "" || req.VideoID != "" {
		searchType = "related"
		videoURL := req.VideoURL
		if videoURL == "" {
			videoURL = "https://www.youtube.com/watch?v=" + req.VideoID
		}
		info, err := h.downloader.GetDetailedInfo(c.Request.Context(), videoURL)
		if err == nil {
			// Parse duration string to time.Duration
			var dur time.Duration
			if info.DurationSec > 0 {
				dur = time.Duration(info.DurationSec) * time.Second
			}
			
			videosLegacy := []youtube.LegacySearchResult{{
				ID:          info.ID,
				Title:       info.Title,
				URL:         info.URL,
				Channel:     info.Channel,
				ChannelID:   info.ChannelID,
				ChannelURL:  info.ChannelURL,
				Thumbnail:   info.Thumbnail,
				Duration:    info.Duration,
				DurationSec: info.DurationSec,
				UploadDate:  info.UploadDate,
			}}
			
			// Convert to new SearchResult type
			for _, v := range videosLegacy {
				videos = append(videos, youtube.SearchResult{
					ID:         v.ID,
					Title:      v.Title,
					URL:        v.URL,
					Channel:    v.Channel,
					ChannelID:  v.ChannelID,
					ChannelURL: v.ChannelURL,
					Thumbnail:  v.Thumbnail,
					Duration:   dur,
					Views:      int64(v.ViewCount),
					UploadDate: v.UploadDate,
				})
			}
		}
	} else if req.ChannelURL != "" || req.ChannelID != "" || req.ChannelName != "" {
		searchType = "channel"
		channelURL := resolveChannelURL(req.ChannelURL, req.ChannelID, req.ChannelName)
		videosLegacy, err := h.downloader.GetChannelVideos(c.Request.Context(), channelURL, limit)
		if err == nil {
			for _, v := range videosLegacy {
				var dur time.Duration
				if v.DurationSec > 0 {
					dur = time.Duration(v.DurationSec) * time.Second
				}
				videos = append(videos, youtube.SearchResult{
					ID:         v.ID,
					Title:      v.Title,
					URL:        v.URL,
					Channel:    v.Channel,
					ChannelID:  v.ChannelID,
					ChannelURL: v.ChannelURL,
					Thumbnail:  v.Thumbnail,
					Duration:   dur,
					Views:      int64(v.ViewCount),
					UploadDate: v.UploadDate,
				})
			}
		}
	} else if req.Query != "" {
		searchType = "search"
		videos, err = h.downloader.Search(c.Request.Context(), req.Query, limit)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false,
			"error": "Specify query, channel_url, channel_id, channel_name, video_url or video_id",
		})
		return
	}

	if err != nil {
		logger.Error("Remote search failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	logger.Info("Remote search completed",
		zap.String("type", searchType),
		zap.Int("count", len(videos)))

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"search_type": searchType,
		"query":       req.Query,
		"limit":       limit,
		"videos":      videos,
		"count":       len(videos),
	})
}
