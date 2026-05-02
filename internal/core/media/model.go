package media

import "time"

// MediaAsset is the canonical media asset model for the system.
type MediaAsset struct {
	ID          string
	WorkspaceID string
	ProjectID   string

	SourceID   string
	SourceKind SourceKind
	MediaType  MediaType
	Status     MediaStatus

	Title       string
	Description string
	Category    string
	Tags        []string

	ExternalID  string
	ExternalURL string

	DurationSecs int

	PrimaryFile *MediaFile
	Files       []MediaFile

	MetadataJSON string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// MediaFile represents a file associated with a MediaAsset.
type MediaFile struct {
	ID           string
	MediaAssetID string

	LocationKind LocationKind
	URI          string
	LocalPath    string
	DriveLink    string
	DownloadLink string

	MimeType     string
	Width        int
	Height       int
	DurationSecs int
	FileSizeBytes int64
	FileHash     string

	Status MediaStatus

	CreatedAt time.Time
	UpdatedAt time.Time
}
