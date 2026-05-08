package artlist

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/service/artlist"
	"velox/go-master/pkg/apiutil"
)

// Recommend handles the recommendation endpoint
func (h *Handler) Recommend(c *gin.Context) {
	var req artlist.RecommendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, fmt.Sprintf("invalid request: %v", err))
		return
	}

	h.log.Info("recommend request",
		zap.String("topic", req.Topic),
		zap.String("segment_id", req.SegmentID),
		zap.Int("queries", len(req.Queries)),
		zap.Float64("min_score", req.MinScore),
	)

	resp, err := h.service.Recommend(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("recommend failed", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("recommend failed: %v", err))
		return
	}

	apiutil.OK(c, resp)
}
