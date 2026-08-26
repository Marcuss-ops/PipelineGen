package assets

import "context"

// TagSource classifies where a tag comes from so provider, semantic,
// manual, transcript, visual and imported tags can coexist without
// losing provenance.
type TagSource string

const (
	TagSourceProvider   TagSource = "provider"
	TagSourceSemantic   TagSource = "semantic"
	TagSourceManual     TagSource = "manual"
	TagSourceTranscript TagSource = "transcript"
	TagSourceVisual     TagSource = "visual"
	TagSourceImport     TagSource = "import"
)

// ValidTagSources lists every supported tag source. It is the single
// source of truth for allowed `asset_tags.source` values and mirrors the
// CHECK constraint in migrations/sqlite/172_asset_tags.sql.
var ValidTagSources = []TagSource{
	TagSourceProvider,
	TagSourceSemantic,
	TagSourceManual,
	TagSourceTranscript,
	TagSourceVisual,
	TagSourceImport,
}

// ProviderMetadata is the canonical domain representation of the
// structured metadata returned by an external provider (Artlist,
// Storyblocks, Pexels, Pixabay, etc.).
type ProviderMetadata struct {
	AssetID              string
	Provider             string
	ExternalID           string
	Creator              string
	Country              string
	Location             string
	CollectionID         string
	CollectionTitle      string
	PageURL              string
	ThumbnailURL         string
	PreviewURL           string
	LicenseClass         string
	ProviderMetadataHash string
	RawMetadataJSON      string
	FetchedAt            string
	UpdatedAt            string
}

// AssetTag is a single normalized tag attached to an asset with an
// explicit provenance source.
type AssetTag struct {
	AssetID       string
	Tag           string
	NormalizedTag string
	Source        TagSource
	Confidence    float64
	Language      string
	CreatedAt     string
}

// ProviderMetadataRepository persists and retrieves provider-specific
// metadata for an asset.
type ProviderMetadataRepository interface {
	// UpsertProviderMetadata inserts or replaces the provider metadata
	// row for the given asset.
	UpsertProviderMetadata(ctx context.Context, meta ProviderMetadata) error
	// GetProviderMetadata returns the stored provider metadata for an
	// asset, or nil when no row exists.
	GetProviderMetadata(ctx context.Context, assetID string) (*ProviderMetadata, error)
}

// AssetTagRepository persists and retrieves asset tags while keeping
// tags from different sources separated by the `source` column.
type AssetTagRepository interface {
	// ReplaceTagsBySource atomically replaces all tags for an asset
	// from a given source. Other sources are left untouched.
	ReplaceTagsBySource(ctx context.Context, assetID string, source TagSource, tags []AssetTag) error
	// ListTagsByAsset returns every tag for an asset, ordered by source
	// and normalized tag.
	ListTagsByAsset(ctx context.Context, assetID string) ([]AssetTag, error)
	// ListTagsBySource returns tags for an asset that belong to a single
	// source.
	ListTagsBySource(ctx context.Context, assetID string, source TagSource) ([]AssetTag, error)
}
