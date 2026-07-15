// Package asset — Asset projection scanner (Wave C / Phase 3 slim).
//
// Phase 3 (Wave C / Blocco 1 Asset SSOT, June 2026): the previous
// version of this file used `database/sql` imports for two reasons:
// (a) `*sql.Rows` / `*sql.Row` in arg signatures, and (b)
// sql.NullString / sql.NullInt64 / sql.NullFloat64 to handle nullable
// columns.
//
// Both were dropped:
//
//   - The arg signatures now use the unexported `mediaAssetScanner`
//     interface satisfied by any value with a `Scan(dest ...any) error`
//     method (including `*sql.Rows` and `*sql.Row`, via Go's
//     structural typing — no `database/sql` import needed in this
//     domain file).
//   - All `sql.NullString/Int64/Float64` locals became bare
//     `string`/`int64`/`float64` because the projection in
//     `mediaAssetColumns` (canonical in Local infra clips_repository.go)
//     wraps every nullable column with `COALESCE(<col>, <default>)`,
//     so the receiver sees concrete non-null values everywhere — the
//     `.Valid` check was redundant and gone.
//
// The exported `ScanCanonicalAssetRowsPublic` and
// `ScanCanonicalAssetRowPublic` keep their names so existing callers
// (cross-package use of the public scan surface) compile unchanged
// after the migration.
package asset

import (
	"encoding/json"
	"strings"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// mediaAssetScanner abstracts away the SQL row source so the same
// scanner implementation handles `sql.Rows` (many rows) and `sql.Row`
// (single row) without duplicating the column projection. As long as
// the underlying type exposes `Scan(dest ...any) error` it satisfies
// this interface (Go structural typing — no `database/sql` import
// needed here).
type mediaAssetScanner interface {
	Scan(dest ...any) error
}

// scanMediaAsset scans a canonical Asset directly from any SQL
// scanner. The SELECT list (mediaAssetColumns in Local infra
// clips_repository.go) covers all typed columns plus metadata_json for
// non-canonical extras.
func scanMediaAsset(s mediaAssetScanner) (*Asset, error) {
	var a Asset
	var (
		id, sourceStr, name, tags, tagsNorm                    string
		embeddingJSON                                          string
		duration                                               int64
		urlStr                                                 string
		mediaType, localPath, relativePath                     string
		driveFileID, driveFolderID, driveLink, downloadLink    string
		fileHash, metadataStr, visualEmb, transcriptEmb        string
		createdAtStr, updatedAtStr                             string
		width, height                                          int64
		lifecycle, deletedAtStr                                string
		folderID, parentFolderID, folderPath                   string
		category, groupName, filename, errCol, thumbURL, phash string
		searchText, sceneType                                  string
		qualityScore                                           float64
		reuseCount                                             int64
		lastUsedAt                                             string
	)

	// Scan target order MUST match mediaAssetColumns in clips_repository.go.
	err := s.Scan(
		&id, &sourceStr, &name, &tags, &tagsNorm,
		&embeddingJSON, &duration, &urlStr,
		&mediaType, &localPath, &relativePath,
		&driveFileID, &driveFolderID, &driveLink, &downloadLink,
		&fileHash, &metadataStr, &visualEmb, &transcriptEmb,
		&createdAtStr, &updatedAtStr, &width, &height,
		&lifecycle, &deletedAtStr,
		&folderID, &parentFolderID, &folderPath,
		&category, &groupName, &filename, &errCol, &thumbURL, &phash,
		&searchText, &sceneType, &qualityScore, &reuseCount,
		&lastUsedAt,
	)
	if err != nil {
		return nil, err
	}

	// Map onto canonical Asset.
	a.ID = id
	a.Source = Source(sourceStr)
	a.Name = name
	if duration != 0 {
		a.Duration = time.Duration(duration) * time.Millisecond
	}
	a.SourceURL = urlStr
	a.MediaType = MediaType(mediaType)

	// Parse metadata_json first so other setters write into it.
	a.SetMetadataJSON(metadataStr)

	a.SetLocalPath(localPath)
	a.SetDriveFileID(driveFileID)
	a.SetDriveLink(driveLink)
	a.SetDownloadLink(downloadLink)
	a.SetFileHash(fileHash)
	if embeddingJSON != "" {
		a.SetEmbeddingJSON(embeddingJSON)
	}
	if visualEmb != "" {
		a.SetVisualEmbedding(visualEmb)
	}
	if transcriptEmb != "" {
		a.SetTranscriptEmbedding(transcriptEmb)
	}

	// Timestamps.
	if createdAtStr != "" {
		if t := timeutil.ParseRFC3339(createdAtStr); !t.IsZero() {
			a.CreatedAt = t
			if a.UpdatedAt.IsZero() {
				a.UpdatedAt = t
			}
		}
	}
	if updatedAtStr != "" {
		if t := timeutil.ParseRFC3339(updatedAtStr); !t.IsZero() {
			a.UpdatedAt = t
		}
	}

	// Canonical columns from migration 059.
	a.SetFolderID(folderID)
	a.SetParentFolderID(parentFolderID)
	a.SetFolderPath(folderPath)
	a.Category = category
	a.Filename = filename
	a.ThumbnailURL = thumbURL
	a.SetPHash(phash)
	a.SearchText = searchText
	a.SetSceneType(sceneType)
	if qualityScore != 0 {
		a.SetQualityScore(qualityScore)
	}
	if reuseCount != 0 {
		a.SetReuseCount(int(reuseCount))
	}
	a.SetLastUsedAt(lastUsedAt)
	if deletedAtStr != "" && strings.TrimSpace(deletedAtStr) != "" {
		if t := timeutil.ParseRFC3339(deletedAtStr); !t.IsZero() {
			a.DeletedAt = &t
		}
	}
	if lifecycle != "" {
		a.LifecycleState = LifecycleState(lifecycle)
	}

	// Legacy fallback: drive_folder_id → folder_id.
	if a.FolderID() == "" && driveFolderID != "" {
		a.SetFolderID(driveFolderID)
	}

	// Parse tags.
	if tags != "" && tags != "[]" {
		_ = json.Unmarshal([]byte(tags), &a.Tags)
	}

	// group_name read directly from the column (no metadata_json fallback).
	a.Group = groupName

	return &a, nil
}

// scanCanonicalAssetRows scans a canonical asset from any SQL scanner.
// Public alias retained for cross-package callers that handle scan
// results through the explicit type; the underlying call delegates to
// scanMediaAsset for the canonical column layout.
func scanCanonicalAssetRows(rows mediaAssetScanner) (*Asset, error) {
	return scanMediaAsset(rows)
}

// scanCanonicalAssetRow scans a single canonical asset from any SQL
// scanner (typically `*sql.Row` via interface satisfaction).
func (s *AssetStoreSQLite) scanCanonicalAssetRow(row mediaAssetScanner) (*Asset, error) {
	return scanMediaAsset(row)
}

// ScanCanonicalAssetRowsPublic is an exported wrapper for scanCanonicalAssetRows.
func ScanCanonicalAssetRowsPublic(rows mediaAssetScanner) (*Asset, error) {
	return scanCanonicalAssetRows(rows)
}

// ScanCanonicalAssetRowPublic is an exported wrapper for scanCanonicalAssetRow.
func (s *AssetStoreSQLite) ScanCanonicalAssetRowPublic(row mediaAssetScanner) (*Asset, error) {
	return s.scanCanonicalAssetRow(row)
}
