// Package media defines canonical domain types for media assets,
// clip folders, images, and source providers.
package media

import "time"

// ── Enums ───────────────────────────────────────────────────────────

// SourceType identifies the origin of a media asset.
type SourceType string

const (
	SourceStock      SourceType = "stock"
	SourceArtlist    SourceType = "artlist"
	SourceYoutubeClip SourceType = "youtube_clip"
	SourceClipDrive  SourceType = "clip_drive"
	SourceImage      SourceType = "image"
	SourceGenerated  SourceType = "generated"
)

func (s SourceType) IsValid() bool {
	switch s {
	case SourceStock, SourceArtlist, SourceYoutubeClip, SourceClipDrive, SourceImage, SourceGenerated:
		return true
	}
	return false
}

// MediaType classifies the content type of a media asset.
type MediaType string

const (
	MediaTypeStock     MediaType = "stock"
	MediaTypeClip      MediaType = "clip"
	MediaTypeImage     MediaType = "image"
	MediaTypeAudio     MediaType = "audio"
	MediaTypeDocument  MediaType = "document"
)

func (m MediaType) IsValid() bool {
	switch m {
	case MediaTypeStock, MediaTypeClip, MediaTypeImage, MediaTypeAudio, MediaTypeDocument:
		return true
	}
	return false
}

// AssetStatus tracks the lifecycle of a media asset.
type AssetStatus string

const (
	AssetStatusActive      AssetStatus = "active"
	AssetStatusArchived    AssetStatus = "archived"
	AssetStatusDeleted     AssetStatus = "deleted"
	AssetStatusProcessing  AssetStatus = "processing"
	AssetStatusFailed      AssetStatus = "failed"
)

func (s AssetStatus) IsValid() bool {
	switch s {
	case AssetStatusActive, AssetStatusArchived, AssetStatusDeleted, AssetStatusProcessing, AssetStatusFailed:
		return true
	}
	return false
}

// ── Tree / Hierarchy ────────────────────────────────────────────────

// AssetNode represents a node in the asset tree hierarchy.
type AssetNode struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	AssetID      string `json:"asset_id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	ParentID     string `json:"parent_id,omitempty"`
	RootID       string `json:"root_id,omitempty"`
	Path         string `json:"path,omitempty"`
	Depth        int    `json:"depth"`
	IsFolder     bool   `json:"is_folder"`
	DriveFileID  string `json:"drive_file_id,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	Metadata     string `json:"metadata,omitempty"`
	ChildCount   int    `json:"child_count,omitempty"`
}

// ── Processing Result ───────────────────────────────────────────────

// AssetExecutionResult is a unified result type for asset processing.
type AssetExecutionResult struct {
	ID          string `json:"id,omitempty"`
	Source      string `json:"source,omitempty"`
	MediaType   string `json:"media_type,omitempty"`
	Filename    string `json:"filename,omitempty"`
	LocalPath   string `json:"local_path,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	DownloadLink string `json:"download_link,omitempty"`
	FileHash    string `json:"file_hash,omitempty"`
	Status      string `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ── Clip Folders ────────────────────────────────────────────────────

// ClipFolder represents a folder of clips from a single video source.
type ClipFolder struct {
	ID               string    `json:"id"`
	Source           string    `json:"source"`
	SourceURL        string    `json:"source_url"`
	VideoID          string    `json:"video_id,omitempty"`
	FolderID         string    `json:"folder_id"`
	FolderPath       string    `json:"folder_path"`
	LocalFolderPath  string    `json:"local_folder_path"`
	Group            string    `json:"group_name"`
	ManifestTXTPath  string    `json:"manifest_txt_path"`
	ManifestJSONPath string    `json:"manifest_json_path"`
	ClipCount        int       `json:"clip_count"`
	ProcessedCount   int       `json:"processed_count"`
	FailedCount      int       `json:"failed_count"`
	SkippedCount     int       `json:"skipped_count"`
	LastError        string    `json:"last_error,omitempty"`
	Metadata         string    `json:"metadata,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ── Indexing ────────────────────────────────────────────────────────

// IndexingCheckpoint tracks the last indexing state for an asset.
type IndexingCheckpoint struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	LastIndexedAt time.Time `json:"last_indexed_at"`
	Metadata      string    `json:"metadata"`
}

// ── Pipeline Strategy ───────────────────────────────────────────────

// PipelineStrategy controls how existing data is handled during processing.
type PipelineStrategy string

const (
	StrategyVerify  PipelineStrategy = "verify"
	StrategySkip    PipelineStrategy = "skip"
	StrategyReplace PipelineStrategy = "replace"
)

// ── Monitoring ──────────────────────────────────────────────────────

// MonitoredSource represents a YouTube channel or playlist being monitored.
type MonitoredSource struct {
	ID              string `json:"id"`
	Source          string `json:"source"`
	ExternalID      string `json:"external_id"`
	ExternalURL     string `json:"external_url"`
	Title           string `json:"title"`
	ChannelID       string `json:"channel_id"`
	ChannelURL      string `json:"channel_url"`
	Keyword         string `json:"keyword"`
	GroupName       string `json:"group_name"`
	Category        string `json:"category"`
	Status          string `json:"status"`
	LastSeenAt      string `json:"last_seen_at"`
	LastCheckedAt   string `json:"last_checked_at"`
	ProcessedCount  int    `json:"processed_count"`
	MetadataJSON    string `json:"metadata_json"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}
