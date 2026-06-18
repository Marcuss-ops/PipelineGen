package clips

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// mediaAssetScanner abstracts away sql.Rows vs sql.Row so callers
// in this file can scan either without duplicating the column-projection
// logic between List*, Get*, and Search paths.
type mediaAssetScanner interface {
	Scan(dest ...any) error
}

// scanMediaAsset scans a media asset from any SQL scanner (sql.Rows or sql.Row).
//
// After migration 059 + this canonicalization round, every typed column on
// media_assets is read directly from the column. The SELECT list
// (mediaAssetColumns in repository.go) covers all 37 typed columns plus
// metadata_json for non-canonical extras only (clipindexer search helpers,
// prompt/mood/subjects, provider raw metadata, transcript-derivable search
// fields, clipindexer state machine).
//
// IMPORTANT: every Scan target is a sql.NullX type so any column absence,
// row-count mismatch, or table-mismatch surfaces as ErrNoRows / a typed
// convertAssign error rather than a silent partial-population. Anywhere
// the typed MediaAsset struct has no matching field (tags_norm,
// relative_path, raw column reads), the target is a local sql.NullX still
// required to keep the count math between SELECT and Scan in lockstep.
//
// The metadata_json fallbacks that previously lifted media_type / status /
// local_path / drive_link / drive_file_id / download_link / file_hash from
// the column-never-was-populated JSON map are GONE: migration 059 strips
// those keys from metadata_json on the canonical column set, and UpsertClip
// writes exclusively to the typed columns. Reading from the typed column
// post-migration is the only path that yields non-empty values.
//
// Two fallbacks REMAIN because they have no canonical column:
//   - drive_folder_id → folder_id piggyback (legacy column pre-030 still
//     populated for backward compat with pre-migration rows)
//   - group_name → clip.Group (source-specific annotation, not a column)
func scanMediaAsset(s mediaAssetScanner) (*models.MediaAsset, error) {
	var clip models.MediaAsset
	var (
		// Core identity / metadata.
		idNull, sourceNull, nameNull, tagsNull, tagsNormNull sql.NullString
		embeddingJSON                                        sql.NullString
		duration                                             sql.NullInt64
		urlNull                                              sql.NullString
		// Per-source media info (added in canonical column extension).
		mediaTypeNull, statusNull, localPathNull sql.NullString
		relativePathNull, driveFileIDNull        sql.NullString
		driveFolderID                            sql.NullString
		driveLinkNull, downloadLinkNull          sql.NullString
		fileHashNull                             sql.NullString
		// Free-form extras (kept for non-canonical JSON helpers).
		metadataStr           sql.NullString
		visualEmb, transcriptEmb sql.NullString
		// Timestamps.
		createdAtStr, updatedAtStr sql.NullString
		// Image dimensions (image rows; zero for non-image rows).
		widthNull, heightNull sql.NullInt64
		// Canonical columns (migration 059).
		lifecycle, deletedAtStr                                  sql.NullString
		folderIDNull, parentFolderIDNull, folderPathNull         sql.NullString
		category, filename, errCol, thumbURL, phash              sql.NullString
		searchText, sceneType                                    sql.NullString
		qualityScore                                             sql.NullFloat64
		reuseCount                                               sql.NullInt64
		lastUsedAtNull                                           sql.NullString
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
		// ErrNoRows, type mismatch, OR a column missing from the SELECT
		// list all surface here. ALWAYS return nil on error — never a
		// partially-populated clip, otherwise callers like GetClip will
		// mistakenly return a non-nil clip after DeleteClip.
		return nil, err
	}

	// Canonical column reads — mapped onto the typed MediaAsset struct.
	clip.ID = idNull.String
	clip.Source = sourceNull.String
	clip.Name = nameNull.String
	clip.TagsNorm = tagsNormNull.String
	if duration.Valid {
		clip.Duration = int(duration.Int64)
	}
	clip.ExternalURL = urlNull.String
	clip.MediaType = mediaTypeNull.String
	clip.Status = statusNull.String
	clip.LocalPath = localPathNull.String
	clip.RelativePath = relativePathNull.String
	clip.DriveFileID = driveFileIDNull.String
	clip.DriveLink = driveLinkNull.String
	clip.DownloadLink = downloadLinkNull.String
	clip.FileHash = fileHashNull.String
	if embeddingJSON.Valid {
		clip.EmbeddingJSON = embeddingJSON.String
	}
	if visualEmb.Valid {
		clip.VisualEmbedding = visualEmb.String
	}
	if transcriptEmb.Valid {
		clip.TranscriptEmbedding = transcriptEmb.String
	}

	// Timestamps: created_at always populated by INSERT; updated_at
	// separately maintained by the upsert path. Prefer updated_at when
	// non-empty (UpsertClip refreshes it on every write); fall back to
	// created_at so legacy callers that never updated updated_at still see
	// a non-zero UpdatedAt.
	if createdAtStr.Valid {
		if t := timeutil.ParseRFC3339(createdAtStr.String); !t.IsZero() {
			clip.CreatedAt = t
			if clip.UpdatedAt.IsZero() {
				clip.UpdatedAt = t
			}
		}
	}
	if updatedAtStr.Valid {
		if t := timeutil.ParseRFC3339(updatedAtStr.String); !t.IsZero() {
			clip.UpdatedAt = t
		}
	}

	// Image dimensions (zero for non-image rows; image_assets.repository
	// also reads these directly via its own scan path).
	if widthNull.Valid {
		clip.Width = int(widthNull.Int64)
	}
	if heightNull.Valid {
		clip.Height = int(heightNull.Int64)
	}

	// 15 canonical columns from migration 059.
	clip.FolderID = folderIDNull.String
	clip.ParentFolderID = parentFolderIDNull.String
	clip.FolderPath = folderPathNull.String
	clip.Category = category.String
	clip.Filename = filename.String
	clip.Error = errCol.String
	clip.ThumbURL = thumbURL.String
	clip.PHash = phash.String
	clip.SearchText = searchText.String
	clip.SceneType = sceneType.String
	if qualityScore.Valid {
		clip.QualityScore = qualityScore.Float64
	}
	if reuseCount.Valid {
		clip.ReuseCount = int(reuseCount.Int64)
	}
	clip.LastUsedAt = lastUsedAtNull.String
	if deletedAtStr.Valid && strings.TrimSpace(deletedAtStr.String) != "" {
		if t := timeutil.ParseRFC3339(deletedAtStr.String); !t.IsZero() {
			clip.DeletedAt = &t
		}
	}
	if lifecycle.Valid {
		clip.LifecycleState = lifecycle.String
	}

	// Legacy fallback: if folder_id column is empty but the legacy
	// drive_folder_id column has data, lift it into the canonical
	// folder_id field. drive_folder_id is pre-migration-030 and a few
	// pre-existing rows still have it; this piggyback keeps the service
	// layer from needing to know about both columns.
	if clip.FolderID == "" && driveFolderID.Valid && driveFolderID.String != "" {
		clip.FolderID = driveFolderID.String
	}

	// Parse tags (the canonical column).
	if tagsNull.Valid && tagsNull.String != "" && tagsNull.String != "[]" {
		_ = json.Unmarshal([]byte(tagsNull.String), &clip.Tags)
	}

	// Parse metadata_json for non-canonical extras only (search helpers,
	// prompt/mood/subjects, provider raw metadata, clipindexer state).
	clip.SetMetadataJSON(metadataStr.String)

	// group_name (metadata_json-only, source-specific — no canonical
	// column) is the last remaining non-canonical field that flows into
	// the typed struct.
	if clip.Group == "" {
		clip.Group = clip.GetMetadataString("group_name")
	}

	return &clip, nil
}

// scanMediaAssetRows scans a media asset from sql.Rows.
func scanMediaAssetRows(rows *sql.Rows) (*models.MediaAsset, error) {
	return scanMediaAsset(rows)
}

// scanMediaAssetRow scans a single media asset from sql.Row.
func (r *Repository) scanMediaAssetRow(row *sql.Row) (*models.MediaAsset, error) {
	return scanMediaAsset(row)
}
