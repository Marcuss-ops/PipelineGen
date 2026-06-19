package assetrepo

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// scanner abstracts sql.Row and sql.Rows for scanning.
type scanner interface {
	Scan(dest ...any) error
}

// scanAsset scans a canonical Asset from any SQL scanner.
func scanAsset(s scanner) (*asset.Asset, error) {
	var a asset.Asset
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

	a.ID = idNull.String
	a.Source = asset.Source(sourceNull.String)
	a.Name = nameNull.String
	if duration.Valid {
		a.Duration = time.Duration(duration.Int64) * time.Millisecond
	}
	a.SourceURL = urlNull.String
	a.MediaType = asset.MediaType(mediaTypeNull.String)

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

	if a.FolderID() == "" && driveFolderID.Valid && driveFolderID.String != "" {
		a.SetFolderID(driveFolderID.String)
	}

	if tagsNull.Valid && tagsNull.String != "" && tagsNull.String != "[]" {
		_ = json.Unmarshal([]byte(tagsNull.String), &a.Tags)
	}

	if a.Group == "" {
		a.Group = a.GetMetadataString("group_name")
	}

	return &a, nil
}
