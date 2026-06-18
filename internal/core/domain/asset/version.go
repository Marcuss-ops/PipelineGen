// Package asset — Version is the canonical domain entity for an asset_versions row.
//
// An asset may have multiple Versions representing historical snapshots:
// the original download, a re-encoded variant, a transcription rebuild, etc.
// Each Version is immutable once written; to "update" the asset a new version
// is appended.
//
// NOTE: the asset_versions table itself is scheduled for a follow-up
// migration (PR E of the asset migration plan). Defining the canonical type
// now so consumers can adopt the field set without depending on table state.
package asset

import "time"

// Version represents a single asset_versions row.
type Version struct {
	ID            int64     `json:"id"`
	AssetID       string    `json:"asset_id"`
	VersionNumber int       `json:"version_number"` // 1-indexed monotonic
	SourceURI     string    `json:"source_uri"`     // where this version came from
	FileHash      string    `json:"file_hash"`      // hash of the version's binary
	FileSizeBytes int64      `json:"file_size_bytes"`
	MimeType      string    `json:"mime_type"`
	MetadataJSON  string    `json:"metadata_json,omitempty"` // free-form per-version metadata
	CreatedAt     time.Time `json:"created_at"`
}
