package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/media/realtime"
)

// RealtimeMatcher is the interface for real-time asset matching.
// This allows test mocks without importing the realtime package's concrete type.
type RealtimeMatcher interface {
	Match(ctx context.Context, req *realtime.MatchRequest) (*realtime.MatchResponse, error)
}

// MatchHandler handles the POST /api/realtime/match endpoint.
type MatchHandler struct {
	svc RealtimeMatcher
	log *zap.Logger
}

// NewMatchHandler creates a new realtime match handler.
func NewMatchHandler(svc RealtimeMatcher, log *zap.Logger) *MatchHandler {
	return &MatchHandler{
		svc: svc,
		log: log,
	}
}

// RegisterRoutes registers the realtime routes.
func (h *MatchHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/realtime/match", h.Match)
}

// Match handles the real-time asset matching request.
func (h *MatchHandler) Match(c *gin.Context) {
	var req realtime.MatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "invalid request: " + err.Error(),
		})
		return
	}

	resp, err := h.svc.Match(c.Request.Context(), &req)
	if err != nil {
		h.log.Warn("realtime match failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}
