package media_test

import (
	"context"
	"database/sql"
	"testing"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// TestBackfill_SQLiteToPostgres_ParityVerified proves the FASE-3 contract
// end-to-end on live engines: a migrated SQLite media database is copied
// into PostgreSQL and the fail-closed verifier proves row counts and every
// compared field match exactly. A second run re-copies with mutated SQLite
// rows to prove convergence (idempotent upserts) — the parity verifier must
// stay green after both runs.
func TestBackfill_SQLiteToPostgres_ParityVerified(t *testing.T) {
	dsn, ok := requirePostgresDSN(t)
	if !ok {
		return
	}

	// Hermetic PostgreSQL side: the test container persists rows across runs
	// (named volume), so reset the media surfaces before comparing counts.
	// (Same reset contract as every other test in this package.)
	_ = newMediaTestDB(t)

	sqliteDB := sqlite.NewMigratedTestDB(t)
	seed := []struct {
		id, source, name, lifecycle, indexState, metadata string
		duration                                          int64
	}{
		{"asset-001", "youtube", "Barista latte art close-up", "ACTIVE", "DISCOVERED", `{"youtube_video_id":"yt001"}`, 9500},
		{"asset-002", "artlist", "Crowd celebrating street", "ACTIVE", "NOT_INDEXABLE", `{"category":"celebrity"}`, 12000},
		{"asset-003", "stock", "Antique industrial machine", "ACTIVE", "INDEXED", `{}`, 7250},
	}
	for _, s := range seed {
		if _, err := sqliteDB.ExecContext(context.Background(),
			`INSERT INTO media_assets (id, source, name, lifecycle_state, index_state, duration_ms, metadata_json, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			s.id, s.source, s.name, s.lifecycle, s.indexState, s.duration, s.metadata); err != nil {
			t.Fatalf("seed sqlite media_assets: %v", err)
		}
	}
	// Two locations for asset-001 (local + drive), one for asset-002.
	seedLocations := []struct {
		assetID, kind, uri, hash string
		primary                  int
	}{
		{"asset-001", "local", "/data/clips/latte.mp4", "d41d8cd98f00b204e9800998ecf8427e", 1},
		{"asset-001", "drive", "1AbCdriveFileID", "", 0},
		{"asset-002", "drive", "1XyZdriveFileID", "", 1},
	}
	for _, l := range seedLocations {
		if _, err := sqliteDB.ExecContext(context.Background(),
			`INSERT INTO asset_locations (asset_id, location_kind, uri, file_hash, is_primary, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			l.assetID, l.kind, l.uri, l.hash, l.primary); err != nil {
			t.Fatalf("seed sqlite asset_locations: %v", err)
		}
	}

	cfg := pgmedia.BackfillConfig{
		SQLiteDSN:   sqliteDSNOf(t, sqliteDB),
		PostgresDSN: dsn,
	}
	report, err := pgmedia.RunMediaBackfill(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RunMediaBackfill: %v (report: %+v)", err, report)
	}
	if report.AssetsCopied != len(seed) || report.LocationsCopied != len(seedLocations) {
		t.Fatalf("unexpected copy counts: assets=%d locations=%d", report.AssetsCopied, report.LocationsCopied)
	}
	if report.MismatchCount != 0 {
		t.Fatalf("parity mismatches: %v", report.Mismatches)
	}

	// Convergence: mutate SQLite (new asset, changed duration), re-run, and
	// require the verifier to stay green — proving idempotent upserts sync
	// both sides to the same observable state.
	if _, err := sqliteDB.ExecContext(context.Background(),
		`UPDATE media_assets SET duration_ms = 9999 WHERE id = 'asset-001'`); err != nil {
		t.Fatalf("mutate sqlite: %v", err)
	}
	if _, err := sqliteDB.ExecContext(context.Background(),
		`INSERT INTO media_assets (id, source, name, lifecycle_state, index_state, duration_ms, created_at, updated_at)
		 VALUES ('asset-004', 'youtube', 'Woman walking city street', 'ACTIVE', 'DISCOVERED', 6100, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert extra sqlite row: %v", err)
	}
	report2, err := pgmedia.RunMediaBackfill(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RunMediaBackfill (second run): %v (report: %+v)", err, report2)
	}
	if report2.MismatchCount != 0 || report2.PostgresAssetCount != report.SQLiteAssetCount+1 {
		t.Fatalf("convergence failed: report2=%+v", report2)
	}

	// VerifyOnly mode must pass on the converged state without copying.
	report3, err := pgmedia.RunMediaBackfill(context.Background(), pgmedia.BackfillConfig{
		SQLiteDSN:   cfg.SQLiteDSN,
		PostgresDSN: dsn,
		VerifyOnly:  true,
	})
	if err != nil || report3.MismatchCount != 0 {
		t.Fatalf("verify-only run failed: err=%v report=%+v", err, report3)
	}
}

// sqliteDSNOf extracts the file path DSN of a test SQLite database. It runs
// exactly ONE PRAGMA query: the migrated test DB pool is capped at a single
// connection, so any second overlapping query would deadlock.
func sqliteDSNOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query("PRAGMA database_list")
	if err != nil {
		t.Fatalf("query pragma database_list: %v", err)
	}
	file := ""
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			rows.Close()
			t.Fatalf("scan pragma database_list: %v", err)
		}
		if name == "main" && path != "" {
			file = path
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate pragma database_list: %v", err)
	}
	rows.Close()
	if file == "" {
		t.Fatal("could not resolve sqlite test db file path")
	}
	return file + "?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"
}
