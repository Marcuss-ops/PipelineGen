package asset

import (
	"context"
	"time"
)

// RenditionKind describes the semantic role of a rendition.
type RenditionKind string

const (
	RenditionKindMaster     RenditionKind = "master"
	RenditionKindMezzanine  RenditionKind = "mezzanine"
	RenditionKindProxy      RenditionKind = "proxy"
	RenditionKindThumbnail  RenditionKind = "thumbnail"
	RenditionKindStoryboard RenditionKind = "storyboard"
	RenditionKindAudio      RenditionKind = "audio"
	RenditionKindSubtitle   RenditionKind = "subtitle"
)

// AssetRendition is a single technical variant of a media asset.
type AssetRendition struct {
	ID         string        `json:"id"`
	AssetID    string        `json:"asset_id"`
	LocationID *int64        `json:"location_id,omitempty"`
	Kind       RenditionKind `json:"kind"`
	Container  string        `json:"container,omitempty"`
	Codec      string        `json:"codec,omitempty"`
	Width      int           `json:"width"`
	Height     int           `json:"height"`
	FPS        float64       `json:"fps"`
	Bitrate    int64         `json:"bitrate"`
	ColorSpace string        `json:"color_space,omitempty"`
	SHA256     string        `json:"sha256,omitempty"`
	SizeBytes  int64         `json:"size_bytes"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// RenditionRepository persists and retrieves AssetRendition records.
type RenditionRepository interface {
	Create(ctx context.Context, rendition *AssetRendition) (string, error)
	Get(ctx context.Context, id string) (*AssetRendition, error)
	ListByAsset(ctx context.Context, assetID string) ([]*AssetRendition, error)
	ListByLocation(ctx context.Context, locationID int64) ([]*AssetRendition, error)
	Update(ctx context.Context, rendition *AssetRendition) error
	Delete(ctx context.Context, id string) error
}
