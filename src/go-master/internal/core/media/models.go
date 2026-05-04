package media

import "time"

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
