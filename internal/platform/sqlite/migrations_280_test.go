package sqlite

import "testing"

func TestMigrations_280_RenderAttemptCacheTimingColumns(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	for _, column := range []string{"cache_lookup_us", "cache_probe_us", "cache_store_us", "cache_outcome"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('render_attempt_analytics') WHERE name = ?`, column).Scan(&count); err != nil {
			t.Fatalf("inspect render_attempt_analytics.%s: %v", column, err)
		}
		if count != 1 {
			t.Errorf("migration 280 did not add render_attempt_analytics.%s", column)
		}
	}
}
