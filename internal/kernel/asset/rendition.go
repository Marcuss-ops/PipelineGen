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
	// RenditionKindManifest (PR-CLIPINGEST-PIPELINE step 9, July 2026):
	// canonical rendition kind for the per-asset `{asset_id}__manifest.json`
	// sidecar (the asset's metadata ledger co-located with the master in
	// the same Drive folder). The JSON sidecar is not a video/image/audio
	// rendition in the traditional sense, but it IS one of the three
	// canonical files the Publisher publishes for every asset per the
	// user-spec literal `{asset_id}__master.mp4 + __preview.mp4 +
	// __manifest.json`. Adding it to the RenditionKind enum keeps the
	// canonical Publisher surface uniform (all three files flow through
	// the same per-file publish seam); the file is text/JSON so
	// buildRenditionOutput's per-kind probe (width/height/fps) no-ops
	// for it (the manifest has no codec/width/height). Future cleanup
	// may collapse the JSON sidecar into a separate ManifestFile type,
	// but the surface stays a RenditionKind until that lands.
	RenditionKindManifest RenditionKind = "manifest"
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
