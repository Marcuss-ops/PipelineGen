package clip

import (
	"encoding/json"
	"time"
)

// MediaType constants
const (
	MediaTypeClip  = "clip"
	MediaTypeStock = "stock"
)

// ClipIndex represents the full clip index
type ClipIndex struct {
	Version      string          `json:"version"`
	LastSync     time.Time       `json:"last_sync"`
	RootFolderID string          `json:"root_folder_id"`
	Clips        []IndexedClip   `json:"clips"`
	Folders      []IndexedFolder `json:"folders"`
	Stats        IndexStats      `json:"stats"`
}

// IndexedClip represents a clip in the index with enriched metadata
type IndexedClip struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Filename     string    `json:"filename"`
	FolderID     string    `json:"folder_id"`
	FolderPath   string    `json:"folder_path"`
	Group        string    `json:"group"`
	MediaType    string    `json:"media_type"` // "clip" or "stock"
	DriveLink    string    `json:"drive_link"`
	DownloadLink string    `json:"download_link,omitempty"`
	Duration     float64   `json:"duration"`
	Resolution   string    `json:"resolution"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	Size         int64     `json:"size"`
	MimeType     string    `json:"mime_type"`
	Tags         []string  `json:"tags"`
	Description  string    `json:"description,omitempty"`
	ModifiedAt   time.Time `json:"modified_at"`
	IndexedAt    time.Time `json:"indexed_at"`
}

// IndexedFolder represents a folder in the index
type IndexedFolder struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Path           string    `json:"path"`
	ParentID       string    `json:"parent_id"`
	Group          string    `json:"group"`
	ClipCount      int       `json:"clip_count"`
	SubfolderCount int       `json:"subfolder_count"`
	ModifiedAt     time.Time `json:"modified_at"`
	IndexedAt      time.Time `json:"indexed_at"`
}

// IndexStats holds index statistics
type IndexStats struct {
	TotalClips       int            `json:"total_clips"`
	TotalFolders     int            `json:"total_folders"`
	ClipsByGroup     map[string]int `json:"clips_by_group"`
	ClipsByMediaType map[string]int `json:"clips_by_media_type"`
	LastScanDuration time.Duration  `json:"last_scan_duration"`
}

// SearchFilters holds search filter options
type SearchFilters struct {
	Group       string   `json:"group"`
	MediaType   string   `json:"media_type"` // "clip" or "stock"
	FolderID    string   `json:"folder_id"`
	MinDuration float64  `json:"min_duration"`
	MaxDuration float64  `json:"max_duration"`
	Resolution  string   `json:"resolution"`
	Tags        []string `json:"tags"`
	Limit       int      `json:"limit"`
	Offset      int      `json:"offset"`
}

// MarshalJSON custom marshaling to handle maps with nil safety
func (s IndexStats) MarshalJSON() ([]byte, error) {
	type Alias IndexStats
	clipsByGroup := s.ClipsByGroup
	if clipsByGroup == nil {
		clipsByGroup = make(map[string]int)
	}
	clipsByMediaType := s.ClipsByMediaType
	if clipsByMediaType == nil {
		clipsByMediaType = make(map[string]int)
	}
	return json.Marshal(&struct {
		ClipsByGroup     map[string]int `json:"clips_by_group"`
		ClipsByMediaType map[string]int `json:"clips_by_media_type"`
		*Alias
	}{
		ClipsByGroup:     clipsByGroup,
		ClipsByMediaType: clipsByMediaType,
		Alias:            (*Alias)(&s),
	})
}
