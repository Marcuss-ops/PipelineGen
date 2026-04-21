package stockdb

import (
	"time"
)

// StockDatabase is the root JSON structure (kept for legacy migration)
type StockDatabase struct {
	LastSynced time.Time          `json:"last_synced"`
	Folders    []StockFolderEntry `json:"folders"`
	Clips      []StockClipEntry   `json:"clips"`
}

// StockFolderEntry matches the exact schema
type StockFolderEntry struct {
	TopicSlug  string    `json:"topic_slug"`  // PK, e.g. "gervonta-davis"
	DriveID    string    `json:"drive_id"`    // Google Drive folder ID
	ParentID   string    `json:"parent_id"`   // Parent folder Drive ID
	FullPath   string    `json:"full_path"`   // e.g. "stock/Boxe/GervontaDavis"
	Section    string    `json:"section"`     // "stock" or "clips" — KEY for fast lookup
	LastSynced time.Time `json:"last_synced"` // When last scanned
}

// StockClipEntry matches the exact schema
type StockClipEntry struct {
	ClipID   string   `json:"clip_id"`             // PK, Drive file ID
	FolderID string   `json:"folder_id"`           // FK → stock_folders.drive_id
	Filename string   `json:"filename"`            // e.g. "knockout_garcia.mp4"
	Source   string   `json:"source"`              // "artlist" or "stock"
	Tags     []string `json:"tags"`                // comma-separated keywords
	Duration int      `json:"duration"`            // seconds
	Status   string   `json:"status,omitempty"`    // queued|processing|failed|uploaded
	ErrorLog string   `json:"error_log,omitempty"` // latest error for retries/debug
}
