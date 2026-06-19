// Package association provides the script-to-asset matching use case.
//
// The implementation lives in internal/media/association/.
// This package defines the interface contract for the matching engine.
package association

import "context"

// MatchRequest is a request to find matching assets for a script segment.
type MatchRequest struct {
	ScriptID    int64
	SegmentText string
	Tags        []string
	Topic       string
	Language    string
	Limit       int
}

// MatchResult is a single matched asset.
type MatchResult struct {
	AssetID string
	Score   float64
	Source  string
	Name    string
}

// Service is the contract for script-to-asset matching.
type Service interface {
	MatchAssets(ctx context.Context, req MatchRequest) ([]MatchResult, error)
}
