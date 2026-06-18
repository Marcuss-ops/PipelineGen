package clips

import (
	"database/sql"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

type mediaAssetScanner interface {
	Scan(dest ...any) error
}

// scanMediaAsset scans a media asset from any SQL scanner (sql.Rows or sql.Row).
func scanMediaAsset(s mediaAssetScanner) (*models.MediaAsset, error) {
	var clip models.MediaAsset
	var tagsNull, metadataStr sql.NullString
	var sourceNull, nameNull, urlNull, driveFolderID sql.NullString
	var createdAtStr sql.NullString
	var duration sql.NullInt64
	var embeddingJSON, visualEmb, transcriptEmb sql.NullString

	err := s.Scan(
		&clip.ID, &sourceNull, &nameNull, &tagsNull, &embeddingJSON, &duration, &urlNull, &createdAtStr, &metadataStr, &driveFolderID, &visualEmb, &transcriptEmb,
	)
	if driveFolderID.Valid && driveFolderID.String != "" && clip.FolderID == "" {
		clip.FolderID = driveFolderID.String
	}
	if err != nil {
		return nil, err
	}

	clip.Source = sourceNull.String
	clip.Name = nameNull.String
	clip.ExternalURL = urlNull.String

	if embeddingJSON.Valid {
		clip.EmbeddingJSON = embeddingJSON.String
	}

	if visualEmb.Valid {
		clip.VisualEmbedding = visualEmb.String
	}

	if transcriptEmb.Valid {
		clip.TranscriptEmbedding = transcriptEmb.String
	}

	if duration.Valid {
		clip.Duration = int(duration.Int64)
	}

	if createdAtStr.Valid {
		if t := timeutil.ParseRFC3339(createdAtStr.String); !t.IsZero() {
			clip.CreatedAt = t
			clip.UpdatedAt = t
		}
	}

	// Parse tags
	if tagsNull.Valid && tagsNull.String != "" && tagsNull.String != "[]" {
		_ = json.Unmarshal([]byte(tagsNull.String), &clip.Tags)
	}

	// Parse metadata_json into Metadata map
	clip.SetMetadataJSON(metadataStr.String)

	// Extract common fields from Metadata map for convenience
	clip.FolderID = clip.GetMetadataString("folder_id")
	clip.ParentFolderID = clip.GetMetadataString("parent_folder_id")
	clip.FolderPath = clip.GetMetadataString("folder_path")
	clip.Group = clip.GetMetadataString("group_name")
	clip.MediaType = clip.GetMetadataString("media_type")
	clip.DriveLink = clip.GetMetadataString("drive_link")
	clip.DriveFileID = clip.GetMetadataString("drive_file_id")
	clip.DownloadLink = clip.GetMetadataString("download_link")
	clip.Category = clip.GetMetadataString("category")
	clip.FileHash = clip.GetMetadataString("file_hash")
	clip.LocalPath = clip.GetMetadataString("local_path")
	clip.Status = clip.GetMetadataString("status")
	clip.Error = clip.GetMetadataString("error")
	clip.ThumbURL = clip.GetMetadataString("thumb_url")
	clip.PHash = clip.GetMetadataString("phash")
	clip.Filename = clip.GetMetadataString("filename")
	clip.VisualEmbeddingJSON = clip.GetMetadataString("visual_embedding_json")
	clip.SearchText = clip.GetMetadataString("search_text")
	clip.SceneType = clip.GetMetadataString("scene_type")

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
