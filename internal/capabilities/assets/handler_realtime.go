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
)

// RealtimeMatchRequest is a local type replacing the removed
// mediarealtime.MatchRequest (package internal/capabilities/assets/realtime).
type RealtimeMatchRequest struct {
	Query    string   `json:"query"`
	Source   string   `json:"source"`
	TopK     int      `json:"top_k"`
	MinScore float64  `json:"min_score"`
	Filters  []string `json:"filters"`
}

// RealtimeMatchResponse is a local type replacing the removed
// mediarealtime.MatchResponse.
type RealtimeMatchResponse struct {
	Matches []RealtimeMatchAsset `json:"matches"`
}

// RealtimeMatchAsset is a local type for match results.
type RealtimeMatchAsset struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Source    string  `json:"source"`
	Score     float64 `json:"score"`
	DriveLink string  `json:"drive_link"`
}

// RealtimeMatcher is the narrow port the handler depends on.
type RealtimeMatcher interface {
	Match(ctx context.Context, req *RealtimeMatchRequest) (*RealtimeMatchResponse, error)
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
	var req RealtimeMatchRequest
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
