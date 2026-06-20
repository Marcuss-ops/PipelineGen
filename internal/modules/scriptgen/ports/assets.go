package ports

import "context"

// AssetSummary is the lightweight view of a media asset consumed by the
// scriptgen module when planning clip assignment. It deliberately does
// NOT expose binary data, transcripts, or vectors — those live in
// internal/media/ and must not leak into the scriptgen module.
type AssetSummary struct {
	AssetID     string   `json:"asset_id"`
	Title       string   `json:"title"`
	Source      string   `json:"source"` // "youtube" | "artlist" | "stock"
	URL         string   `json:"url,omitempty"`
	DurationSec int      `json:"duration_sec"`
	Tags        []string `json:"tags,omitempty"`
	Score       float64  `json:"score,omitempty"`
}

// AssetRepository abstracts read-only access to the media asset catalog.
// The concrete adapter (SQLite-backed, possibly with cache) is wired
// in by Agent 1.
type AssetRepository interface {
	GetByID(ctx context.Context, assetID string) (*AssetSummary, error)
	ListByIDs(ctx context.Context, ids []string) ([]AssetSummary, error)
	PickForTopic(ctx context.Context, topic string, n int) ([]AssetSummary, error)
}
