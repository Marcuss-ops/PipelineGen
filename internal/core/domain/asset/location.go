// Package asset — Location is the canonical domain entity for a single
// asset_locations row.
//
// One asset can have multiple Locations (a local file + a Drive copy +
// an S3 mirror), with at most one flagged as IsPrimary. The URI disambiguates
// the storage kind implicitly:
//
//	uri = "/path/..."          → LocationKindLocal
//	uri = "drive://FILE_ID"   → LocationKindDrive
//	uri = "s3://bucket/key"   → LocationKindObjectStorage
//
// ExternalID is the opaque identifier inside the remote system
// (Drive FILE_ID, S3 object key, ...) — empty for local. AccessURL is a
// human-browse link (Drive web URL, S3 public URL). DownloadURL is an
// explicit download endpoint.
package asset

import "time"

// LocationKind categorises where a Location physically lives.
type LocationKind string

const (
	LocationKindLocal         LocationKind = "local"
	LocationKindDrive         LocationKind = "drive"
	LocationKindObjectStorage LocationKind = "object_storage"
)

// Location is the canonical domain entity for an asset_locations row.
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
	FileHash      string       `json:"file_hash"`
	IsPrimary     bool         `json:"is_primary"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}
