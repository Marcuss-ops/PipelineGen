package youtube

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// StockSearchYouTubeRequest represents a stock search request
type StockSearchYouTubeRequest struct {
	Subject    string `json:"subject"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results" binding:"min=0,max=100"`
}

// StockSearchYouTube godoc
// @Summary Search YouTube for stock footage
// @Description Search YouTube for stock footage sources
// @Tags youtube
// @Accept json
// @Produce json
// @Param request body StockSearchYouTubeRequest true "Stock search request"
// @Success 200 {object} map[string]interface{}
// @Router /youtube/stock/search [post]
func (h *YouTubeHandler) StockSearchYouTube(c *gin.Context) {
	var req StockSearchYouTubeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	subject := req.Subject
	if subject == "" {
		subject = req.Query
	}
	if subject == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Subject/query required"})
		return
	}

	maxResults := req.MaxResults
	if maxResults == 0 {
		maxResults = 15
	}

	results, err := h.downloader.SearchWithOptions(c.Request.Context(), subject, maxResults, "highlights")
	if err != nil {
		logger.Error("Stock search failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Extract URLs for compatibility
	links := make([]string, len(results))
	for i, r := range results {
		links[i] = r.URL
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"links":   links,
		"count":   len(links),
		"videos":  results,
		"subject": subject,
	})
}
