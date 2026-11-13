package sqlite

import (
	"path/filepath"
	"testing"
)

// TestMigrations_292_RenderAttemptAnalyticsCache reproduces the cache schema
// drift: the cache database applied the baseline ledger row but does not carry
// render_attempt_analytics. Migration 292 must recreate the CURRENT canonical
// table (not the old baseline shape) and the indexes the verifier expects.
func TestMigrations_292_RenderAttemptAnalyticsCache(t *testing.T) {
	targetDir, err := filepath.Abs(migrationsDirFrom)
	if err != nil {
		t.Fatal(err)
	}
	preDir := copyMigrationSubset(t, targetDir, 291, map[int]bool{0: true})
	dbPath := filepath.Join(t.TempDir(), "cache-292.sqlite")
	if err := RunMigrationsOnDB(dbPath, nil, preDir, "cache"); err != nil {
		t.Fatalf("apply cache migrations up to 291: %v", err)
	}
	db := openMigrationTestDB(t, dbPath)

	// The production cache database is missing the table even though its
	// baseline ledger row is applied. Drop it here to reproduce that state.
	if _, err := db.Exec(`DROP TABLE IF EXISTS render_attempt_analytics`); err != nil {
		t.Fatalf("drop render_attempt_analytics: %v", err)
	}

	fullDir := copyMigrationSubset(t, targetDir, 292, map[int]bool{0: true})
	if err := RunMigrationsOnDB(dbPath, nil, fullDir, "cache"); err != nil {
		t.Fatalf("apply migration 292: %v", err)
	}

	for _, column := range []string{
		"run_id", "number_count", "batch_item_ids_json", "cache_outcome",
		"chronon_timing_content_type", "recorded_at",
	} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('render_attempt_analytics') WHERE name = ?`, column,
		).Scan(&count); err != nil {
			t.Fatalf("inspect render_attempt_analytics.%s: %v", column, err)
		}
		if count != 1 {
			t.Errorf("migration 292 did not create render_attempt_analytics.%s", column)
		}
	}

	for _, index := range []string{
		"idx_render_attempt_analytics_job",
		"idx_render_attempt_analytics_run",
	} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index,
		).Scan(&count); err != nil {
			t.Fatalf("inspect %s: %v", index, err)
		}
		if count != 1 {
			t.Errorf("migration 292 did not create %s", index)
		}
	}
}
