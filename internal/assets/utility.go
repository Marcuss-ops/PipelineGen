package assets

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
	
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

func (s *AssetStoreSQLite) GetFolderChildren(ctx context.Context, parentID string) ([]*Asset, error) {
	query := `SELECT ` + mediaAssetColumns + `
		FROM media_assets
		WHERE ` + SoftDeleteFilter() + ` AND parent_folder_id = ?
		ORDER BY name ASC`

	rows, err := s.db.QueryContext(ctx, query, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			s.log.Error("failed to scan clip", zap.Error(err))
			continue
		}
		clips = append(clips, clip)
	}

	return clips, rows.Err()
}

// FindByPHash searches for a clip with the given perceptual hash (canonical column after migration 059).
// Returns the clip ID if found, empty string if not.
func (s *AssetStoreSQLite) FindByPHash(ctx context.Context, phash string) (string, error) {
	if phash == "" {
		return "", nil
	}
	var id string
	query := `SELECT id FROM media_assets WHERE phash = ? AND ` + SoftDeleteFilter() + ` LIMIT 1`
	err := s.db.QueryRowContext(ctx, query, phash).Scan(&id)
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
func (s *AssetStoreSQLite) MarkUsed(ctx context.Context, clipID string) error {
	if clipID == "" {
		return nil
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx, `
		UPDATE media_assets
		SET reuse_count = reuse_count + 1,
		    last_used_at = ?
		WHERE id = ?
	`, now, clipID)
	return err
}

// MarkClipsUsed marks multiple clips as used in a single operation.
func (s *AssetStoreSQLite) MarkClipsUsed(ctx context.Context, clipIDs []string) error {
	for _, id := range clipIDs {
		if err := s.MarkUsed(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

