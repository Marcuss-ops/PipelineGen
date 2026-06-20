package asset

import (
	"context"
	"fmt"

	sqlutil "github.com/Marcuss-ops/PipelineGen/pkg/sqlutil"
)

// SearchStockByKeywords searches stock clips by keywords using LIKE on the media_assets table.
//
// Lives in its own file so the stock-specific query (which hard-codes
// source='stock' and ignores the Repository.source argument) does not
// clutter the user-facing search.go.
func (s *AssetStoreSQLite) SearchStockByKeywords(ctx context.Context, keywords []string, limit int) ([]*Asset, error) {
	if len(keywords) == 0 {
		return []*Asset{}, nil
	}

	columns := clipSearchColumns()
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*Asset{}, nil
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM media_assets
		WHERE source = 'stock' AND `+SoftDeleteFilter()+` AND (%s)
		LIMIT ?`,
		mediaAssetColumns,
		conditionSQL,
	)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}
