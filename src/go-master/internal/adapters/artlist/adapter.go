// Package artlist defines adapter interfaces for Artlist scraper.
//
// STATUS: EXPERIMENTAL - Interface defined but not yet implemented or used.
// TODO: Implement and migrate artlist service to use this adapter.
package artlist

import "context"

type SearchInput struct {
	Query string
	Limit int
}

type ClipCandidate struct {
	ID       string
	Title    string
	URL      string
	Duration float64
	Tags     []string
}

type ArtlistAdapter interface {
	Search(ctx context.Context, input SearchInput) ([]ClipCandidate, error)
}
