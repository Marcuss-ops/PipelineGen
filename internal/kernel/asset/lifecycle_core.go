// Package asset — lifecycle state + pipeline-strategy DTOs
// (Wave C / Phase 3 slim).
//
// Phase 3 (Wave C / Blocco 1 Asset SSOT, June 2026): the 3 SQL
// receivers that used to live here (GetCurrentVersion/ListVersions/
// AppendVersion) + the scanVersion helper + the
// versionRepositoryAdapter struct + the VersionRepository() factory
// were relocated to Local infra at
// internal/platform/sqlite/assets/version_queries.go,
// reachable via HYBRID-embed promotion through the legacy
// asset store struct.
//
// This file now carries ONLY the canonical type surface: SourceType
// enum, AssetNode API shape, IndexingCheckpoint, PipelineStrategy enum
// + NormalizeStrategy/ActiveKey/MonitoredSource. NO SQL primitives,
// NO `database/sql` import.
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

// validSourceTypes is the canonical set of known SourceType values.
// The map lookup keeps the C2-C AST check deterministic rather than relying on
// detection (godlike/06 SSOT co-located structural validation: the
// enum declaration and its membership test live in the same file).
var validSourceTypes = map[SourceType]struct{}{
	SourceStock:       {},
	SourceArtlist:     {},
	SourceYoutubeClip: {},
	SourceClipDrive:   {},
	SourceImage:       {},
	SourceGenerated:   {},
	SourceSoundEffect: {},
}

// IsValid reports whether the SourceType matches a known constant.
func (s SourceType) IsValid() bool {
	_, ok := validSourceTypes[s]
	return ok
}

// ── Tree node (API response shape) ──────────────────────────────────

// ── AssetStatus enum REMOVED ────────────────────────────────────────
//
// Pre-PR1 (Lifecycle state SSOT, June 2026), this file declared a
// separate lowercase `AssetStatus` enum (active/archived/deleted/
// processing/failed) that co-existed with `LifecycleState` and the
// `status` column in media_assets. The two enums polluted writers
// (lowercase strings in production code) and readers (the COALESCE
// fallback in qdrant/asset_store.go). PR 1 retired AssetStatus and
// the `status` column so `LifecycleState` is the single source of
// truth at every layer (enum, column, payload, search filter).
//
// Any future reintroduction of an archived/failed state must add a
// new LifecycleState constant instead of reviving AssetStatus —
// reintroducing a parallel enum is what created the drift in the
// first place.

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
// internal/platform/sqlite/assets.MonitoredSourceRow; the
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
