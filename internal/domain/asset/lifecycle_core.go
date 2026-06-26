package asset

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
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

type versionRepositoryAdapter struct {
	store *AssetStoreSQLite
}

func (a *versionRepositoryAdapter) GetCurrent(ctx context.Context, assetID string) (*Version, error) {
	return a.store.GetCurrentVersion(ctx, assetID)
}

func (a *versionRepositoryAdapter) List(ctx context.Context, assetID string) ([]Version, error) {
	return a.store.ListVersions(ctx, assetID)
}

func (a *versionRepositoryAdapter) Append(ctx context.Context, v *Version) error {
	return a.store.AppendVersion(ctx, v)
}

// VersionRepository returns the VersionRepository adapter for the store.
func (s *AssetStoreSQLite) VersionRepository() VersionRepository {
	return &versionRepositoryAdapter{store: s}
}

// GetCurrentVersion returns the latest version for an asset.
func (s *AssetStoreSQLite) GetCurrentVersion(ctx context.Context, assetID string) (*Version, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at
		FROM asset_versions
		WHERE asset_id = ?
		ORDER BY version_number DESC LIMIT 1
	`, assetID)
	return scanVersion(row)
}

// ListVersions returns all versions for an asset.
func (s *AssetStoreSQLite) ListVersions(ctx context.Context, assetID string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at
		FROM asset_versions
		WHERE asset_id = ?
		ORDER BY version_number DESC
	`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Version
	for rows.Next() {
		ver, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ver)
	}
	return out, rows.Err()
}

func (s *AssetStoreSQLite) AppendVersion(ctx context.Context, v *Version) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var nextVer int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_number), 0) + 1 FROM asset_versions WHERE asset_id = ?`, v.AssetID).Scan(&nextVer)
	if err != nil {
		return err
	}

	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err = tx.ExecContext(ctx, `
		INSERT INTO asset_versions
			(asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, v.AssetID, nextVer, v.SourceURI, v.FileHash, v.FileSizeBytes, v.MimeType, v.MetadataJSON, nowStr)
	if err != nil {
		return err
	}

	v.VersionNumber = nextVer
	return tx.Commit()
}

func scanVersion(scanner interface{ Scan(dest ...any) error }) (*Version, error) {
	var v Version
	var sourceURI, fileHash, mimeType, metaJSON, createdAtStr sql.NullString
	err := scanner.Scan(
		&v.ID, &v.AssetID, &v.VersionNumber, &sourceURI, &fileHash, &v.FileSizeBytes, &mimeType, &metaJSON, &createdAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.SourceURI = sourceURI.String
	v.FileHash = fileHash.String
	v.MimeType = mimeType.String
	v.MetadataJSON = metaJSON.String
	v.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
	return &v, nil
}
