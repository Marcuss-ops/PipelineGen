// Package backfill — upsert.go: the canonical multi-row upsert the
// SQLite → PostgreSQL backfill engine writes through.
//
// This used to be a method on the core PostgresAssetCommitter
// (media/backfill_committer.go). The write path never used it: the only
// caller is RunMediaBackfill, so it moved here with the engine instead of
// leaving a backfill-only seam on the live committer.
package backfill

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// upsertRows writes rowCount rows of cols into table, keyed on the table's
// natural conflict target: (asset_id, location_kind) for asset_locations and
// id for every other media surface. Columns are full-replaced from EXCLUDED,
// so a re-run converges the target to the source row.
func upsertRows(ctx context.Context, db *sql.DB, table string, cols []string, rowCount int, valueAt func(rowIdx, colIdx int) any) (int, error) {
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
	res, err := db.ExecContext(ctx, sb.String(), params...)
	if err != nil {
		return 0, fmt.Errorf("media backfill: upsert postgres media_assets: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
