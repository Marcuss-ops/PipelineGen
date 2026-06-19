package asset

import "time"

// Version represents a single version record for an asset.
type Version struct {
	ID            int64     `json:"id"`
	AssetID       string    `json:"asset_id"`
	VersionNumber int       `json:"version_number"`
	SourceURI     string    `json:"source_uri"`
	FileHash      string    `json:"file_hash"`
	FileSizeBytes int64     `json:"file_size_bytes"`
	MimeType      string    `json:"mime_type"`
	MetadataJSON  string    `json:"metadata_json,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}
