// Package assets (api/assets) — handler_realtime.go holds the
// RealtimeMatcher HTTP transport for the POST /api/realtime/match
// endpoint. Wave 14 close (June 2026): this receiver was absorbed
// from the standalone internal/api/realtime/ package when the
// 1-file package directory was consolidated.
//
// Routes mounted on the empty-prefix module (see registry.go
// `module.NewRouteModule("realtime", ..., "", ...)`) → /api/realtime/*.
package assets

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mediarealtime "github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
)

// RealtimeMatcher is the narrow port the handler depends on. The
// concrete *mediarealtime.Service satisfies it; tests can substitute
// a mock without importing the real service.
type RealtimeMatcher interface {
	Match(ctx context.Context, req *mediarealtime.MatchRequest) (*mediarealtime.MatchResponse, error)
}

// RealtimeMatchHandler handles the POST /api/realtime/match endpoint.
// Named with the "Realtime" prefix to avoid colliding with the other
// *MatchHandler receivers elsewhere in the assets package
// (e.g. voiceover/soundeffect asset matching).
type RealtimeMatchHandler struct {
	svc RealtimeMatcher
	log *zap.Logger
}

// NewRealtimeMatchHandler creates a new realtime match handler.
func NewRealtimeMatchHandler(svc RealtimeMatcher, log *zap.Logger) *RealtimeMatchHandler {
	return &RealtimeMatchHandler{
		svc: svc,
		log: log,
	}
}

// RegisterRoutes registers the realtime routes.
//
// Mounted on the empty-prefix module → /api/realtime/match.
func (h *RealtimeMatchHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/realtime/match", h.Match)
}

// Match handles the real-time asset matching request.
func (h *RealtimeMatchHandler) Match(c *gin.Context) {
	var req mediarealtime.MatchRequest
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
