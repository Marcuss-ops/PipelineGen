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

	Name     *string
	Category *string
	Group    *string

	// Tags is the JSON-encoded canonical tag array (media_assets.tags). When
	// set, the writer derives media_assets.tags_norm in the SAME statement so
	// the lexical tag projection can never drift from the tag array.
	Tags *string
	// SearchTerms is the JSON-encoded keyword array (media_assets.search_terms)
	// consumed by the local lexical search predicate.
	SearchTerms *string
	// ReviewStatus is the governance review state column.
	ReviewStatus        *string
	FolderID            *string
	FolderPath          *string
	DeletedAt           *string
	UpdatedAt           *string
	SearchText          *string
	LifecycleState      *string
	IndexState          *string
	EnrichState         *string
	MetadataJSON        *string
	MetadataPatchJSON   *string
	EmbeddingJSON       *string
	VisualEmbedding     *string
	TranscriptEmbedding *string
	Collection          *string
	SceneType           *string
	PHash               *string
	LastUsedAt          *string
	QualityScore        *float64
	ReuseCount          *int

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
//
// MimeType + FileSizeBytes carry the byte identity of the object that was
// delivered. They exist because the patch is the LAST writer of the drive
// location row, and it used to hardcode ”,0 — so a delivery that provably
// measured and even disk-verified the artifact size (clip.render's outbox
// consumer compares os.Stat(path).Size() against req.SizeBytes before
// uploading) recorded a location that named a real Drive object and said
// nothing about its bytes. Both fields are optional: an unknown value (""/0)
// PRESERVES whatever the row already holds instead of erasing a known fact.
type DriveLocationPatch struct {
	AssetID     string
	DriveFileID string
	DriveLink   string
	DownloadURL string

	// MimeType is the IANA media type of the delivered object (e.g.
	// "video/mp4"). Empty means "not attested by this caller".
	MimeType string

	// FileSizeBytes is the object's real size in bytes. <= 0 means "not
	// attested by this caller".
	FileSizeBytes int64
}

// AssetMutator is the mutation half of the canonical asset writer. Production
// composition supplies the SAME canonical committer instance
// (pgmedia.PostgresMediaCommitter) that implements AssetCommitter; this split
// interface exists only to avoid forcing read-only consumers and test fakes to
// implement mutation methods they never call.
//
// Invariant: there is one concrete production writer (PostgreSQL), not one
// writer per mutation type. Every mutator method is SELF-OWNED: it opens its
// own transaction on the media SSOT. There is deliberately no tx-scoped
// variant on this boundary, because a caller-owned transaction could be a
// SQLite one and would then run SQLite SQL against the PostgreSQL media
// domain.
type AssetMutator interface {
	PatchAsset(ctx context.Context, patch AssetPatch) error
	PatchAssetTx(ctx context.Context, tx Transaction, patch AssetPatch) error
	ReconcileDriveLocations(ctx context.Context, changes []DriveLocationPatch) error
	ReconcileDriveLocationsTx(ctx context.Context, tx Transaction, changes []DriveLocationPatch) error
}

// CanonicalAssetWriter is the complete production write surface. Composition
// should construct one instance and pass the narrow AssetCommitter or
// AssetMutator view to consumers as required.
//
// MEDIA-SSOT (September 2026, DEMOLITION COMPLETE): the interface is the
// UNION of two self-owned contracts only. It deliberately exposes NO
// transaction-bound method (`UpsertClipTx`, `SetIndexStateTx`), because such a
// method is engine-agnostic by type (`*sql.Tx`) while the media domain is
// owned by exactly one engine. That type was the single defect that let the
// SQLite outbox dispatcher hand its own transaction to the PostgreSQL writer;
// removing it makes the cross-database bug unrepresentable rather than merely
// discouraged. Producers commit through CommitAndIndex / CommitAsset /
// CommitDiscoveredAssetAndIndex instead.
type CanonicalAssetWriter interface {
	AssetCommitter
	AssetMutator
}
