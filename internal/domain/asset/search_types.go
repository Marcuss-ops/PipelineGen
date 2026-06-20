package asset

import "context"

// SearchQuery defines a search request across assets.
type SearchQuery struct {
	Text      string   `json:"text"`
	Source    string   `json:"source,omitempty"`
	MediaType string   `json:"media_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Limit     int      `json:"limit"`
}

// SearchResult is a scored search hit.
type SearchResult struct {
	Asset *Asset  `json:"asset"`
	Score float64 `json:"score"`
}

// Searcher is the optional interface for semantic/keyword search.
type Searcher interface {
	Search(ctx context.Context, query SearchQuery) ([]SearchResult, error)
}
