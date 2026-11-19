package sqlite

import (
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// TestMigrations_299_CachePlaneRepair reproduces the cache-plane drift on a
// fresh database: the consolidated baseline declares coverage through 298 but
// only dumps the quarantined legacy_cache_* tables, so 260/274 (cache plane),
// 283 (semantic plan cache) and 292 (render analytics) never run. Migration
// 299 must create every canonical cache table and stay idempotent.
func TestMigrations_299_CachePlaneRepair(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "cache-299.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "cache"); err != nil {
		t.Fatalf("apply cache migrations through 299: %v", err)
	}
	db := openMigrationTestDB(t, dbPath)

	for _, table := range []string{
		"research_cache",
		"artlist_search_cache",
		"transcript_cache",
		"translation_cache",
		"stock_source_cache",
		"vidrush_provider_cache",
		"media_query_cache",
		"artifact_cache_entries",
		"artifact_cache_metrics",
		"semantic_plan_cache",
		"render_attempt_analytics",
	} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&count); err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("migration 299 did not create cache table %s", table)
		}
	}

	// The two tables that failed in production must be usable, not just
	// present: the adapters write and read the exact column sets below.
	if _, err := db.Exec(
		`INSERT INTO vidrush_provider_cache(namespace, cache_key, payload_json) VALUES ('image', 'k', '{}')`,
	); err != nil {
		t.Fatalf("insert into vidrush_provider_cache: %v", err)
	}
	var payload string
	if err := db.QueryRow(
		`SELECT payload_json FROM vidrush_provider_cache WHERE namespace='image' AND cache_key='k'`,
	).Scan(&payload); err != nil || payload != "{}" {
		t.Fatalf("read vidrush_provider_cache: payload=%q err=%v", payload, err)
	}

	if _, err := db.Exec(
		`INSERT INTO semantic_plan_cache(cache_key, payload_json, provider, provider_lane, stored_at, expires_at)
		 VALUES ('k', '{}', 'searxng', 'images', '2026-11-18T00:00:00Z', '2026-11-19T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert into semantic_plan_cache: %v", err)
	}

	// Re-applying the whole corpus must not fail on the repair migration.
	if err := RunMigrationsOnDB(dbPath, nil, targetDir, "cache"); err != nil {
		t.Fatalf("re-apply cache migrations (299 must be idempotent): %v", err)
	}

	// The repaired index must live on the canonical table: the legacy table
	// quarantined by 264 already owns the 188 name, which is why 299's
	// IF NOT EXISTS skipped it and startup verification refused the schema.
	var indexTable string
	if err := db.QueryRow(
		`SELECT tbl_name FROM sqlite_master WHERE type='index' AND name='idx_vidrush_provider_cache_updated_at'`,
	).Scan(&indexTable); err != nil {
		t.Fatalf("repaired index missing after 300: %v", err)
	}
	if indexTable != "vidrush_provider_cache" {
		t.Fatalf("repaired index lives on %s, want vidrush_provider_cache", indexTable)
	}
	if err := verifyDeclaredTables(db, targetDir, "cache", zap.NewNop()); err != nil {
		t.Fatalf("cache schema must satisfy every applied declaration: %v", err)
	}
}
