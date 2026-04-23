package catalogdb

import (
	"database/sql"
	"time"
)

// CatalogDB provides a normalized local SQLite catalog for clips.
type CatalogDB struct {
	db       *sql.DB
	path     string
	ftsReady bool
}

// Clip represents a normalized clip entry in the catalog.
type Clip struct {
	ID            string    `json:"id"`
	Source        string    `json:"source"`
	SourceID      string    `json:"source_id"`
	Provider      string    `json:"provider,omitempty"`
	Title         string    `json:"title,omitempty"`
	Description   string    `json:"description,omitempty"`
	Filename      string    `json:"filename,omitempty"`
	Category      string    `json:"category,omitempty"`
	FolderID      string    `json:"folder_id,omitempty"`
	FolderPath    string    `json:"folder_path,omitempty"`
	DriveFileID   string    `json:"drive_file_id,omitempty"`
	DriveURL      string    `json:"drive_url,omitempty"`
	ExternalPath  string    `json:"external_path,omitempty"`
	LocalPath     string    `json:"local_path,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
	DurationSec   int       `json:"duration_sec,omitempty"`
	Width         int       `json:"width,omitempty"`
	Height        int       `json:"height,omitempty"`
	MimeType      string    `json:"mime_type,omitempty"`
	FileExt       string    `json:"file_ext,omitempty"`
	FileSizeBytes int64     `json:"file_size_bytes,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	ModifiedAt    time.Time `json:"modified_at,omitempty"`
	LastSyncedAt  time.Time `json:"last_synced_at,omitempty"`
	IsActive      bool      `json:"is_active"`
	MetadataJSON  string    `json:"metadata_json,omitempty"`
}

// SyncState persists the sync cursor and timestamps for a given source.
type SyncState struct {
	Source            string    `json:"source"`
	Cursor            string    `json:"cursor,omitempty"`
	LastFullScanAt    time.Time `json:"last_full_scan_at,omitempty"`
	LastIncrementalAt time.Time `json:"last_incremental_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// SearchOptions defines filters for catalog searching.
type SearchOptions struct {
	Query       string `json:"query"`
	Source      string `json:"source,omitempty"`
	FolderID    string `json:"folder_id,omitempty"`
	MinDuration int    `json:"min_duration,omitempty"`
	MaxDuration int    `json:"max_duration,omitempty"`
	OnlyActive  bool   `json:"only_active,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// SearchResult wraps a clip with its relevance score.
type SearchResult struct {
	Clip  Clip    `json:"clip"`
	Score float64 `json:"score"`
}

const (
	SourceArtlist    = "artlist"
	SourceClipDrive  = "clips"
	SourceStockDrive = "stock"
)
