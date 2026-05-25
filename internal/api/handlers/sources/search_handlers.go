package sources

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/sources/artlist"
	"velox/go-master/internal/pkg/apiutil"
)

// SearchRequest represents a search request
type SearchRequest struct {
	Q     string `form:"q" binding:"required"`
	Type  string `form:"type"` // video, image, audio, all
	Limit int    `form:"limit,default=20"`
	Sort  string `form:"sort"`
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

	// Search YouTube clips
	if h.youtubeSvc != nil && (req.Type == "" || req.Type == "video" || req.Type == "all") {
		// YouTube search is live and supports the "-N" limit suffix internally in s.SearchLive
		ytResults, err := h.youtubeSvc.SearchLive(c.Request.Context(), req.Q, req.Limit, req.Sort)
		if err != nil {
			h.log.Warn("youtube search failed", zap.Error(err))
			results["youtube"] = gin.H{
				"count":   0,
				"results": []interface{}{},
				"error":   err.Error(),
			}
		} else {
			results["youtube"] = gin.H{
				"count":   len(ytResults),
				"results": ytResults,
				"source":  "live",
			}
		}
	}

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
		catalogResults, err := h.catalogRepo.SearchAll(c.Request.Context(), req.Q)
		if err != nil {
			h.log.Warn("catalog search failed", zap.Error(err))
		} else {
			results["catalog"] = gin.H{
				"count":   len(catalogResults),
				"results": catalogResults,
			}
		}
	}

	// Unified Local Search (media_assets table)
	if h.clipsRepo != nil && (req.Type == "" || req.Type == "video" || req.Type == "all") {
		localClips, err := h.clipsRepo.SearchClips(c.Request.Context(), "all", req.Q)
		if err != nil {
			h.log.Warn("local unified search failed", zap.Error(err))
		} else {
			results["local"] = gin.H{
				"count":   len(localClips),
				"results": localClips,
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
		"ok":        true,
		"total":     stats.Total,
		"by_type":   stats.ByType,
		"by_status": stats.ByStatus,
	})
}
