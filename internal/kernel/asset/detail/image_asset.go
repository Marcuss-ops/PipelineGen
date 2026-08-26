package detail

import "time"

// ImageAsset represents an image stored in the asset index.
type ImageAsset struct {
	ID           int64         `json:"id"`
	Hash         string        `json:"hash"`
	SubjectID    string        `json:"subject_id"`
	SlugID       string        `json:"slug_id,omitempty"`
	PathRel      string        `json:"path_rel"`
	LocalPath    string        `json:"local_path,omitempty"`
	SourceURL    string        `json:"source_url"`
	License      string        `json:"license"`
	Width        int           `json:"width"`
	Height       int           `json:"height"`
	SizeBytes    int64         `json:"size_bytes"`
	QualityScore int           `json:"quality_score"`
	Description  string        `json:"description"`
	DriveFileID  string        `json:"drive_file_id,omitempty"`
	Status       string        `json:"status,omitempty"`
	Error        string        `json:"error,omitempty"`
	MetadataJSON string        `json:"metadata_json"`
	CreatedAt    time.Time     `json:"created_at"`
	Tags         []string      `json:"tags,omitempty"`
	Origin       ImageOrigin   `json:"origin,omitempty"`
	Provider     ImageProvider `json:"provider,omitempty"`
}

// ImageUsage tracks usage of an image inside a rendered video.
type ImageUsage struct {
	ID      int64     `json:"id"`
	ImageID int64     `json:"image_id"`
	VideoID string    `json:"video_id"`
	UsedAt  time.Time `json:"used_at"`
}

// ImageTag represents a tag associated with an image.
type ImageTag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// RepositoryListFilter is the read-request filter shape for the SQLite images_repository ListImages path.
type RepositoryListFilter struct {
	SubjectID string
	Origins   []ImageOrigin
	Providers []string
	StyleIDs  []string
	Tags      []string
	Limit     int
}

// RepositoryImageRow is the read-model returned by the SQLite images_repository.
type RepositoryImageRow struct {
	AssetID       string
	Subject       string
	Slug          string
	Name          string
	PreviewURL    string
	Provider      string
	Origin        ImageOrigin
	License       string
	Author        string
	SourcePageURL string
	DriveLink     string
	LegacyFileMD5 string
	Description   string
	Width         int
	Height        int
	Score         float64
	Tags          []string
	StyleID       string
	StyleVersion  string
	CreatedAt     string
}

const (
	DefaultResolvedLimit = 25
	MaxListImagesLimit   = 100
)
