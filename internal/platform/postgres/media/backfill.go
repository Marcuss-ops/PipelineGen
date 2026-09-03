// Package media — backfill.go: FASE-3 SQLite → PostgreSQL media backfill.
//
// Copies the canonical media surfaces (media_assets, asset_locations) from
// the legacy SQLite media database into the PostgreSQL media SSOT and then
// FAILS CLOSED on any parity violation (godlike/07): row counts and every
// compared field of every row must match exactly, or RunMediaBackfill
// returns an error listing the mismatches.
//
// The column mapping is SCHEMA-DRIVEN, never hand-maintained: at run time
// the engine enumerates the live columns of both engines (SQLite
// PRAGMA table_info / PostgreSQL information_schema) and copies their
// INTERSECTION, in PostgreSQL column order. Columns that exist only on one
// side (legacy leftovers such as SQLite `error`, PG-only canonical columns)
// are excluded automatically, so migration drift on either engine can never
// silently corrupt the copy. The single explicit alias is
// asset_locations.file_hash → legacy_file_md5 (the legacy md5-tier hash
// keeps its priority position on both engines).
//
// Embeddings and derived features are deliberately NOT generated here —
// they belong to the FASE-4 enrichment pipeline so a slow row never blocks
// the backfill.
//
// Idempotence: every write is a full-column upsert keyed on the natural
// key, so re-running the backfill after new SQLite writes converges both
// sides to the same observable state (the parity verifier proves it per
// run, and a --verify-only mode re-checks without copying).
//
// The engine never writes to SQLite and never touches Qdrant.
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // register the "pgx" database/sql driver
	_ "github.com/mattn/go-sqlite3"    // register the "sqlite3" database/sql driver

	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// BackfillConfig configures one SQLite → PostgreSQL media backfill run.
type BackfillConfig struct {
	// SQLiteDSN is the database/sql DSN of the legacy media database
	// (driver "sqlite3"), e.g. /path/to/media.db.sqlite?_journal_mode=WAL.
	SQLiteDSN string
	// PostgresDSN is the database/sql DSN of the PostgreSQL media SSOT
	// (driver "pgx" via pgx/v5/stdlib).
	PostgresDSN string
	// BatchSize is the keyset-pagination page size. Zero means 500.
	BatchSize int
	// Limit caps the number of media_assets rows copied (0 = all).
	Limit int
	// VerifyOnly skips the copy phase and only runs the parity verifier.
	VerifyOnly bool
}

// BackfillReport is the machine-readable outcome of one run.
type BackfillReport struct {
	AssetsCopied          int      `json:"assets_copied"`
	LocationsCopied       int      `json:"locations_copied"`
	AssetsScanned         int      `json:"assets_scanned"`
	LocationsScanned      int      `json:"locations_scanned"`
	LocationsSkipped      int      `json:"locations_skipped_orphan_fk"`
	SQLiteAssetCount      int64    `json:"sqlite_asset_count"`
	PostgresAssetCount    int64    `json:"postgres_asset_count"`
	SQLiteLocationCount   int64    `json:"sqlite_location_count"`
	PostgresLocationCount int64    `json:"postgres_location_count"`
	AssetColumnsMapped    int      `json:"asset_columns_mapped"`
	LocationColumnsMapped int      `json:"location_columns_mapped"`
	MismatchCount         int      `json:"mismatch_count"`
	Mismatches            []string `json:"mismatches,omitempty"`
	VerifyOnly            bool     `json:"verify_only"`
}

const (
	backfillMaxReportedMismatches = 100
	backfillDefaultBatchSize      = 500
)

// locationColumnAliases maps SQLite asset_locations columns onto their
// PostgreSQL names. This is the ONLY hand-written mapping in the engine;
// everything else is derived from the live schemas.
var locationColumnAliases = map[string]string{
	"file_hash": "legacy_file_md5",
}

// RunMediaBackfill executes the FASE-3 backfill and the fail-closed parity
// verification. It applies the canonical PostgreSQL media migrations first
// (idempotent IF NOT EXISTS DDL) so a fresh media database is
// self-bootstrapping.
func RunMediaBackfill(ctx context.Context, cfg BackfillConfig) (*BackfillReport, error) {
	if strings.TrimSpace(cfg.SQLiteDSN) == "" {
		return nil, fmt.Errorf("media backfill: SQLiteDSN is required")
	}
	if strings.TrimSpace(cfg.PostgresDSN) == "" {
		return nil, fmt.Errorf("media backfill: PostgresDSN is required")
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = backfillDefaultBatchSize
	}

	sqliteDB, err := sql.Open("sqlite3", cfg.SQLiteDSN)
	if err != nil {
		return nil, fmt.Errorf("media backfill: open sqlite: %w", err)
	}
	defer sqliteDB.Close()
	if err := sqliteDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("media backfill: ping sqlite: %w", err)
	}

	pg, err := sql.Open("pgx", cfg.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("media backfill: open postgres: %w", err)
	}
	defer pg.Close()
	if err := pg.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("media backfill: ping postgres: %w", err)
	}

	// Self-bootstrapping schema: idempotent IF NOT EXISTS DDL is a no-op on
	// an already-populated media database.
	for _, ddl := range []string{pgmigration.MediaSchemaDDL, pgmigration.MediaVectorSurfacesDDL} {
		if _, err := pg.ExecContext(ctx, ddl); err != nil {
			return nil, fmt.Errorf("media backfill: apply media migrations: %w", err)
		}
	}

	// Schema-driven mapping: intersect the live column inventories and
	// capture the PostgreSQL types so legacy NULLs can be coerced to the
	// canonical zero values (SQLite is weakly typed and carries NULLs in
	// columns the canonical schema declares NOT NULL DEFAULT ...).
	assetCols, assetTypes, err := intersectColumns(ctx, sqliteDB, pg, "media_assets", nil)
	if err != nil {
		return nil, err
	}
	locCols, locTypes, err := intersectColumns(ctx, sqliteDB, pg, "asset_locations", locationColumnAliases)
	if err != nil {
		return nil, err
	}
	if len(assetCols) == 0 || len(locCols) == 0 {
		return nil, fmt.Errorf("media backfill: schema intersection produced no mappable columns")
	}

	report := &BackfillReport{
		VerifyOnly:            cfg.VerifyOnly,
		AssetColumnsMapped:    len(assetCols),
		LocationColumnsMapped: len(locCols),
	}
	committer := NewPostgresAssetCommitter(pg, NewOutboxRepository(pg), nil)
	if !cfg.VerifyOnly {
		if report.AssetsCopied, report.AssetsScanned, err = backfillAssets(ctx, sqliteDB, committer, "media_assets", assetCols, assetTypes, batch, cfg.Limit); err != nil {
			return nil, err
		}
		if report.LocationsCopied, report.LocationsScanned, report.LocationsSkipped, err = backfillLocations(ctx, sqliteDB, pg, "asset_locations", locCols, locTypes); err != nil {
			return nil, err
		}
	}

	if err := verifyMediaParity(ctx, sqliteDB, pg, assetCols, assetTypes, locCols, locTypes, report); err != nil {
		return report, err
	}
	if report.MismatchCount > 0 {
		return report, fmt.Errorf("media backfill: parity verification FAILED with %d mismatch(es)", report.MismatchCount)
	}
	return report, nil
}

// sqliteColumns enumerates the live columns of a SQLite table.
func sqliteColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, fmt.Errorf("media backfill: pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("media backfill: scan pragma table_info(%s): %w", table, err)
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// postgresColumns enumerates the live columns of a PostgreSQL table in
// ordinal order together with their data types (information_schema
// data_type, e.g. text / bigint / real). The types drive the legacy-NULL
// zero coercion shared by the copy and the parity verifier.
func postgresColumns(ctx context.Context, db *sql.DB, table string) ([]string, map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, nil, fmt.Errorf("media backfill: information_schema(%s): %w", table, err)
	}
	defer rows.Close()
	var cols []string
	types := map[string]string{}
	for rows.Next() {
		var name, dtype string
		if err := rows.Scan(&name, &dtype); err != nil {
			return nil, nil, fmt.Errorf("media backfill: scan information_schema(%s): %w", table, err)
		}
		cols = append(cols, name)
		types[name] = dtype
	}
	return cols, types, rows.Err()
}

// intersectColumns computes the mappable projection: every PostgreSQL
// column that has a SQLite source (after applying the alias map), in
// PostgreSQL order. Returns the projection and the PostgreSQL types of the
// projected columns.
func intersectColumns(ctx context.Context, sqliteDB, pg *sql.DB, table string, aliases map[string]string) ([]string, map[string]string, error) {
	sc, err := sqliteColumns(ctx, sqliteDB, table)
	if err != nil {
		return nil, nil, err
	}
	pc, ptypes, err := postgresColumns(ctx, pg, table)
	if err != nil {
		return nil, nil, err
	}
	sqliteSet := make(map[string]bool, len(sc))
	for _, c := range sc {
		sqliteSet[c] = true
	}
	var out []string
	types := map[string]string{}
	for _, pc1 := range pc {
		matched := false
		for sc1, pName := range aliases {
			if pName == pc1 && sqliteSet[sc1] {
				out = append(out, pc1)
				types[pc1] = ptypes[pc1]
				matched = true
				break
			}
		}
		if !matched && sqliteSet[pc1] {
			out = append(out, pc1)
			types[pc1] = ptypes[pc1]
		}
	}
	return out, types, nil
}

// coerceLegacyZero maps a legacy SQLite NULL onto the canonical zero value
// of the PostgreSQL column type, so NOT NULL DEFAULT ... columns accept the
// row and the parity verifier compares like-for-like. Non-NULL values pass
// through untouched.
func coerceLegacyZero(v any, pgType string) any {
	if v != nil {
		return v
	}
	switch pgType {
	case "bigint", "integer", "smallint":
		return int64(0)
	case "real", "double precision", "numeric", "decimal":
		return float64(0)
	case "boolean":
		return false
	default:
		// text, character varying, json(b), and any unmapped type.
		return ""
	}
}

// backfillAssetRow is one streamed SQLite media_assets row.
type backfillAssetRow struct {
	id   string
	vals []any
}

// backfillAssets streams SQLite media_assets in id keyset order and upserts
// the mapped projection into PostgreSQL. Returns (copied, scanned).
func backfillAssets(ctx context.Context, sqliteDB *sql.DB, committer *PostgresAssetCommitter, table string, cols []string, types map[string]string, batch, limit int) (int, int, error) {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = `"` + c + `"`
	}
	query := "SELECT id, " + strings.Join(quoted, ", ") + " FROM " + table + " WHERE id > ? ORDER BY id LIMIT ?"
	copied, scanned := 0, 0
	lastID := ""
	for {
		if limit > 0 && scanned >= limit {
			break
		}
		pageSize := batch
		if limit > 0 && scanned+pageSize > limit {
			pageSize = limit - scanned
		}
		rows, err := sqliteDB.QueryContext(ctx, query, lastID, pageSize)
		if err != nil {
			return copied, scanned, fmt.Errorf("media backfill: select sqlite %s: %w", table, err)
		}
		var batchRows []backfillAssetRow
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			var id string
			if err := rows.Scan(append([]any{&id}, ptrs...)...); err != nil {
				rows.Close()
				return copied, scanned, fmt.Errorf("media backfill: scan sqlite %s: %w", table, err)
			}
			// Legacy NULLs become the canonical zero values of the
			// PostgreSQL column types (cols[i] is a PG column name).
			for i := range vals {
				vals[i] = coerceLegacyZero(vals[i], types[cols[i]])
			}
			batchRows = append(batchRows, backfillAssetRow{id: id, vals: vals})
			scanned++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return copied, scanned, fmt.Errorf("media backfill: iterate sqlite %s: %w", table, err)
		}
		rows.Close()
		if len(batchRows) == 0 {
			break
		}
		lastID = batchRows[len(batchRows)-1].id

		inserted, err := committer.UpsertBackfillRows(ctx, table, cols, len(batchRows), func(rowIdx, colIdx int) any {
			return batchRows[rowIdx].vals[colIdx]
		})
		if err != nil {
			return copied, scanned, err
		}
		copied += inserted
		if len(batchRows) < pageSize {
			break
		}
	}
	return copied, scanned, nil
}

// backfillLocations streams SQLite asset_locations and upserts the mapped
// projection. Orphan rows whose asset did not surface in media_assets are
// counted, never fatal. Returns (copied, scanned, skippedOrphanFK).
func backfillLocations(ctx context.Context, sqliteDB, pg *sql.DB, table string, cols []string, types map[string]string) (int, int, int, error) {
	// cols are PG names; map back to SQLite source names for the SELECT.
	srcNames := make([]string, len(cols))
	for i, c := range cols {
		src := c
		for s, p := range locationColumnAliases {
			if p == c {
				src = s
			}
		}
		srcNames[i] = src
	}
	quoted := make([]string, len(srcNames))
	for i, c := range srcNames {
		quoted[i] = `"` + c + `"`
	}
	query := "SELECT " + strings.Join(quoted, ", ") + " FROM " + table + " ORDER BY asset_id, location_kind"
	rows, err := sqliteDB.QueryContext(ctx, query)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("media backfill: select sqlite %s: %w", table, err)
	}
	defer rows.Close()

	copied, scanned, skipped := 0, 0, 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return copied, scanned, skipped, fmt.Errorf("media backfill: scan sqlite %s: %w", table, err)
		}
		scanned++
		committer := NewPostgresAssetCommitter(pg, NewOutboxRepository(pg), nil)
		inserted, err := committer.UpsertBackfillRows(ctx, table, cols, 1, func(_, colIdx int) any {
			return vals[colIdx]
		})
		if err != nil {
			if strings.Contains(err.Error(), "violates foreign key constraint") {
				// Orphan location (asset row absent): report, never crash —
				// the parity verifier compares per-known-asset only.
				skipped++
				continue
			}
			return copied, scanned, skipped, err
		}
		copied += inserted
	}
	if err := rows.Err(); err != nil {
		return copied, scanned, skipped, fmt.Errorf("media backfill: iterate sqlite %s: %w", table, err)
	}
	return copied, scanned, skipped, nil
}

// verifyMediaParity is the fail-closed acceptance check: total counts must
// match and every mapped field of every row must be byte-identical.
func verifyMediaParity(ctx context.Context, sqliteDB, pg *sql.DB, assetCols []string, assetTypes map[string]string, locCols []string, locTypes map[string]string, report *BackfillReport) error {
	var err error
	if report.SQLiteAssetCount, report.PostgresAssetCount, err = compareCounts(ctx, sqliteDB, pg, "media_assets"); err != nil {
		return err
	}
	if report.SQLiteLocationCount, report.PostgresLocationCount, err = compareCounts(ctx, sqliteDB, pg, "asset_locations"); err != nil {
		return err
	}
	if report.SQLiteAssetCount != report.PostgresAssetCount {
		report.addMismatch(fmt.Sprintf("media_assets count: sqlite=%d postgres=%d", report.SQLiteAssetCount, report.PostgresAssetCount))
	}
	if report.SQLiteLocationCount != report.PostgresLocationCount {
		report.addMismatch(fmt.Sprintf("asset_locations count: sqlite=%d postgres=%d", report.SQLiteLocationCount, report.PostgresLocationCount))
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
				vals[i] = coerceLegacyZero(vals[i], types[names[i]])
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
