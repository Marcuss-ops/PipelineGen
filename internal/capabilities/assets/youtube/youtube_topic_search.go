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
// yt-dlp execution; this type only describes the HTTP input. GET-only: the
// former POST variant was retired (no internal consumers; GET is the canonical
// discovery surface).
type topicSearchRequest struct {
	Q              string `form:"q"`
	Limit          int    `form:"limit"`
	Sort           string `form:"sort"`
	PublishedAfter string `form:"published_after"`
}

// SearchByTopic discovers and ranks public YouTube videos for a keyword.
// It is intentionally separate from /api/media/search: that endpoint searches
// already-registered local media, while this endpoint performs live YouTube
// discovery and returns candidate URLs for a later extraction job.
func (h *YouTubeClipHandler) SearchByTopic(c *gin.Context) {
	var req topicSearchRequest
	req.Q = c.Query("q")
	req.Limit, _ = strconv.Atoi(c.Query("limit"))
	req.Sort = c.Query("sort")
	req.PublishedAfter = c.Query("published_after")

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
