package models

import "time"

// Clip represents a video clip asset
type Clip struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Filename     string    `json:"filename"`
	FolderID     string    `json:"folder_id"`
	FolderPath   string    `json:"folder_path"`
	Group        string    `json:"group"`
	MediaType    string    `json:"media_type"`
	DriveLink    string    `json:"drive_link"`
	DownloadLink string    `json:"download_link"`
	DriveFileID  string    `json:"drive_file_id"` // Google Drive file ID
	Tags         []string  `json:"tags"`
	Source       string    `json:"source"`
	Category     string    `json:"category"`
	ExternalURL  string    `json:"external_url"`
	Duration     int       `json:"duration"`
	Metadata     string    `json:"metadata"`
	FileHash     string    `json:"file_hash"`
	LocalPath    string    `json:"local_path"` // Path to downloaded local file
	ThumbURL     string    `json:"thumb_url"` // Thumbnail URL
	Status       string    `json:"status"`
	Error        string    `json:"error"`
	SearchTerms  []string  `json:"search_terms"` // Frasi di riferimento/query di ricerca che hanno portato al download
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	// Extended fields for clip catalog
	SearchText   string    `json:"search_text,omitempty"`
	SceneType    string    `json:"scene_type,omitempty"`
	QualityScore float64   `json:"quality_score,omitempty"`
	ReuseCount   int       `json:"reuse_count,omitempty"`
	LastUsedAt   string    `json:"last_used_at,omitempty"`
	UsableFor    []string  `json:"usable_for,omitempty"`
	AvoidFor     []string  `json:"avoid_for,omitempty"`
}

// IndexingCheckpoint represents a checkpoint for the indexing process
type IndexingCheckpoint struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	LastIndexedAt time.Time `json:"last_indexed_at"`
	Metadata      string    `json:"metadata"`
}
