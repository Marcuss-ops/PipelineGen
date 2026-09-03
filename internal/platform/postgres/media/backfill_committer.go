package media

import (
	"context"
	"fmt"
	"strings"
)

// UpsertBackfillAssets is the canonical adapter entry point for the legacy
// SQLite projection. Keeping this compatibility write here ensures backfill
// uses the same owner as normal media asset commits.
func (c *PostgresAssetCommitter) UpsertBackfillRows(ctx context.Context, table string, cols []string, rowCount int, valueAt func(rowIdx, colIdx int) any) (int, error) {
	if rowCount == 0 {
		return 0, nil
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES ")
	params := make([]any, 0, rowCount*len(cols))
	for r := 0; r < rowCount; r++ {
		if r > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		for j := range cols {
			if j > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "$%d", len(params)+1)
			params = append(params, valueAt(r, j))
		}
		sb.WriteString(")")
	}
	sb.WriteString(" ON CONFLICT (")
	if table == "asset_locations" {
		sb.WriteString("asset_id, location_kind")
	} else {
		sb.WriteString("id")
	}
	sb.WriteString(") DO UPDATE SET ")
	for i, column := range cols {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s = EXCLUDED.%s", column, column)
	}
	res, err := c.db.ExecContext(ctx, sb.String(), params...)
	if err != nil {
		return 0, fmt.Errorf("media backfill: upsert postgres media_assets: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
