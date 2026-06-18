// Package asset defines the canonical domain interface for media asset
// persistence. The Repository contract is the single source of truth
// for media_asset CRUD operations; implementations live in the
// infrastructure layer (internal/repository/clips).
//
// This interface is intentionally FOCUSED — it exposes only the methods
// that cross-domain services consume. The full clips.Repository (~59 methods)
// remains available for callers that need bulk operations, dedup, folders,
// or raw DB access; those callers depend on the concrete type directly.
package asset

import "context"

// MediaAsset is the canonical domain representation of a media_assets row.
// It is intentionally minimal — services that need the full typed model
// (with Metadata map, tags, embeddings, etc.) use the legacy models.MediaAsset
// type, which will be migrated into this domain package in a follow-up.
type MediaAsset struct {
	ID             string   `json:"id"`
	Source         string   `json:"source"`
	Name           string   `json:"name"`
	Tags           []string `json:"tags"`
	DurationMs     int64    `json:"duration_ms"`
	URL            string   `json:"url"`
	MediaType      string   `json:"media_type"`
	Status         string   `json:"status"`
	DriveFileID    string   `json:"drive_file_id"`
	DriveFolderID  string   `json:"drive_folder_id"`
	DriveLink      string   `json:"drive_link"`
	DownloadLink   string   `json:"download_link"`
	FileHash       string   `json:"file_hash"`
	LocalPath      string   `json:"local_path"`
	EmbeddingJSON  string   `json:"embedding_json"`
	SearchText     string   `json:"search_text"`
	IsFolder       bool     `json:"is_folder"`
}

// Repository is the canonical domain contract for media asset persistence.
// Implementations live in internal/repository/clips.
//
// Services MUST depend on this interface, NOT on the concrete clips.Repository.
// This enables test doubles and keeps the domain layer decoupled from SQLite.
//
// NOTE: This interface uses *asset.MediaAsset but the concrete clips.Repository
// uses *models.MediaAsset. An adapter (analogous to assetprocessing.Adapter)
// is needed before services can migrate to this interface. See PR-07 follow-up.
type Repository interface {
	// UpsertClip inserts or updates a media asset row (full UPSERT).
	UpsertClip(ctx context.Context, clip *MediaAsset) error

	// GetClip returns a single media asset by ID, or nil if not found.
	GetClip(ctx context.Context, id string) (*MediaAsset, error)

	// DeleteClip soft-deletes a media asset by ID (sets lifecycle_state='deleted').
	DeleteClip(ctx context.Context, id string) error

	// RestoreClip restores a soft-deleted media asset.
	RestoreClip(ctx context.Context, id string) error

	// HardDeleteClip permanently deletes a media asset by ID.
	HardDeleteClip(ctx context.Context, id string) error

	// CountAll returns the total count of non-deleted media assets.
	CountAll(ctx context.Context) (int64, error)

	// ListClips returns all media assets, optionally filtered by source.
	ListClips(ctx context.Context, source string) ([]*MediaAsset, error)
}
