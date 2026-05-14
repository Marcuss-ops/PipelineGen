package artlist

import "velox/go-master/pkg/models"

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

// SyncRequest represents a sync request
type SyncRequest struct {
	Terms       []string `json:"terms"`
	Limit       int      `json:"limit"`
	OnlyPending bool     `json:"only_pending"`
}

// SyncResponse represents a sync response
type SyncResponse struct {
	OK         bool   `json:"ok"`
	Requested  int    `json:"requested"`
	Synced     int    `json:"synced"`
	Failed     int    `json:"failed"`
	SavedClips int    `json:"saved_clips"`
	Error      string `json:"error,omitempty"`
}
