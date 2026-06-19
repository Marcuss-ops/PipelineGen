package clips

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

func (r *Repository) GetFolderChildren(ctx context.Context, parentID string) ([]*assets.Asset, error) {
	query := `SELECT ` + mediaAssetColumns + `
		FROM media_assets
		WHERE ` + r.SoftDeleteFilter() + ` AND parent_folder_id = ?
		ORDER BY name ASC`

	rows, err := r.db.QueryContext(ctx, query, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*assets.Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			r.log.Error("failed to scan clip", zap.Error(err))
			continue
		}
		clips = append(clips, clip)
	}

	return clips, rows.Err()
}

// FindByPHash searches for a clip with the given perceptual hash (canonical column after migration 059).
// Returns the clip ID if found, empty string if not.
func (r *Repository) FindByPHash(ctx context.Context, phash string) (string, error) {
	if phash == "" {
		return "", nil
	}
	var id string
	query := `SELECT id FROM media_assets WHERE phash = ? AND ` + r.SoftDeleteFilter() + ` LIMIT 1`
	err := r.db.QueryRowContext(ctx, query, phash).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindByPHash: %w", err)
	}
	return id, nil
}

// MarkUsed marks a clip as used, incrementing reuse_count and setting last_used_at
// on the canonical columns (migration 059).
func (r *Repository) MarkUsed(ctx context.Context, clipID string) error {
	if clipID == "" {
		return nil
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET reuse_count = reuse_count + 1,
		    last_used_at = ?
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
