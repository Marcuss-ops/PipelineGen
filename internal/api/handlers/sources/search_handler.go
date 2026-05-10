package sources

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/apiutil"
)

// SearchRequest represents a search request
type SearchRequest struct {
	Q     string `form:"q" binding:"required"`
	Type  string `form:"type"` // video, image, audio, all
	Limit int    `form:"limit,default=20"`
}

// Search godoc
func (h *Handler) Search(c *gin.Context) {
	var req SearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		apiutil.BadRequest(c, "query parameter 'q' is required")
		return
	}

	if req.Limit <= 0 {
		req.Limit = 20
	}

	results := gin.H{}

	// Search Artlist clips
	if h.artlistSvc != nil && (req.Type == "" || req.Type == "video" || req.Type == "all") {
		searchReq := &artlist.SearchRequest{
			Term:  req.Q,
			Limit: req.Limit,
		}
		searchResp, err := h.artlistSvc.Search(c.Request.Context(), searchReq)
		if err != nil {
			h.log.Warn("artlist search failed", zap.Error(err))
			results["artlist"] = gin.H{
				"count":   0,
				"results": []interface{}{},
				"error":   err.Error(),
			}
		} else if searchResp != nil {
			results["artlist"] = gin.H{
				"count":   len(searchResp.Clips),
				"results": searchResp.Clips,
				"source":  searchResp.Source,
			}
		}
	}

	// Search Catalog folders
	if h.catalogRepo != nil {
		catalogResults, err := h.catalogRepo.SearchAll(req.Q)
		if err != nil {
			h.log.Warn("catalog search failed", zap.Error(err))
		} else {
			results["catalog"] = gin.H{
				"count":   len(catalogResults),
				"results": catalogResults,
			}
		}
	}

	apiutil.OK(c, gin.H{
		"query":   req.Q,
		"type":    req.Type,
		"results": results,
	})
}

// Stats godoc
func (h *Handler) Stats(c *gin.Context) {
	ctx := c.Request.Context()

	stats, err := h.assetIndexSvc.GetStats(ctx)
	if err != nil {
		h.log.Error("failed to get asset stats", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"total":      stats.Total,
		"by_type":    stats.ByType,
		"by_status":  stats.ByStatus,
	})
}
