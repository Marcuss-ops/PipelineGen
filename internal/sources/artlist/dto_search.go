package artlist

import "github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"

// SearchRequest represents a search request
type SearchRequest struct {
	Term     string `json:"term"`
	Limit    int    `json:"limit"`
	PreferDB bool   `json:"prefer_db"`
}

// SearchResponse represents a search response with canonical asset types.
type SearchResponse struct {
	OK     bool               `json:"ok"`
	Term   string             `json:"term"`
	Source string             `json:"source"`
	Clips  []asset.MediaAsset `json:"clips"`
	Error  string             `json:"error,omitempty"`
}
