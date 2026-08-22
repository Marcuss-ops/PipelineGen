package artifacts

import (
	"context"
	"time"
)

// AssetIndexRecord is the application-owned projection DTO for an indexed
// media asset. Infrastructure adapters translate it to their storage model.
type AssetIndexRecord struct {
	AssetID      string
	AssetType    string
	Source       string
	SourceID     string
	GroupName    string
	Subfolder    string
	LocalPath    string
	DriveLink    string
	DownloadLink string
	LegacyFileMD5     string
	ContentHash  string
	Status       string
	Metadata     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AssetIndexPort is the narrow projection port required by Finalizer.
type AssetIndexPort interface {
	Upsert(ctx context.Context, rec *AssetIndexRecord) error
}

// MetadataInput is the application-owned input for canonical metadata
// construction. The semantic infrastructure adapter maps it to its DTO.
type MetadataInput struct {
	AssetID             string
	AssetType           string
	Source              string
	MediaType           string
	Generator           string
	PromptOriginal      string
	SemanticDescription string
	SearchText          string
	Subjects            []string
	SubjectSlugs        []string
	Tags                []string
	Categories          []string
	Mood                []string
	Style               []string
	Confidence          float64
	EmbeddingStatus     string
	VisualEmbeddingJSON string
	PHash               string
	VisualDimensions    int
	Assets              []map[string]any
	Extra               map[string]any
}

// MetadataPort is the narrow semantic metadata contract consumed by the
// artifact finalizer. The canonical implementation lives in infrastructure.
type MetadataPort interface {
	MetadataMapFromJSON(raw string) map[string]any
	MetadataMapToJSON(meta map[string]any) string
	MergeMetadataSearchText(parts ...string) string
	AssetTypeForMediaType(mediaType string) string
	BuildAssetMetadata(input MetadataInput, existing map[string]any) map[string]any
}
