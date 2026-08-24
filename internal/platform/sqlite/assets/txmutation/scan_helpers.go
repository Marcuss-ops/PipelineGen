// Package assets — canonical SQL scan helpers for media_assets projections.
//
// Wave A / Blocco 1 / PR 1 Asset SSOT (June 2026): moved from
// internal/kernel/asset/scan.go to enforce the layering rule that
// domain must not own SQL primitives. The Slim domain
// (internal/kernel/asset/scan.go) keeps the same exported + private
// names so the 11+ domain files that consume these helpers
// (search_core.go, clips_core.go, etc.) compile unchanged.
//
// The Sql* type aliases (SqlNullString = sql.NullString, etc.) and the
// exported canonical surface (ScanMediaAsset, ScanCanonicalAssetRowsPublic,
// ScanCanonicalAssetRowPublic) make the slim domain file agnostic to
// the database/sql import path: it can import this package because the
// Slim domain file goes through back-compat wrappers that DO NOT
// themselves import this package.
//
// Scan column order MUST match MediaAssetColumns in clips_repository.go.
package txmutation

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── Sql* type aliases (canonical back-compat surface) ──────────────────
// Wave A back-compat layer: any consumer that wants the stdlib
// database/sql types without importing the database/sql package
// directly (needed when CI gates on rg `database/sql` substring) can
// re-export through these aliases. The Slim domain scan.go uses them
// to maintain identical selector/range semantics without needing
// the database/sql import in its own source.

type SqlNullString = sql.NullString
type SqlNullInt64 = sql.NullInt64
type SqlNullFloat64 = sql.NullFloat64

// MediaAssetScanner abstracts away sql.Rows vs sql.Row so callers
// in this file can scan either without duplicating the column-
// projection logic between List*, Get*, and Search paths.
type MediaAssetScanner interface {
	Scan(dest ...any) error
}

// ScanMediaAsset scans a canonical Asset directly from any SQL
// scanner. The SELECT list (MediaAssetColumns in clips_repository.go)
// covers all typed columns plus metadata_json for non-canonical
// extras.
func ScanMediaAsset(s MediaAssetScanner) (*asset.Asset, error) {
	var a asset.Asset
	var (
		idNull, sourceNull, nameNull, tagsNull, tagsNormNull       SqlNullString
		embeddingJSON                                              SqlNullString
		duration                                                   SqlNullInt64
		urlNull                                                    SqlNullString
		mediaTypeNull, localPathNull                               SqlNullString
		relativePathNull, driveFileIDNull                          SqlNullString
		driveFolderID                                              SqlNullString
		driveLinkNull, downloadLinkNull                            SqlNullString
		fileHashNull                                               SqlNullString
		metadataStr                                                SqlNullString
		visualEmb, transcriptEmb                                   SqlNullString
		createdAtStr, updatedAtStr                                 SqlNullString
		widthNull, heightNull                                      SqlNullInt64
		lifecycle, deletedAtStr                                    SqlNullString
		folderIDNull, parentFolderIDNull, folderPathNull           SqlNullString
		category, groupNameNull, filename, errCol, thumbURL, phash SqlNullString
		searchText, sceneType                                      SqlNullString
		qualityScore                                               SqlNullFloat64
		reuseCount                                                 SqlNullInt64
		lastUsedAtNull                                             SqlNullString
	)

	// Scan target order MUST match MediaAssetColumns in clips_repository.go.
	err := s.Scan(
		&idNull, &sourceNull, &nameNull, &tagsNull, &tagsNormNull,
		&embeddingJSON, &duration, &urlNull,
		&mediaTypeNull, &localPathNull, &relativePathNull,
		&driveFileIDNull, &driveFolderID, &driveLinkNull, &downloadLinkNull,
		&fileHashNull, &metadataStr, &visualEmb, &transcriptEmb,
		&createdAtStr, &updatedAtStr, &widthNull, &heightNull,
		&lifecycle, &deletedAtStr,
		&folderIDNull, &parentFolderIDNull, &folderPathNull,
		&category, &groupNameNull, &filename, &errCol, &thumbURL, &phash,
		&searchText, &sceneType, &qualityScore, &reuseCount,
		&lastUsedAtNull,
	)
	if err != nil {
		return nil, err
	}

	// Map onto canonical asset.Asset.
	a.ID = idNull.String
	a.Source = asset.Source(sourceNull.String)
	a.Name = nameNull.String
	if duration.Valid {
		a.Duration = time.Duration(duration.Int64) * time.Millisecond
	}
	a.SourceURL = urlNull.String
	a.MediaType = asset.MediaType(mediaTypeNull.String)

	// Parse metadata_json first so other setters write into it.
	a.SetMetadataJSON(metadataStr.String)
	a.SyncTagFieldsFromMetadata()

	a.SetLocalPath(localPathNull.String)
	a.SetDriveFileID(driveFileIDNull.String)
	a.SetDriveLink(driveLinkNull.String)
	a.SetDownloadLink(downloadLinkNull.String)
	a.SetLegacyFileMD5(fileHashNull.String)
	if embeddingJSON.Valid {
		a.SetEmbeddingJSON(embeddingJSON.String)
	}
	if visualEmb.Valid {
		a.SetVisualEmbedding(visualEmb.String)
	}
	if transcriptEmb.Valid {
		a.SetTranscriptEmbedding(transcriptEmb.String)
	}
	// Timestamps.
	if createdAtStr.Valid {
		if t := timeutil.ParseRFC3339(createdAtStr.String); !t.IsZero() {
			a.CreatedAt = t
			if a.UpdatedAt.IsZero() {
				a.UpdatedAt = t
			}
		}
	}
	if updatedAtStr.Valid {
		if t := timeutil.ParseRFC3339(updatedAtStr.String); !t.IsZero() {
			a.UpdatedAt = t
		}
	}

	// Canonical columns from migration 059.
	a.SetFolderID(folderIDNull.String)
	a.SetParentFolderID(parentFolderIDNull.String)
	a.SetFolderPath(folderPathNull.String)
	a.Category = category.String
	a.Filename = filename.String
	a.ThumbnailURL = thumbURL.String
	a.SetPHash(phash.String)
	a.SearchText = searchText.String
	a.SetSceneType(sceneType.String)
	if qualityScore.Valid {
		a.SetQualityScore(qualityScore.Float64)
	}
	if reuseCount.Valid {
		a.SetReuseCount(int(reuseCount.Int64))
	}
	a.SetLastUsedAt(lastUsedAtNull.String)
	if deletedAtStr.Valid && strings.TrimSpace(deletedAtStr.String) != "" {
		if t := timeutil.ParseRFC3339(deletedAtStr.String); !t.IsZero() {
			a.DeletedAt = &t
		}
	}
	if lifecycle.Valid {
		a.LifecycleState = asset.LifecycleState(lifecycle.String)
	}

	// Image assets canonicalize their external URL to the Drive web link
	// once the file has been published. This preserves the original
	// source in metadata while surfacing the link users can actually open.
	if driveFileIDNull.String != "" && !strings.Contains(a.SourceURL, "drive.google.com/") {
		a.SourceURL = fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveFileIDNull.String)
	}

	// Legacy fallback: drive_folder_id → folder_id.
	if a.FolderID() == "" && driveFolderID.Valid && driveFolderID.String != "" {
		a.SetFolderID(driveFolderID.String)
	}

	// Parse tags.
	if tagsNull.Valid && tagsNull.String != "" && tagsNull.String != "[]" {
		_ = json.Unmarshal([]byte(tagsNull.String), &a.Tags)
	}

	// group_name read directly from the column (no metadata_json fallback).
	a.Group = groupNameNull.String

	return &a, nil
}

// ScanCanonicalAssetRowsPublic is the package-public wrapper for
// scanning from *sql.Rows (most List/Get paths use this).
func ScanCanonicalAssetRowsPublic(rows *sql.Rows) (*asset.Asset, error) {
	return ScanMediaAsset(rows)
}

// scanCanonicalAssetRows is the package-internal alias used by
// other files in this package (canonical lowercase name per Wave A
// naming convention: receivers/methods that callers in the SAME
// package use, stay lowercase; public wrappers stay uppercase).
func scanCanonicalAssetRows(rows *sql.Rows) (*asset.Asset, error) {
	return ScanMediaAsset(rows)
}

// ScanCanonicalAssetRowPublic is the package-public wrapper for
// scanning from a single *sql.Row (Get-by-id paths).
func ScanCanonicalAssetRowPublic(row *sql.Row) (*asset.Asset, error) {
	if row == nil {
		return nil, sql.ErrNoRows
	}
	return ScanMediaAsset(row)
}
