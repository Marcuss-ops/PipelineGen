package asset

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// mediaAssetScanner abstracts away sql.Rows vs sql.Row so callers
// in this file can scan either without duplicating the column-projection
// logic between List*, Get*, and Search paths.
type mediaAssetScanner interface {
	Scan(dest ...any) error
}

// scanMediaAsset scans a canonical Asset directly from any SQL scanner.
// The SELECT list (mediaAssetColumns in repository.go) covers all typed columns plus
// metadata_json for non-canonical extras.
func scanMediaAsset(s mediaAssetScanner) (*Asset, error) {
	var a Asset
	var (
		idNull, sourceNull, nameNull, tagsNull, tagsNormNull sql.NullString
		embeddingJSON                                        sql.NullString
		duration                                             sql.NullInt64
		urlNull                                              sql.NullString
		mediaTypeNull, statusNull, localPathNull             sql.NullString
		relativePathNull, driveFileIDNull                    sql.NullString
		driveFolderID                                        sql.NullString
		driveLinkNull, downloadLinkNull                      sql.NullString
		fileHashNull                                         sql.NullString
		metadataStr                                          sql.NullString
		visualEmb, transcriptEmb                             sql.NullString
		createdAtStr, updatedAtStr                           sql.NullString
		widthNull, heightNull                                sql.NullInt64
		lifecycle, deletedAtStr                              sql.NullString
		folderIDNull, parentFolderIDNull, folderPathNull     sql.NullString
		category, filename, errCol, thumbURL, phash          sql.NullString
		searchText, sceneType                                sql.NullString
		qualityScore                                         sql.NullFloat64
		reuseCount                                           sql.NullInt64
		lastUsedAtNull                                       sql.NullString
	)

	// Scan target order MUST match mediaAssetColumns in repository.go.
	err := s.Scan(
		&idNull, &sourceNull, &nameNull, &tagsNull, &tagsNormNull,
		&embeddingJSON, &duration, &urlNull,
		&mediaTypeNull, &statusNull, &localPathNull, &relativePathNull,
		&driveFileIDNull, &driveFolderID, &driveLinkNull, &downloadLinkNull,
		&fileHashNull, &metadataStr, &visualEmb, &transcriptEmb,
		&createdAtStr, &updatedAtStr, &widthNull, &heightNull,
		&lifecycle, &deletedAtStr,
		&folderIDNull, &parentFolderIDNull, &folderPathNull,
		&category, &filename, &errCol, &thumbURL, &phash,
		&searchText, &sceneType, &qualityScore, &reuseCount,
		&lastUsedAtNull,
	)
	if err != nil {
		return nil, err
	}

	// Map onto canonical Asset.
	a.ID = idNull.String
	a.Source = Source(sourceNull.String)
	a.Name = nameNull.String
	if duration.Valid {
		a.Duration = time.Duration(duration.Int64) * time.Millisecond
	}
	a.SourceURL = urlNull.String
	a.MediaType = MediaType(mediaTypeNull.String)

	// Parse metadata_json first so other setters write into it.
	a.SetMetadataJSON(metadataStr.String)

	a.SetLocalPath(localPathNull.String)
	a.SetDriveFileID(driveFileIDNull.String)
	a.SetDriveLink(driveLinkNull.String)
	a.SetDownloadLink(downloadLinkNull.String)
	a.SetFileHash(fileHashNull.String)
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
		a.LifecycleState = LifecycleState(lifecycle.String)
	}

	// Legacy fallback: drive_folder_id → folder_id.
	if a.FolderID() == "" && driveFolderID.Valid && driveFolderID.String != "" {
		a.SetFolderID(driveFolderID.String)
	}

	// Parse tags.
	if tagsNull.Valid && tagsNull.String != "" && tagsNull.String != "[]" {
		_ = json.Unmarshal([]byte(tagsNull.String), &a.Tags)
	}

	// group_name from metadata_json or field.
	if a.Group == "" {
		a.Group = a.GetMetadataString("group_name")
	}

	return &a, nil
}

// scanCanonicalAssetRows scans a canonical asset from sql.Rows.
func scanCanonicalAssetRows(rows *sql.Rows) (*Asset, error) {
	return scanMediaAsset(rows)
}

// scanCanonicalAssetRow scans a single canonical asset from sql.Row.
func (s *AssetStoreSQLite) scanCanonicalAssetRow(row *sql.Row) (*Asset, error) {
	return scanMediaAsset(row)
}

// ScanCanonicalAssetRowsPublic is an exported wrapper for scanCanonicalAssetRows.
func ScanCanonicalAssetRowsPublic(rows *sql.Rows) (*Asset, error) {
	return scanCanonicalAssetRows(rows)
}

// ScanCanonicalAssetRowPublic is an exported wrapper for scanCanonicalAssetRow.
func (s *AssetStoreSQLite) ScanCanonicalAssetRowPublic(row *sql.Row) (*Asset, error) {
	return s.scanCanonicalAssetRow(row)
}
