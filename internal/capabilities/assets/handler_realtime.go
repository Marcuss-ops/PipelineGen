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

// RegisterRoutes registers the realtime routes.
//
// Mounted on the empty-prefix module → /api/realtime/match.

// Match handles the real-time asset matching request.
