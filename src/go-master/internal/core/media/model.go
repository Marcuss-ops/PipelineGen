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

	MimeType      string
	Width         int
	Height        int
	DurationSecs  int
	FileSizeBytes int64
	FileHash      string

	Status MediaStatus

	CreatedAt time.Time
	UpdatedAt time.Time
}
type Item struct {
	ID           string
	WorkspaceID  string
	ProjectID    string
	SourceID     string
	SourceKind   SourceKind
	MediaType    MediaType
	Status       MediaStatus
	Title        string
	Description  string
	ExternalID   string
	ExternalURL  string
	DurationSecs int
	FileHash     string
	MetadataJSON string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
type File struct {
	ID            string
	MediaItemID   string
	LocationKind  LocationKind
	URI           string
	MimeType      string
	Width         int
	Height        int
	DurationSecs  int
	FileSizeBytes int64
	FileHash      string
	Status        MediaStatus
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
type Source struct {
	ID           string
	WorkspaceID  string
	Kind         SourceKind
	Name         string
	ExternalID   string
	ExternalURL  string
	MetadataJSON string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
type Tag struct {
	ID             string
	WorkspaceID    string
	Name           string
	NormalizedName string
	CreatedAt      time.Time
}
type Usage struct {
	ID          string
	MediaItemID string
	ProjectID   string
	ScriptID    string
	UsageKind   UsageKind
	UsedAt      time.Time
}
type SearchQuery struct {
	WorkspaceID string
	ProjectID   string
	SourceKinds []SourceKind
	MediaTypes  []MediaType
	Statuses    []MediaStatus
	Query       string
	Tags        []string
	Limit       int
	Offset      int
}
