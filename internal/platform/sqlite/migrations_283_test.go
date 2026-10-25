package sqlite

import (
	"path/filepath"
	"testing"
)

// TestMigrations_283_SemanticPlanCacheTable pins the durable plan cache
// schema: the table exists in the CACHE plane only, carries the columns the
// adapter reads and writes, and keeps the (cache_key) upsert contract.
func TestMigrations_283_SemanticPlanCacheTable(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	fullDir := copyMigrationSubset(t, targetDir, 283, nil)

	cachePath := filepath.Join(t.TempDir(), "cache-283.sqlite")
	if err := RunMigrationsOnDB(cachePath, nil, fullDir, "cache"); err != nil {
		t.Fatalf("apply cache migrations up to 283: %v", err)
	}
	cacheDB := openMigrationTestDB(t, cachePath)
	defer func() { _ = cacheDB.Close() }()

	cols := scanColumnNames(t, cacheDB, "semantic_plan_cache")
	for _, column := range []string{"cache_key", "payload_json", "provider", "provider_lane", "stored_at", "expires_at"} {
		if _, ok := cols[column]; !ok {
			t.Fatalf("semantic_plan_cache missing column %q; got %v", column, cols)
		}
	}

	// The adapter upserts on cache_key and reads the payload/provider back.
	if _, err := cacheDB.Exec(`
		INSERT INTO semantic_plan_cache(cache_key, payload_json, provider, provider_lane, stored_at, expires_at)
		VALUES ('alberto stasi', '{"hits":[]}', 'duckduckgo', 'duckduckgo', '2026-10-24T12:00:00Z', '2026-10-24T13:00:00Z')
		ON CONFLICT(cache_key) DO UPDATE SET payload_json = excluded.payload_json`); err != nil {
		t.Fatalf("upsert semantic_plan_cache: %v", err)
	}
	var provider string
	if err := cacheDB.QueryRow(`SELECT provider FROM semantic_plan_cache WHERE cache_key = 'alberto stasi'`).Scan(&provider); err != nil {
		t.Fatalf("read semantic_plan_cache: %v", err)
	}
	if provider != "duckduckgo" {
		t.Fatalf("provider = %q, want duckduckgo", provider)
	}

	// The rebuilt cache plane must not leak into the primary database: the
	// table is rebuildable and has no business-state readers there.
	primaryPath := filepath.Join(t.TempDir(), "primary-283.sqlite")
	if err := RunMigrationsOnDB(primaryPath, nil, fullDir, "primary"); err != nil {
		t.Fatalf("apply primary migrations up to 283: %v", err)
	}
	primaryDB := openMigrationTestDB(t, primaryPath)
	defer func() { _ = primaryDB.Close() }()
	var count int
	if err := primaryDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'semantic_plan_cache'`).Scan(&count); err != nil {
		t.Fatalf("inspect primary schema: %v", err)
	}
	if count != 0 {
		t.Fatal("semantic_plan_cache must belong to the cache plane, not the primary database")
	}
}
