package artlist

import "velox/go-master/internal/media/models"

// SearchRequest represents a search request
type SearchRequest struct {
	Term     string `json:"term"`
	Limit    int    `json:"limit"`
	PreferDB bool   `json:"prefer_db"`
}

// SearchResponse represents a search response
type SearchResponse struct {
	OK     bool                `json:"ok"`
	Term   string              `json:"term"`
	Source string              `json:"source"`
	Clips  []models.MediaAsset `json:"clips"`
	Error  string              `json:"error,omitempty"`
}


