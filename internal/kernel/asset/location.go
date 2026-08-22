package asset

import "time"

// LocationKind categorises where a Location physically lives.
type LocationKind string

const (
	LocationKindLocal         LocationKind = "local"
	LocationKindDrive         LocationKind = "drive"
	LocationKindObjectStorage LocationKind = "object_storage"
)

// Location is the canonical domain entity for an asset location record.
type Location struct {
	ID            int64        `json:"id"`
	AssetID       string       `json:"asset_id"`
	LocationKind  LocationKind `json:"location_kind"`
	URI           string       `json:"uri"`
	ExternalID    string       `json:"external_id,omitempty"`
	AccessURL     string       `json:"access_url,omitempty"`
	DownloadURL   string       `json:"download_url,omitempty"`
	MimeType      string       `json:"mime_type"`
	FileSizeBytes int64        `json:"file_size_bytes"`
	LegacyFileMD5      string       `json:"legacy_file_md5"`
	IsPrimary     bool         `json:"is_primary"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}
