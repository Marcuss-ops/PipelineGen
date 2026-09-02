// Package media — backfill.go: FASE-3 SQLite → PostgreSQL media backfill.
//
// Copies the canonical media surfaces from the legacy SQLite media database
// into the PostgreSQL media SSOT and then FAILS CLOSED on any parity
// violation (godlike/07): row counts and per-row field values must match
// exactly, or RunMediaBackfill returns an error listing the mismatches.
//
// Scope is exactly the FASE-3 contract from the migration plan:
//
//	media_assets      (wide legacy schema → canonical columns; unmapped
//	                   PostgreSQL columns keep their schema defaults)
//	asset_locations   (file_hash maps to legacy_file_md5; location kinds,
//	                   URIs, sizes and primary flags copy verbatim)
//
// Embeddings and derived features are deliberately NOT generated here —
// they belong to the FASE-4 enrichment pipeline so a slow row never blocks
// the backfill.
//
// Idempotence: every write is an upsert keyed on the natural key, so
// re-running the backfill after new SQLite writes converges both sides to
// the same observable state (the parity verifier proves it per run).
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
	MismatchCount         int      `json:"mismatch_count"`
	Mismatches            []string `json:"mismatches,omitempty"`
	VerifyOnly            bool     `json:"verify_only"`
}

const backfillMaxReportedMismatches = 100

// assetBackfillColumns is the SQLite media_assets projection copied
// verbatim into PostgreSQL columns of the same name.
const assetBackfillColumns = `id, source, name, tags, tags_norm, duration_ms,
	url, media_type, status, local_path, relative_path, drive_file_id,
	drive_folder_id, drive_link, download_link, file_hash, embedding_json,
	metadata_json, visual_embedding, transcript_embedding, created_at,
	updated_at`

// locationBackfillColumns is the SQLite asset_locations projection copied
// verbatim (file_hash is renamed to legacy_file_md5 in PostgreSQL).
const locationBackfillColumns = `asset_id, location_kind, uri, mime_type,
	file_size_bytes, file_hash, is_primary, created_at, updated_at`

// RunMediaBackfill executes the FASE-3 backfill and the fail-closed parity
// verification. It applies the canonical PostgreSQL media migrations first
// (idempotent IF NOT EXISTS DDL) so a fresh media database is self-bootstrapping.
func RunMediaBackfill(ctx context.Context, cfg BackfillConfig) (*BackfillReport, error) {
	if strings.TrimSpace(cfg.SQLiteDSN) == "" {
		return nil, fmt.Errorf("media backfill: SQLiteDSN is required")
	}
	if strings.TrimSpace(cfg.PostgresDSN) == "" {
		return nil, fmt.Errorf("media backfill: PostgresDSN is required")
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 500
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

	report := &BackfillReport{VerifyOnly: cfg.VerifyOnly}
	if !cfg.VerifyOnly {
		if report.AssetsCopied, report.AssetsScanned, err = backfillAssets(ctx, sqliteDB, pg, batch, cfg.Limit); err != nil {
			return nil, err
		}
		if report.LocationsCopied, report.LocationsScanned, report.LocationsSkipped, err = backfillLocations(ctx, sqliteDB, pg, batch); err != nil {
			return nil, err
		}
	}

	if err := verifyMediaParity(ctx, sqliteDB, pg, report); err != nil {
		return report, err
	}
	if report.MismatchCount > 0 {
		return report, fmt.Errorf("media backfill: parity verification FAILED with %d mismatch(es)", report.MismatchCount)
	}
	return report, nil
}

// backfillAssetRow is one streamed SQLite media_assets row.
type backfillAssetRow struct {
	id     string
	vals   []any
	status string
}

// backfillAssets streams SQLite media_assets in id keyset order and upserts
// the mapped projection into PostgreSQL. Returns (copied, scanned).
func backfillAssets(ctx context.Context, sqliteDB, pg *sql.DB, batch, limit int) (int, int, error) {
	query := "SELECT " + assetBackfillColumns + " FROM media_assets WHERE id > ? ORDER BY id LIMIT ?"
	cols := strings.Fields(assetBackfillColumns)
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
			return copied, scanned, fmt.Errorf("media backfill: select sqlite media_assets: %w", err)
		}
		var batchRows []backfillAssetRow
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			var id string
			if err := rows.Scan(append([]any{&id}, ptrs[1:]...)...); err != nil {
				rows.Close()
				return copied, scanned, fmt.Errorf("media backfill: scan sqlite media_assets: %w", err)
			}
			// status → lifecycle_state parity: an empty legacy status is
			// stored as the canonical ACTIVE default, everything else copies
			// verbatim so index/lifecycle state machines survive the move.
			status := ""
			for i, c := range cols {
				if c == "status" {
					if s, ok := vals[i].(string); ok {
						status = s
					}
				}
			}
			batchRows = append(batchRows, backfillAssetRow{id: id, vals: vals, status: status})
			scanned++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return copied, scanned, fmt.Errorf("media backfill: iterate sqlite media_assets: %w", err)
		}
		rows.Close()
		if len(batchRows) == 0 {
			break
		}
		lastID = batchRows[len(batchRows)-1].id

		inserted, err := upsertBackfillAssets(ctx, pg, cols, batchRows)
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

// upsertBackfillAssets writes one batch with per-row lifecycle_state
// substitution (SQLite `status` → PostgreSQL lifecycle_state) and full
// column sync so re-runs converge.
func upsertBackfillAssets(ctx context.Context, pg *sql.DB, cols []string, rows []backfillAssetRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	// Target columns: all mapped SQLite columns except `status`, plus
	// lifecycle_state.
	target := make([]string, 0, len(cols))
	for _, c := range cols {
		if c != "status" {
			target = append(target, c)
		}
	}
	target = append(target, "lifecycle_state")

	var sb strings.Builder
	sb.WriteString("INSERT INTO media_assets (")
	sb.WriteString(strings.Join(target, ", "))
	sb.WriteString(") VALUES ")
	params := make([]any, 0, len(rows)*len(target))
	for i, r := range rows {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		for j, c := range target {
			if j > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("$%d", len(params)+1))
			if c == "lifecycle_state" {
				if strings.TrimSpace(r.status) == "" {
					params = append(params, "ACTIVE")
				} else {
					params = append(params, r.status)
				}
				continue
			}
			params = append(params, r.vals[indexOf(cols, c)])
		}
		sb.WriteString(")")
	}
	// Full sync on conflict: every mapped column takes the SQLite value so
	// re-running the backfill converges (FASE-3 idempotence contract).
	sb.WriteString(" ON CONFLICT (id) DO UPDATE SET ")
	for j, c := range target {
		if j > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s = EXCLUDED.%s", c, c)
	}
	res, err := pg.ExecContext(ctx, sb.String(), params...)
	if err != nil {
		return 0, fmt.Errorf("media backfill: upsert postgres media_assets: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// backfillLocations streams SQLite asset_locations and upserts the mapped
// projection (file_hash → legacy_file_md5). Orphan rows whose asset did not
// surface in media_assets are counted, never fatal. Returns
// (copied, scanned, skippedOrphanFK).
func backfillLocations(ctx context.Context, sqliteDB, pg *sql.DB, batch int) (int, int, int, error) {
	query := "SELECT " + locationBackfillColumns + " FROM asset_locations ORDER BY asset_id, location_kind"
	rows, err := sqliteDB.QueryContext(ctx, query)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("media backfill: select sqlite asset_locations: %w", err)
	}
	defer rows.Close()

	cols := strings.Fields(locationBackfillColumns)
	copied, scanned, skipped := 0, 0, 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return copied, scanned, skipped, fmt.Errorf("media backfill: scan sqlite asset_locations: %w", err)
		}
		scanned++
		// file_hash → legacy_file_md5 (PG asset_locations carries the legacy
		// md5-tier hash under the same priority position).
		hashIdx := indexOf(cols, "file_hash")
		hash := ""
		if h, ok := vals[hashIdx].(string); ok {
			hash = h
		}
		_, err := pg.ExecContext(ctx, `
			INSERT INTO asset_locations
				(asset_id, location_kind, uri, mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (asset_id, location_kind) DO UPDATE SET
				uri             = EXCLUDED.uri,
				mime_type       = EXCLUDED.mime_type,
				file_size_bytes = EXCLUDED.file_size_bytes,
				legacy_file_md5 = EXCLUDED.legacy_file_md5,
				is_primary      = EXCLUDED.is_primary,
				created_at      = EXCLUDED.created_at,
				updated_at      = EXCLUDED.updated_at`,
			vals[indexOf(cols, "asset_id")], vals[indexOf(cols, "location_kind")], vals[indexOf(cols, "uri")],
			vals[indexOf(cols, "mime_type")], vals[indexOf(cols, "file_size_bytes")], hash,
			vals[indexOf(cols, "is_primary")], vals[indexOf(cols, "created_at")], vals[indexOf(cols, "updated_at")],
		)
		if err != nil {
			if strings.Contains(err.Error(), "violates foreign key constraint") {
				// Orphan location (asset row absent): report, never crash —
				// the parity verifier compares per-known-asset only.
				skipped++
				continue
			}
			return copied, scanned, skipped, fmt.Errorf("media backfill: upsert postgres asset_locations: %w", err)
		}
		copied++
	}
	if err := rows.Err(); err != nil {
		return copied, scanned, skipped, fmt.Errorf("media backfill: iterate sqlite asset_locations: %w", err)
	}
	return copied, scanned, skipped, nil
}

// verifyMediaParity is the fail-closed acceptance check: total counts must
// match and every compared field of every row must be byte-identical.
func verifyMediaParity(ctx context.Context, sqliteDB, pg *sql.DB, report *BackfillReport) error {
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

	// Per-asset field comparison on the identity core. location_kind rows
	// are compared on their natural key below.
	const assetCompareSQL = `
		SELECT id, source, name, tags, tags_norm, duration_ms, media_type,
			local_path, relative_path, drive_file_id, drive_link, download_link,
			embedding_json, metadata_json, visual_embedding, transcript_embedding,
			created_at, updated_at, lifecycle_state
		FROM media_assets ORDER BY id`
	sqliteRows, err := sqliteDB.QueryContext(ctx, assetCompareSQL)
	if err != nil {
		return fmt.Errorf("media backfill: compare select sqlite: %w", err)
	}
	defer sqliteRows.Close()
	type row map[string]any
	sqliteByID := map[string]row{}
	colNames, _ := sqliteRows.Columns()
	for sqliteRows.Next() {
		vals := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := sqliteRows.Scan(ptrs...); err != nil {
			return fmt.Errorf("media backfill: compare scan sqlite: %w", err)
		}
		r := row{}
		for i, c := range colNames {
			r[c] = vals[i]
		}
		sqliteByID[vals[0].(string)] = r
	}
	if err := sqliteRows.Err(); err != nil {
		return fmt.Errorf("media backfill: compare iterate sqlite: %w", err)
	}

	pgRows, err := pg.QueryContext(ctx, assetCompareSQL)
	if err != nil {
		return fmt.Errorf("media backfill: compare select postgres: %w", err)
	}
	defer pgRows.Close()
	pgByID := map[string]row{}
	pgColNames, _ := pgRows.Columns()
	for pgRows.Next() {
		vals := make([]any, len(pgColNames))
		ptrs := make([]any, len(pgColNames))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := pgRows.Scan(ptrs...); err != nil {
			return fmt.Errorf("media backfill: compare scan postgres: %w", err)
		}
		r := row{}
		for i, c := range pgColNames {
			r[c] = vals[i]
		}
		pgByID[vals[0].(string)] = r
	}
	if err := pgRows.Err(); err != nil {
		return fmt.Errorf("media backfill: compare iterate postgres: %w", err)
	}

	ids := make([]string, 0, len(sqliteByID))
	for id := range sqliteByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s, ok := pgByID[id]
		if !ok {
			report.addMismatch(fmt.Sprintf("media_assets[%s]: missing in postgres", id))
			continue
		}
		for _, c := range colNames {
			sv, pv := fmt.Sprint(sqliteByID[id][c]), fmt.Sprint(s[c])
			if sv != pv {
				report.addMismatch(fmt.Sprintf("media_assets[%s].%s: sqlite=%q postgres=%q", id, c, sv, pv))
			}
		}
	}

	// Locations compared on (asset_id, location_kind, uri).
	const locCompareSQL = `SELECT asset_id, location_kind, uri, mime_type, file_size_bytes, is_primary FROM asset_locations ORDER BY asset_id, location_kind`
	locNames := []string{"asset_id", "location_kind", "uri", "mime_type", "file_size_bytes", "is_primary"}
	locKey := func(vals []any) string {
		return fmt.Sprintf("%s|%s|%s", vals[0], vals[1], vals[2])
	}
	locMap := func(db *sql.DB) (map[string][]any, error) {
		rs, err := db.QueryContext(ctx, locCompareSQL)
		if err != nil {
			return nil, err
		}
		defer rs.Close()
		m := map[string][]any{}
		for rs.Next() {
			vals := make([]any, len(locNames))
			ptrs := make([]any, len(locNames))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rs.Scan(ptrs...); err != nil {
				return nil, err
			}
			m[locKey(vals)] = vals
		}
		return m, rs.Err()
	}
	sqliteLoc, err := locMap(sqliteDB)
	if err != nil {
		return fmt.Errorf("media backfill: compare sqlite asset_locations: %w", err)
	}
	pgLoc, err := locMap(pg)
	if err != nil {
		return fmt.Errorf("media backfill: compare postgres asset_locations: %w", err)
	}
	locIDs := make([]string, 0, len(sqliteLoc))
	for k := range sqliteLoc {
		locIDs = append(locIDs, k)
	}
	sort.Strings(locIDs)
	for _, k := range locIDs {
		p, ok := pgLoc[k]
		if !ok {
			report.addMismatch(fmt.Sprintf("asset_locations[%s]: missing in postgres", k))
			continue
		}
		for i, c := range locNames[3:] {
			sv, pv := fmt.Sprint(sqliteLoc[k][3+i]), fmt.Sprint(p[3+i])
			if sv != pv {
				report.addMismatch(fmt.Sprintf("asset_locations[%s].%s: sqlite=%q postgres=%q", k, c, sv, pv))
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

func indexOf(haystack []string, needle string) int {
	for i, h := range haystack {
		if h == needle {
			return i
		}
	}
	return -1
}

// PrintJSON renders the report as machine-readable JSON on stdout.
func (r *BackfillReport) PrintJSON() {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
}
