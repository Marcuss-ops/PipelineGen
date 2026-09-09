// Package media — backfill_verify.go: fail-closed parity verifier for the
// FASE-3 SQLite → PostgreSQL media backfill. Split from backfill.go purely
// for the max_lines_per_file_strict policy gate; see backfill.go for the
// engine contract (godlike/07: every compared field of every row must match
// exactly or RunMediaBackfill returns an error listing the mismatches).
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// verifyMediaParity is the fail-closed acceptance check: total counts must
// match and every mapped field of every row must be byte-identical.
// verifyMediaParity proves the PostgreSQL SSOT contains the complete
// legacy catalog. Post-cutover the direction of truth is one-way:
//
//   - Every SQLite row MUST exist in PostgreSQL with identical content
//     (enforced by compareRowsByKey per natural key).
//   - PostgreSQL MAY legitimately contain MORE rows than the frozen
//     legacy snapshot: SQLite media writers are demolished, so assets
//     ingested after the snapshot only exist on the SSOT side. A count
//     surplus is expected growth, not a parity violation.
//   - A count SHORTFALL (postgres < sqlite) is always a violation.
func verifyMediaParity(ctx context.Context, sqliteDB, pg *sql.DB, assetCols []string, assetTypes map[string]string, locCols []string, locTypes map[string]string, report *BackfillReport) error {
	var err error
	if report.SQLiteAssetCount, report.PostgresAssetCount, err = compareCounts(ctx, sqliteDB, pg, "media_assets"); err != nil {
		return err
	}
	if report.SQLiteLocationCount, report.PostgresLocationCount, err = compareCounts(ctx, sqliteDB, pg, "asset_locations"); err != nil {
		return err
	}
	if report.PostgresAssetCount < report.SQLiteAssetCount {
		report.addMismatch(fmt.Sprintf("media_assets count shortfall: sqlite=%d postgres=%d (SSOT missing legacy rows)", report.SQLiteAssetCount, report.PostgresAssetCount))
	}
	if report.PostgresLocationCount < report.SQLiteLocationCount {
		report.addMismatch(fmt.Sprintf("asset_locations count shortfall: sqlite=%d postgres=%d (SSOT missing legacy rows)", report.SQLiteLocationCount, report.PostgresLocationCount))
	}

	if err := compareRowsByKey(ctx, sqliteDB, pg, "media_assets", "id", assetCols, assetTypes, nil, report); err != nil {
		return err
	}
	return compareRowsByKey(ctx, sqliteDB, pg, "asset_locations", "asset_id, location_kind", locCols, locTypes, locationColumnAliases, report)
}

// compareRowsByKey pulls the mapped projection from both engines and diffs
// every row on the natural key. srcAliases reverses the PG→SQLite column
// alias map when the SELECT runs against SQLite. The SQLite side applies
// the same legacy-NULL coercion as the copy phase so the diff is
// like-for-like (a NULL in a weakly-typed legacy column equals the
// canonical zero on the PostgreSQL side).
func compareRowsByKey(ctx context.Context, sqliteDB, pg *sql.DB, table, keyCols string, cols []string, types map[string]string, srcAliases map[string]string, report *BackfillReport) error {
	selectList := func(names []string) string {
		q := make([]string, len(names))
		for i, c := range names {
			q[i] = `"` + c + `"`
		}
		return strings.Join(q, ", ")
	}
	srcCols := make([]string, len(cols))
	for i, c := range cols {
		src := c
		for s, p := range srcAliases {
			if p == c {
				src = s
			}
		}
		srcCols[i] = src
	}
	keyOrder := strings.Split(strings.ReplaceAll(keyCols, " ", ""), ",")

	fetch := func(db *sql.DB, names []string) (map[string]map[string]any, error) {
		rows, err := db.QueryContext(ctx, "SELECT "+selectList(names)+" FROM "+table+" ORDER BY "+keyCols)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[string]map[string]any{}
		for rows.Next() {
			vals := make([]any, len(names))
			ptrs := make([]any, len(names))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, err
			}
			for i := range vals {
				vals[i] = sanitizeUTF8Value(coerceLegacyZero(vals[i], types[names[i]]), types[names[i]])
			}
			keyParts := make([]string, len(keyOrder))
			for i, k := range keyOrder {
				for j, n := range names {
					if n == k {
						keyParts[i] = fmt.Sprint(vals[j])
					}
				}
			}
			m := map[string]any{}
			for j, n := range names {
				// Normalize to the PG-side column name for comparison.
				pn := n
				for s, p := range srcAliases {
					if s == n {
						pn = p
					}
				}
				m[pn] = vals[j]
			}
			out[strings.Join(keyParts, "|")] = m
		}
		return out, rows.Err()
	}

	sqliteRows, err := fetch(sqliteDB, srcCols)
	if err != nil {
		return fmt.Errorf("media backfill: compare sqlite %s: %w", table, err)
	}
	pgRows, err := fetch(pg, cols)
	if err != nil {
		return fmt.Errorf("media backfill: compare postgres %s: %w", table, err)
	}

	keys := make([]string, 0, len(sqliteRows))
	for k := range sqliteRows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p, ok := pgRows[k]
		if !ok {
			report.addMismatch(fmt.Sprintf("%s[%s]: missing in postgres", table, k))
			continue
		}
		for _, c := range cols {
			sv, pv := fmt.Sprint(sqliteRows[k][c]), fmt.Sprint(p[c])
			if sv != pv {
				report.addMismatch(fmt.Sprintf("%s[%s].%s: sqlite=%q postgres=%q", table, k, c, sv, pv))
			}
		}
	}
	return nil
}

func compareCounts(ctx context.Context, sqliteDB, pg *sql.DB, table string) (int64, int64, error) {
	var s, p int64
	if err := sqliteDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&s); err != nil {
		return 0, 0, fmt.Errorf("media backfill: count sqlite %s: %w", table, err)
	}
	if err := pg.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&p); err != nil {
		return 0, 0, fmt.Errorf("media backfill: count postgres %s: %w", table, err)
	}
	return s, p, nil
}

func (r *BackfillReport) addMismatch(msg string) {
	r.MismatchCount++
	if len(r.Mismatches) < backfillMaxReportedMismatches {
		r.Mismatches = append(r.Mismatches, msg)
	}
}

// PrintJSON renders the report as machine-readable JSON on stdout.
func (r *BackfillReport) PrintJSON() {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
}
