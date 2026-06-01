package clips

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/sqlutil"
)

func (r *Repository) ListClips(ctx context.Context, source string) ([]*models.MediaAsset, error) {
	query := buildMediaAssetQuery(source)
	args := []interface{}{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// ListClipsPaged returns clips with pagination and optional search.
// If q is non-empty, performs a search; otherwise lists all clips with pagination.
func (r *Repository) ListClipsPaged(ctx context.Context, source string, limit, offset int, q string) ([]*models.MediaAsset, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}

	if strings.TrimSpace(q) != "" {
		return r.SearchClips(ctx, source, q)
	}

	query := buildMediaAssetQuery(source) + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args := []interface{}{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClips searches clips by tag or name using LIKE on the media_assets table.
func (r *Repository) SearchClips(ctx context.Context, source, tag string) ([]*models.MediaAsset, error) {
	columns := []string{"tags", "name"}
	keywords := strings.Fields(tag)
	if len(keywords) == 0 {
		keywords = []string{tag}
	}

	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.MediaAsset{}, nil
	}

	query := buildMediaAssetQuery(source) + " AND (" + conditionSQL + ")"
	finalArgs := []interface{}{}
	if source != "" && source != "all" && source != "unified" {
		finalArgs = append(finalArgs, source)
	}
	finalArgs = append(finalArgs, args...)

	rows, err := r.db.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClipsByKeywords searches clips by keywords using LIKE on the media_assets table.
func (r *Repository) SearchClipsByKeywords(ctx context.Context, source string, keywords []string, limit int) ([]*models.MediaAsset, error) {
	if len(keywords) == 0 {
		return []*models.MediaAsset{}, nil
	}

	columns := []string{"name", "tags"}
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.MediaAsset{}, nil
	}

	query := fmt.Sprintf("%s AND (%s) LIMIT ?", buildMediaAssetQuery(source), conditionSQL)
	finalArgs := []interface{}{}
	if source != "" && source != "all" && source != "unified" {
		finalArgs = append(finalArgs, source)
	}
	finalArgs = append(finalArgs, args...)
	finalArgs = append(finalArgs, limit)

	rows, err := r.db.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchStockByKeywords searches stock clips by keywords using LIKE on the media_assets table.
func (r *Repository) SearchStockByKeywords(ctx context.Context, keywords []string, limit int) ([]*models.MediaAsset, error) {
	if len(keywords) == 0 {
		return []*models.MediaAsset{}, nil
	}

	columns := []string{"name", "tags"}
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*models.MediaAsset{}, nil
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM media_assets
		WHERE source = 'stock' AND json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL AND (%s)
		LIMIT ?`,
		mediaAssetColumns,
		conditionSQL,
	)
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

type mediaAssetScanner interface {
	Scan(dest ...interface{}) error
}

// scanMediaAsset scans a media asset from any SQL scanner (sql.Rows or sql.Row).
func scanMediaAsset(s mediaAssetScanner) (*models.MediaAsset, error) {
	var clip models.MediaAsset
	var tagsNull, metadataStr sql.NullString
	var sourceNull, nameNull, urlNull sql.NullString
	var createdAtStr sql.NullString
	var duration sql.NullInt64
	var embeddingJSON sql.NullString

	err := s.Scan(
		&clip.ID, &sourceNull, &nameNull, &tagsNull, &embeddingJSON, &duration, &urlNull, &createdAtStr, &metadataStr,
	)
	if err != nil {
		return nil, err
	}

	clip.Source = sourceNull.String
	clip.Name = nameNull.String
	clip.ExternalURL = urlNull.String

	if embeddingJSON.Valid {
		clip.EmbeddingJSON = embeddingJSON.String
	}

	if duration.Valid {
		clip.Duration = int(duration.Int64)
	}

	if createdAtStr.Valid {
		if t, err := time.Parse(time.RFC3339, createdAtStr.String); err == nil {
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

// CountClips returns the total number of clips (excluding soft-deleted).
func (r *Repository) CountClips(ctx context.Context) (int, error) {
	query := "SELECT COUNT(*) FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL"
	row := r.db.QueryRowContext(ctx, query)
	var count int
	err := row.Scan(&count)
	return count, err
}

// LastUpdatedAtForTerm returns the most recent created_at value for clips matching a term.
// Uses LIKE search on tags.
func (r *Repository) LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error) {
	term = strings.TrimSpace(term)

	var lastUpdated sql.NullString
	query := `
		SELECT MAX(created_at)
		FROM media_assets
		WHERE source = 'artlist' AND tags LIKE ?
	`
	row := r.db.QueryRowContext(ctx, query, "%"+term+"%")

	if err := row.Scan(&lastUpdated); err != nil {
		return nil, err
	}
	if !lastUpdated.Valid || strings.TrimSpace(lastUpdated.String) == "" {
		return nil, nil
	}
	return &lastUpdated.String, nil
}
