package models

import "time"

// ClipFolder represents a folder containing multiple clips from the same source
type ClipFolder struct {
	ID                string    `json:"id"`
	Source            string    `json:"source"` // youtube, stock, etc.
	SourceURL         string    `json:"source_url"`
	VideoID           string    `json:"video_id,omitempty"`
	FolderID          string    `json:"folder_id"`          // Drive folder ID
	FolderPath        string    `json:"folder_path"`        // Drive folder path
	LocalFolderPath   string    `json:"local_folder_path"`  // Local folder path
	Group             string    `json:"group"`
	ManifestTXTPath   string    `json:"manifest_txt_path"`  // Path to clip_manifest.txt
	ManifestJSONPath  string    `json:"manifest_json_path"` // Path to clip_manifest.json
	ClipCount         int       `json:"clip_count"`
	ProcessedCount    int       `json:"processed_count"`
	FailedCount       int       `json:"failed_count"`
	SkippedCount      int       `json:"skipped_count"`
	LastError         string    `json:"last_error,omitempty"`
	Metadata          string    `json:"metadata,omitempty"` // JSON metadata
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ClipManifest represents the JSON manifest for a clip folder
type ClipManifest struct {
	ID              string            `json:"id"`
	FolderID        string            `json:"folder_id"`
	FolderPath      string            `json:"folder_path"`
	Source          string            `json:"source"`
	SourceURL       string            `json:"source_url"`
	VideoID         string            `json:"video_id,omitempty"`
	LocalFolderPath string            `json:"local_folder_path"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Stats           ClipFolderStats   `json:"stats"`
	Clips           []ClipManifestItem `json:"clips"`
}

// ClipFolderStats represents aggregated statistics for the folder
type ClipFolderStats struct {
	ClipCount      int `json:"clip_count"`
	ProcessedCount int `json:"processed_count"`
	FailedCount    int `json:"failed_count"`
	SkippedCount   int `json:"skipped_count"`
}

// ClipManifestItem represents a clip entry in the manifest
type ClipManifestItem struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Start           string `json:"start"`
	End             string `json:"end"`
	StartSeconds    int    `json:"start_seconds"`
	EndSeconds      int    `json:"end_seconds"`
	DurationSeconds int    `json:"duration_seconds"`
	Filename        string `json:"filename,omitempty"`
	LocalPath       string `json:"local_path,omitempty"`
	DriveLink       string `json:"drive_link,omitempty"`
	FileHash        string `json:"file_hash,omitempty"`
	Status          string `json:"status"`
	Tags            string `json:"tags,omitempty"` // JSON array
}
