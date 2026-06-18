// Package asset — repository interfaces for media asset persistence.
package asset

import "context"

// Repository is the canonical domain contract for media asset CRUD.
// Implementations live in the infrastructure layer.
//
// Services MUST depend on this interface, NOT on the concrete clips.Repository.
type Repository interface {
	Upsert(ctx context.Context, asset *MediaAsset) error
	Get(ctx context.Context, id string) (*MediaAsset, error)
	List(ctx context.Context, filter Filter) ([]*MediaAsset, error)
	Count(ctx context.Context, filter Filter) (int64, error)
	SoftDelete(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	HardDelete(ctx context.Context, id string) error
}

// Filter defines query parameters for listing assets.
type Filter struct {
	Source      string   // filter by source ("youtube", "artlist", etc.)
	MediaType   string   // filter by media type ("video", "audio", "image")
	States      []string // filter by lifecycle states
	IDs         []string // filter by specific IDs
	ExcludeIDs  []string // exclude specific IDs
	HasEmbedding *bool   // filter by whether embedding_json is populated
	IsFolder    *bool    // filter by folder/non-folder
	Limit       int
	Offset      int
}

// Searcher is the optional interface for semantic/keyword search.
type Searcher interface {
	Search(ctx context.Context, query SearchQuery) ([]SearchResult, error)
}

// SearchQuery defines a search request.
type SearchQuery struct {
	Text      string   `json:"text"`
	Source    string   `json:"source,omitempty"`
	MediaType string   `json:"media_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Limit     int      `json:"limit"`
}

// SearchResult is a scored search hit.
type SearchResult struct {
	Asset *MediaAsset `json:"asset"`
	Score float64     `json:"score"`
}
