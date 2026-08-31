package persistence

import (
	"context"
	"time"
)

// AssetPatch is the typed partial-update contract for an existing canonical
// media asset. A nil pointer means "leave unchanged"; an explicit pointer to
// the zero value means "set/clear this field". No producer may issue SQL
// directly to media_assets to perform these mutations.
type AssetPatch struct {
	AssetID string

	Name           *string
	Category       *string
	Group          *string
	SearchText     *string
	LifecycleState *string
	IndexState     *string
	EnrichState    *string
	MetadataJSON   *string
	EmbeddingJSON  *string
	VisualEmbedding     *string
	TranscriptEmbedding *string
	Collection     *string
	SceneType      *string
	PHash          *string
	LastUsedAt     *string
	QualityScore   *float64
	ReuseCount     *int

	DriveFileID  *string
	DriveLink    *string
	DownloadLink *string
	LocalPath    *string

	// IndexStateUpdatedAt overrides the index-state timestamp when IndexState
	// is set. Nil uses the canonical clock at the persistence boundary.
	IndexStateUpdatedAt *time.Time

	// RequestIndex emits the canonical asset.index.requested event in the same
	// transaction as the patch. Source/MediaType/SourceVersion are required
	// when RequestIndex is true; EventKeySuffix distinguishes a mutation event
	// from the initial ingest request without creating another outbox pathway.
	RequestIndex   bool
	IndexPriority  int
	Source         string
	MediaType      string
	SourceVersion  string
	EventKeySuffix string
}

// DriveLocationPatch is the typed mutation contract for the Drive projection
// of an existing asset. The canonical writer preserves the durable Drive file
// identity when DriveFileID is empty, updates asset_locations and media_assets
// atomically, and emits the indexing request through the same outbox.
type DriveLocationPatch struct {
	AssetID     string
	DriveFileID string
	DriveLink   string
	DownloadURL string
}

// AssetMutator is the mutation half of the canonical asset writer. Production
// composition supplies the SAME SQLiteMediaCommitter instance that implements
// AssetCommitter; this split interface exists only to avoid forcing read-only
// consumers and test fakes to implement mutation methods they never call.
//
// Invariant: there is one concrete production writer, not one writer per
// mutation type.
type AssetMutator interface {
	PatchAsset(ctx context.Context, patch AssetPatch) error
	PatchAssetTx(ctx context.Context, tx Transaction, patch AssetPatch) error
	ReconcileDriveLocations(ctx context.Context, changes []DriveLocationPatch) error
	ReconcileDriveLocationsTx(ctx context.Context, tx Transaction, changes []DriveLocationPatch) error
}

// CanonicalAssetWriter is the complete production write surface. Composition
// should construct one instance and pass the narrow AssetCommitter or
// AssetMutator view to consumers as required.
type CanonicalAssetWriter interface {
	AssetCommitter
	AssetMutator
}
