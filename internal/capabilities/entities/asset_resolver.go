package entities

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// EntityAsset is the structured SSOT record of ONE asset bound to an entity
// — the plan's `entity_assets` row:
//
//	entity_id, asset_id, asset_type, sha256, storage_url, drive_file_id,
//	width, height, quality_score, source, created_at
//
// The record is content-addressed: SHA256 is the identity of the asset
// BYTES, so the same file always yields the same sha256 no matter where it
// is stored or which entity binds it. RenderingGen never receives a bare
// URL: it receives the full content-addressed ref (asset_id + sha256 + url)
// and materializes the asset from that identity.
type EntityAsset struct {
	EntityID string `json:"entity_id"`
	AssetID  string `json:"asset_id"`
	// AssetType is the semantic kind of the asset ("photo", "logo",
	// "product_shot", ...).
	AssetType string `json:"asset_type,omitempty"`
	// SHA256 is the content address of the asset bytes (hex, lowercase).
	// REQUIRED: an asset without a sha256 cannot be cached or verified.
	SHA256 string `json:"sha256"`
	// StorageURL is the fetchable source URL (object store / CDN / Drive
	// direct link). RenderingGen materializes the asset from here.
	StorageURL string `json:"storage_url"`
	// LocalPath is an in-process staging hint for bytes already materialized by
	// the producer. It is ephemeral and never crosses a serialized asset record.
	LocalPath string `json:"-"`
	// DriveFileID is the Google Drive file id, when the asset is published
	// to Drive (for artifact publication).
	DriveFileID string `json:"drive_file_id,omitempty"`
	// Width/Height are the asset's pixel dimensions (0 when unknown).
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// QualityScore is the ranked visual quality in [0,1]: the resolver
	// prefers the highest-scoring asset of an entity.
	QualityScore float64 `json:"quality_score"`
	// Source is the provenance of the asset ("internet_images",
	// "ai_generated", "user_upload", ...).
	Source string `json:"source,omitempty"`
}

// ErrInvalidEntityAsset is returned when an EntityAsset violates its SSOT
// invariants: empty identity or a missing content address.
var ErrInvalidEntityAsset = errors.New("invalid entity asset")

// Validate enforces the content-addressed asset invariants: non-empty entity
// and asset ids, a non-empty sha256 content address, a fetchable storage url,
// and a quality score in [0,1]. An asset without a sha256 is rejected: it
// could not be cached, verified or deduplicated.
func (a EntityAsset) Validate() error {
	if strings.TrimSpace(a.EntityID) == "" {
		return fmt.Errorf("%w: empty entity id", ErrInvalidEntityAsset)
	}
	if strings.TrimSpace(a.AssetID) == "" {
		return fmt.Errorf("%w: entity %q: empty asset id", ErrInvalidEntityAsset, a.EntityID)
	}
	if strings.TrimSpace(a.SHA256) == "" {
		return fmt.Errorf("%w: entity %q asset %q: missing sha256 content address", ErrInvalidEntityAsset, a.EntityID, a.AssetID)
	}
	if strings.TrimSpace(a.StorageURL) == "" {
		return fmt.Errorf("%w: entity %q asset %q: missing storage url", ErrInvalidEntityAsset, a.EntityID, a.AssetID)
	}
	if a.QualityScore < 0 || a.QualityScore > 1 {
		return fmt.Errorf("%w: entity %q asset %q: quality score %f out of range", ErrInvalidEntityAsset, a.EntityID, a.AssetID, a.QualityScore)
	}
	return nil
}

// AssetRepository is the relational-style source of truth for entity asset
// records: upsert by stable (entity_id, asset_id), deterministic iteration
// per entity. The plan's "database relazionale" for the entity→assets link.
//
// The in-memory implementation keeps the package pure (zero I/O); a durable
// SQLite adapter can be dropped in behind the same interface by the
// composition root without changing the resolver contract.
type AssetRepository interface {
	// Upsert inserts or replaces the record keyed by (entity_id, asset_id).
	Upsert(asset EntityAsset) error
	// All returns every asset of one entity in deterministic order (best
	// quality first, then asset id).
	All(entityID string) []EntityAsset
}

// InMemoryAssetRepository is the pure, deterministic AssetRepository.
type InMemoryAssetRepository struct {
	byEntity map[string]map[string]EntityAsset // entity_id → asset_id → asset
}

// NewInMemoryAssetRepository returns an empty in-memory asset repository.
func NewInMemoryAssetRepository() *InMemoryAssetRepository {
	return &InMemoryAssetRepository{byEntity: map[string]map[string]EntityAsset{}}
}

// Upsert inserts or replaces the asset record. The (entity_id, asset_id)
// pair is the identity: re-indexing the same asset (e.g. refreshed from the
// store) replaces the previous record instead of duplicating it.
func (r *InMemoryAssetRepository) Upsert(asset EntityAsset) error {
	if err := asset.Validate(); err != nil {
		return err
	}
	bucket, ok := r.byEntity[asset.EntityID]
	if !ok {
		bucket = map[string]EntityAsset{}
		r.byEntity[asset.EntityID] = bucket
	}
	bucket[asset.AssetID] = asset
	return nil
}

// All returns every asset of one entity, best quality first (ties broken by
// asset id), so the resolver and callers never depend on map order.
func (r *InMemoryAssetRepository) All(entityID string) []EntityAsset {
	bucket := r.byEntity[entityID]
	out := make([]EntityAsset, 0, len(bucket))
	for _, asset := range bucket {
		out = append(out, asset)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].QualityScore != out[j].QualityScore {
			return out[i].QualityScore > out[j].QualityScore
		}
		return out[i].AssetID < out[j].AssetID
	})
	return out
}

// ResolvedAssetRef is the content-addressed asset reference RenderingGen
// receives — NEVER a bare URL. The contract: asset_id identifies the asset,
// sha256 is its content address, url is where the bytes live.
type ResolvedAssetRef struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	LocalPath string `json:"-"`
	MediaType string `json:"media_type,omitempty"`
}

// mediaTypeFor maps the semantic asset type to a fetchable media type for
// the ref. Unknown types get the generic image type (the ref contract still
// carries asset_id + sha256 + url regardless).
func mediaTypeFor(assetType string) string {
	switch NormalizeType(assetType) {
	case "LOGO", "PHOTO", "PRODUCT_SHOT", "PORTRAIT":
		return "image/png" // logo/overlay assets render best as png
	case "PHOTO_JPG", "PHOTO_JPEG":
		return "image/jpeg"
	case "VIDEO", "CLIP":
		return "video/mp4"
	case "":
		return ""
	default:
		return "image/png"
	}
}
