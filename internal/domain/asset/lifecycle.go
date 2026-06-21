package asset

import (
	"fmt"
	"strings"
	"time"
)

// ── Source provenance ───────────────────────────────────────────────

// SourceType identifies the origin of a media asset.
type SourceType string

const (
	// SourceStock indicates stock footage media.
	SourceStock SourceType = "stock"
	// SourceArtlist indicates media sourced from Artlist.
	SourceArtlist SourceType = "artlist"
	// SourceYoutubeClip indicates a clip from YouTube.
	SourceYoutubeClip SourceType = "youtube_clip"
	// SourceClipDrive indicates a clip sourced from Google Drive.
	SourceClipDrive SourceType = "clip_drive"
	// SourceImage indicates an image asset.
	SourceImage SourceType = "image"
	// SourceGenerated indicates generated content (script, voiceover).
	SourceGenerated SourceType = "generated"
	// SourceSoundEffect indicates a sound effect asset.
	SourceSoundEffect SourceType = "sound_effect"
)

// IsValid reports whether the SourceType matches a known constant.
func (s SourceType) IsValid() bool {
	switch s {
	case SourceStock, SourceArtlist, SourceYoutubeClip, SourceClipDrive, SourceImage, SourceGenerated, SourceSoundEffect:
		return true
	}
	return false
}

// ── Asset status ─────────────────────────────────────────────────────

// AssetStatus tracks the lifecycle of a media asset.
type AssetStatus string

const (
	// AssetStatusActive indicates an active and available asset.
	AssetStatusActive AssetStatus = "active"
	// AssetStatusArchived indicates an archived asset.
	AssetStatusArchived AssetStatus = "archived"
	// AssetStatusDeleted indicates a soft-deleted asset.
	AssetStatusDeleted AssetStatus = "deleted"
	// AssetStatusProcessing indicates an asset currently being processed.
	AssetStatusProcessing AssetStatus = "processing"
	// AssetStatusFailed indicates an asset with a processing error.
	AssetStatusFailed AssetStatus = "failed"
)

// IsValid reports whether the AssetStatus matches a known constant.
func (s AssetStatus) IsValid() bool {
	switch s {
	case AssetStatusActive, AssetStatusArchived, AssetStatusDeleted, AssetStatusProcessing, AssetStatusFailed:
		return true
	}
	return false
}

// ── Tree node (API response shape) ──────────────────────────────────

// AssetNode represents a node in the asset tree hierarchy for API responses.
type AssetNode struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	AssetID     string `json:"asset_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ParentID    string `json:"parent_id"`
	RootID      string `json:"root_id"`
	Path        string `json:"path"`
	Depth       int    `json:"depth"`
	IsFolder    bool   `json:"is_folder"`
	DriveFileID string `json:"drive_file_id"`
	DriveLink   string `json:"drive_link"`
	Metadata    string `json:"metadata"`
	ChildCount  int    `json:"child_count,omitempty"`
}

// ── Indexing checkpoint ─────────────────────────────────────────────

// IndexingCheckpoint represents a checkpoint for the indexing process.
type IndexingCheckpoint struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	LastIndexedAt time.Time `json:"last_indexed_at"`
	Metadata      string    `json:"metadata"`
}

// ── Pipeline strategy ───────────────────────────────────────────────

// PipelineStrategy controls how existing data is handled during processing.
type PipelineStrategy string

const (
	StrategyVerify  PipelineStrategy = "verify"
	StrategySkip    PipelineStrategy = "skip"
	StrategyReplace PipelineStrategy = "replace"
)

// NormalizeStrategy coerces arbitrary user input to a known PipelineStrategy
// value. Unknown inputs default to StrategyVerify unless force is true, in
// which case they coerce to StrategyReplace.
func NormalizeStrategy(strategy string, force bool) PipelineStrategy {
	s := PipelineStrategy(strings.ToLower(strings.TrimSpace(strategy)))
	switch s {
	case StrategySkip, StrategyVerify, StrategyReplace:
		return s
	}
	if force {
		return StrategyReplace
	}
	return StrategyVerify
}

// ActiveKey produces a deterministic enqueue dedup key for jobs in the
// inactive/active-pending state.
func ActiveKey(prefix, term, folderID string, strategy string, dryRun bool) string {
	return fmt.Sprintf("%s|%s|%s|%s|%t",
		prefix,
		term,
		folderID,
		strategy,
		dryRun,
	)
}

// ── Monitored source ─────────────────────────────────────────────────

// MonitoredSource represents a discovered external source (YouTube video,
// Artlist asset, Drive file, etc.). The SQLite persistence shape (column
// names, table name) lives in
// internal/infrastructure/database/sqlite/assets.MonitoredSourceRow; the
// repository converts via FromMonitoredSourceDomain / row.ToDomain so the
// domain layer has zero knowledge of the underlying schema (PR4.B,
// June 2026).
type MonitoredSource struct {
	ID             string `json:"id"`
	Source         string `json:"source"`
	ExternalID     string `json:"external_id"`
	ExternalURL    string `json:"external_url"`
	Title          string `json:"title"`
	ChannelID      string `json:"channel_id"`
	ChannelURL     string `json:"channel_url"`
	Keyword        string `json:"keyword"`
	GroupName      string `json:"group_name"`
	Category       string `json:"category"`
	Status         string `json:"status"`
	LastSeenAt     string `json:"last_seen_at"`
	LastCheckedAt  string `json:"last_checked_at"`
	ProcessedCount int    `json:"processed_count"`
	MetadataJSON   string `json:"metadata_json"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}
