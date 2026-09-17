package artifacts

import "context"

// Registry is the central interface for media asset persistence.
// Implementations handle storage, retrieval, and deduplication of media records.
type Registry interface {
	// UpsertMedia inserts a new record or updates an existing one.
	UpsertMedia(ctx context.Context, rec *MediaRecord) error
	// GetMedia retrieves a single media record by its unique ID.
	GetMedia(ctx context.Context, id string) (*MediaRecord, error)
	// DeleteMedia removes a media record from drive.
	DeleteMedia(ctx context.Context, id string) error
	// GetAllWithDriveFileID returns all records that have an associated Drive file ID.
	GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error)
	// FindByPHash looks for an existing asset with the same perceptual hash.
	// It returns the existing asset ID if found, or an empty string otherwise.
	FindByPHash(ctx context.Context, phash string) (string, error)
	// FindByContentHash resolves the live asset that owns a physical content
	// identity (the SHA-256 hex digest of the bytes).
	//
	// It is the SINGLE authority for content-identity lookup: adapters MUST NOT
	// re-implement their own SHA-256 queries, because a second copy of this
	// predicate is how "the same bytes" silently becomes "the same asset".
	//
	// A nil record with a nil error means "these bytes are not stored yet".
	// Returning a record means the bytes ARE stored — the caller still has to
	// decide whether its own asset ID already owns them (logical identity) or
	// whether it is a new logical asset that merely reuses the storage.
	FindByContentHash(ctx context.Context, sha256 string) (*MediaRecord, error)
}
