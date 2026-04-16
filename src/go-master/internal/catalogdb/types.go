package catalogdb

import "time"

const (
	SourceArtlist   = "artlist"
	SourceClipDrive = "clip_drive"
	SourceStockDrive = "stock_drive"
)

// Clip represents a normalized clip record stored in the unified local catalog.
type Clip struct {
	ID            string
	Source        string
	SourceID      string
	Provider      string
	Title         string
	Description   string
	Filename      string
	Category      string
	FolderID      string
	FolderPath    string
	DriveFileID   string
	DriveURL      string
	ExternalPath  string
	LocalPath     string
	Tags          []string
	DurationSec   int
	Width         int
	Height        int
	MimeType      string
	FileExt       string
	FileSizeBytes int64
	CreatedAt     time.Time
	ModifiedAt    time.Time
	LastSyncedAt  time.Time
	IsActive      bool
	MetadataJSON  string
}

// SearchOptions defines the local search criteria used by the suggestion layer.
type SearchOptions struct {
	Query        string
	Source       string
	FolderID     string
	Limit        int
	MinDuration  int
	MaxDuration  int
	OnlyActive   bool
}

// SearchResult is a ranked catalog search hit.
type SearchResult struct {
	Clip  Clip
	Score float64
}

// SyncState tracks source-level synchronization cursors and timestamps.
type SyncState struct {
	Source            string
	Cursor            string
	LastFullScanAt    time.Time
	LastIncrementalAt time.Time
	UpdatedAt         time.Time
}
