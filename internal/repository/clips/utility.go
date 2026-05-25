package clips

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
)

func (r *Repository) GetFolderChildren(ctx context.Context, parentID string) ([]*models.MediaAsset, error) {
	query := `SELECT ` + mediaAssetColumns + `
		FROM media_assets
		WHERE json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL AND json_extract(COALESCE(metadata_json,'{}'), '$.parent_folder_id') = ?
		ORDER BY name ASC`

	rows, err := r.db.QueryContext(ctx, query, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*models.MediaAsset
	for rows.Next() {
		clip, err := scanMediaAssetRows(rows)
		if err != nil {
			r.log.Error("failed to scan clip", zap.Error(err))
			continue
		}
		clips = append(clips, clip)
	}

	return clips, rows.Err()
}

// FindByPHash searches for a clip with the given perceptual hash (stored in metadata_json).
// Returns the clip ID if found, empty string if not.
func (r *Repository) FindByPHash(ctx context.Context, phash string) (string, error) {
	if phash == "" {
		return "", nil
	}
	var id string
	query := `SELECT id FROM media_assets WHERE json_extract(COALESCE(metadata_json,'{}'), '$.phash') = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.deleted_at') IS NULL LIMIT 1`
	err := r.db.QueryRowContext(ctx, query, phash).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindByPHash: %w", err)
	}
	return id, nil
}

// MarkUsed marks a clip as used, incrementing reuse_count and setting last_used_at.
func (r *Repository) MarkUsed(ctx context.Context, clipID string) error {
	if clipID == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET metadata_json = json_set(
			COALESCE(metadata_json, '{}'),
			'$.reuse_count',
			COALESCE(CAST(json_extract(metadata_json, '$.reuse_count') AS INTEGER), 0) + 1,
			'$.last_used_at',
			?
		)
		WHERE id = ?
	`, now, clipID)
	return err
}

// MarkClipsUsed marks multiple clips as used in a single operation.
func (r *Repository) MarkClipsUsed(ctx context.Context, clipIDs []string) error {
	for _, id := range clipIDs {
		if err := r.MarkUsed(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
