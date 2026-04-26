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
	Tags         []string  `json:"tags"`
	Source       string    `json:"source"`
	Category     string    `json:"category"`
	ExternalURL  string    `json:"external_url"`
	Duration     int       `json:"duration"`
	Metadata     string    `json:"metadata"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IndexingCheckpoint represents a checkpoint for the indexing process
type IndexingCheckpoint struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	LastIndexedAt time.Time `json:"last_indexed_at"`
	Metadata      string    `json:"metadata"`
}
