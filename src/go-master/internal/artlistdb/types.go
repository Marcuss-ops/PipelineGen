package artlistdb

import (
	"sync"
)

// ArtlistDB is a local JSON database of Artlist search results.
type ArtlistDB struct {
	path string
	data *ArtlistData
	mu   sync.RWMutex
}

// ArtlistData holds all clips organized by search term.
type ArtlistData struct {
	Searches    map[string]SearchResult `json:"searches"`
	TotalClips  int                     `json:"total_clips"`
	LastUpdated string                  `json:"last_updated"`
}

// SearchResult holds clips found for a specific search term.
type SearchResult struct {
	Term              string        `json:"term"`
	Clips             []ArtlistClip `json:"clips"`
	SearchedAt        string        `json:"searched_at"`
	DownloadedClipIDs []string      `json:"downloaded_clip_ids"`
	DriveFolderID     string        `json:"drive_folder_id"`
}

// ArtlistClip represents a single Artlist clip from search.
type ArtlistClip struct {
	ID             string   `json:"id"`
	VideoID        string   `json:"video_id"`
	Title          string   `json:"title"`
	FileID         string   `json:"file_id,omitempty"`
	Name           string   `json:"name,omitempty"`
	Term           string   `json:"term,omitempty"`
	Folder         string   `json:"folder,omitempty"`
	FolderID       string   `json:"folder_id,omitempty"`
	OriginalURL    string   `json:"original_url"`
	URL            string   `json:"url"`
	DriveFileID    string   `json:"drive_file_id,omitempty"`
	DriveURL       string   `json:"drive_url,omitempty"`
	LocalPathDrive string   `json:"local_path_drive,omitempty"`
	DownloadPath   string   `json:"download_path,omitempty"`
	Duration       int      `json:"duration"`
	Width          int      `json:"width"`
	Height         int      `json:"height"`
	Category       string   `json:"category"`
	Tags           []string `json:"tags"`
	Embedding      string   `json:"embedding,omitempty"`
	VisualHash     string   `json:"visual_hash,omitempty"`
	Downloaded     bool     `json:"downloaded"`
	AddedAt        string   `json:"added_at"`
	DownloadedAt   string   `json:"downloaded_at,omitempty"`
	UsedInVideos   []string `json:"used_in_videos,omitempty"`
}

// DBStats holds typed database statistics.
type DBStats struct {
	TotalSearches   int    `json:"total_searches"`
	TotalClips      int    `json:"total_clips"`
	TotalDownloaded int    `json:"total_downloaded"`
	LastUpdated     string `json:"last_updated"`
}

type DedupStats struct {
	CanonicalKept    int      `json:"canonical_kept"`
	DuplicateMarked  int      `json:"duplicate_marked"`
	DriveIDsToDelete []string `json:"drive_ids_to_delete"`
}
