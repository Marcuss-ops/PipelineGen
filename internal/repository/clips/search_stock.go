package clips

import (
	"context"
	"fmt"

	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/sqlutil"
)

// SearchStockByKeywords searches stock clips by keywords using LIKE on the media_assets table.
//
// Lives in its own file so the stock-specific query (which hard-codes
// source='stock' and ignores the Repository.source argument) does not
// clutter the user-facing search.go.
func (r *Repository) SearchStockByKeywords(ctx context.Context, keywords []string, limit int) ([]*models.MediaAsset, error) {
	if len(keywords) == 0 {
		return []*models.MediaAsset{}, nil
	}

	columns := clipSearchColumns()
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
