package youtube

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// GetSubtitles godoc
// @Summary Get YouTube subtitles
// @Description Download subtitles from a YouTube video with timestamps
// @Tags youtube
// @Produce json
// @Param youtube_url query string true "YouTube video URL"
// @Param language query string false "Language code" default(en)
// @Success 200 {object} map[string]interface{}
// @Router /youtube/subtitles [get]
func (h *YouTubeHandler) GetSubtitles(c *gin.Context) {
	youtubeURL := c.Query("youtube_url")
	language := c.DefaultQuery("language", "en")

	if youtubeURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "youtube_url required"})
		return
	}

	result, err := h.downloader.GetSubtitles(c.Request.Context(), youtubeURL, language)
	if err != nil {
		logger.Error("Failed to get subtitles", zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "No subtitles found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"youtube_url": result.YouTubeURL,
		"language":    result.Language,
		"char_count":  result.CharCount,
		"vtt_content": result.VTTContent,
	})
}

// SearchRequest represents a search request
type SearchRequest struct {
	Query      string `json:"query" binding:"required,min=1"`
	MaxResults int    `json:"max_results"`
}

// SearchVideos godoc
// @Summary Search YouTube videos
// @Description Search for videos on YouTube
// @Tags youtube
// @Accept json
// @Produce json
// @Param request body SearchRequest true "Search request"
// @Success 200 {object} map[string]interface{}
// @Router /youtube/search [post]
func (h *YouTubeHandler) SearchVideos(c *gin.Context) {
	var req SearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	maxResults := req.MaxResults
	if maxResults == 0 {
		maxResults = 15
	}

	results, err := h.downloader.Search(c.Request.Context(), req.Query, maxResults)
	if err != nil {
		logger.Error("Search failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":     true,
		"videos": results,
		"count":  len(results),
		"query":  req.Query,
	})
}

// SearchInterviewsRequest represents an interview search request
type SearchInterviewsRequest struct {
	Subject    string `json:"subject" binding:"required,min=1"`
	MaxResults int    `json:"max_results"`
}

// SearchInterviews godoc
// @Summary Search YouTube interviews
// @Description Search for interviews on YouTube
// @Tags youtube
// @Accept json
// @Produce json
// @Param request body SearchInterviewsRequest true "Search request"
// @Success 200 {object} map[string]interface{}
// @Router /youtube/search/interviews [post]
func (h *YouTubeHandler) SearchInterviews(c *gin.Context) {
	var req SearchInterviewsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	maxResults := req.MaxResults
	if maxResults == 0 {
		maxResults = 10
	}

	results, err := h.downloader.SearchWithOptions(c.Request.Context(), req.Subject, maxResults, "interviews")
	if err != nil {
		logger.Error("Interview search failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Extract just URLs for compatibility
	links := make([]string, len(results))
	for i, r := range results {
		links[i] = r.URL
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"links":   links,
		"videos":  results,
		"count":   len(results),
		"type":    "interviews",
		"subject": req.Subject,
	})
}
