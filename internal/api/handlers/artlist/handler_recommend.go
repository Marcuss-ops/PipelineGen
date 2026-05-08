package artlist

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/clipresolver"
	"velox/go-master/pkg/apiutil"
)

// Recommend handles the recommendation endpoint using clipresolver
func (h *Handler) Recommend(c *gin.Context) {
	var req clipresolver.RecommendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, fmt.Sprintf("invalid request: %v", err))
		return
	}

	if h.clipResolver == nil {
		apiutil.InternalError(c, fmt.Errorf("clip resolver service not available"))
		return
	}

	h.log.Info("clip resolver recommend request",
		zap.String("topic", req.Topic),
		zap.String("segment_id", req.SegmentID),
		zap.Int("queries", len(req.Queries)),
		zap.Float64("min_score", req.MinScore),
	)

	resp, err := h.clipResolver.Recommend(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("clip resolver recommend failed", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("recommend failed: %v", err))
		return
	}

	apiutil.OK(c, resp)
}
