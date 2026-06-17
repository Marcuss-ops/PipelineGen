package assetregistry

import "context"

// Registry is the central interface for media asset persistence.
// Implementations handle storage, retrieval, and deduplication of media records.
type Registry interface {
	// UpsertMedia inserts a new record or updates an existing one.
	UpsertMedia(ctx context.Context, rec *MediaRecord) error
	// GetMedia retrieves a single media record by its unique ID.
	GetMedia(ctx context.Context, id string) (*MediaRecord, error)
	// DeleteMedia removes a media record from storage.
	DeleteMedia(ctx context.Context, id string) error
	// GetAllWithDriveFileID returns all records that have an associated Drive file ID.
	GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error)
	// FindByPHash looks for an existing asset with the same perceptual hash.
	// It returns the existing asset ID if found, or an empty string otherwise.
	FindByPHash(ctx context.Context, phash string) (string, error)
}
