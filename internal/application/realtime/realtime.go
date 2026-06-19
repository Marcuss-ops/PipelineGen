// Package realtime provides real-time clip search use cases.
//
// The implementation lives in internal/media/realtime/.
package realtime

import "context"

// ClipMatch is a real-time clip match result.
type ClipMatch struct {
	AssetID string
	Score   float64
	Name    string
	Path    string
}

// Searcher is the contract for real-time clip search.
type Searcher interface {
	SearchClips(ctx context.Context, query string, limit int) ([]ClipMatch, error)
}
