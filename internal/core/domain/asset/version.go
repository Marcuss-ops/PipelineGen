// Package asset — Version is the canonical domain entity for an asset_versions row.
//
// An asset may have multiple Versions representing historical snapshots:
// the original download, a re-encoded variant, a transcription rebuild, etc.
// Each Version is immutable once written; to "update" the asset a new version
// is appended.
//
// NOTE: the asset_versions table itself is scheduled for a follow-up
// migration (PR E of the asset migration plan). Defining the canonical type
// and contract now so consumers can adopt the field set without depending
// on table state. Until the table exists, the concrete implementation returns
// (nil, nil) from GetCurrent — consumers must treat CurrentVersion as
// advisory.
package asset

import (
	"context"
	"time"
)

// Version represents a single asset_versions row.
type Version struct {
	ID            int64     `json:"id"`
	AssetID       string    `json:"asset_id"`
	VersionNumber int       `json:"version_number"` // 1-indexed monotonic
	SourceURI     string    `json:"source_uri"`     // where this version came from
	FileHash      string    `json:"file_hash"`      // hash of the version's binary
	FileSizeBytes int64     `json:"file_size_bytes"`
	MimeType      string    `json:"mime_type"`
	MetadataJSON  string    `json:"metadata_json,omitempty"` // free-form per-version metadata
	CreatedAt     time.Time `json:"created_at"`
}

// VersionRepository is the canonical domain contract for asset_versions
// persistence. Implementations live in the infrastructure layer.
//
// Services MUST depend on this interface, NOT on a concrete type. Until
// the asset_versions table exists, a stub implementation that returns
// (nil, nil) for GetCurrent is acceptable — Details.CurrentVersion will
// be nil and consumers must not assume a non-nil value.
type VersionRepository interface {
	// GetCurrent returns the latest Version (highest VersionNumber) for
	// the asset, or (nil, nil) if no version exists. Read-only.
	GetCurrent(ctx context.Context, assetID string) (*Version, error)

	// List returns all Versions for the asset, newest first. Read-only.
	// Returns an empty slice if no versions exist.
	List(ctx context.Context, assetID string) ([]Version, error)

	// Append inserts a new Version row. The repository is responsible
	// for assigning VersionNumber atomically (monotonic per asset_id).
	// The CreatedAt timestamp must be set by the implementation if the
	// caller leaves it zero.
	Append(ctx context.Context, v *Version) error
}
