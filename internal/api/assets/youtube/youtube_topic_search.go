package youtube

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// topicSearchRequest is the transport shape for live YouTube discovery.
// The application service owns validation, ranking, metadata enrichment, and
// yt-dlp execution; this type only describes the HTTP input.
type topicSearchRequest struct {
	Q              string `form:"q" json:"q"`
	Limit          int    `form:"limit" json:"limit"`
	Sort           string `form:"sort" json:"sort"`
	PublishedAfter string `form:"published_after" json:"published_after"`
}

// SearchByTopic discovers and ranks public YouTube videos for a keyword.
// It is intentionally separate from /api/media/search: that endpoint searches
// already-registered local media, while this endpoint performs live YouTube
// discovery and returns candidate URLs for a later extraction job.
func (h *YouTubeClipHandler) SearchByTopic(c *gin.Context) {
	var req topicSearchRequest
	if c.Request.Method == http.MethodGet {
		req.Q = c.Query("q")
		req.Limit, _ = strconv.Atoi(c.Query("limit"))
		req.Sort = c.Query("sort")
		req.PublishedAfter = c.Query("published_after")
	} else if !apiutil.BindJSONInto(c, &req) {
		return
	}

	if strings.TrimSpace(req.Q) == "" {
		apiutil.BadRequest(c, "q is required")
		return
	}

	result, err := h.service.SearchByTopicWithFilter(
		c.Request.Context(), req.Q, req.Limit, req.Sort, req.PublishedAfter,
	)
	if err != nil {
		apiutil.Error(c, http.StatusServiceUnavailable, err.Error())
		return
	}
	apiutil.OK(c, result)
}
