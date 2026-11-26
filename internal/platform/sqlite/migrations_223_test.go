package sqlite

import "testing"

// TestMigrations_223ArtlistCatalogIsRetired pins the retirement of the Artlist
// catalog. Migration 223 created artlist_clips/artlist_queries/
// artlist_query_clips, but the provider is retired (VELOX_FEATURE_ARTLIST_ENABLED
// is false and no runtime code reads these tables). The 2026-11-26 census found
// ZERO rows and ZERO runtime references, so DEMOL-13/301 drops them.
func TestMigrations_223ArtlistCatalogIsRetired(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	for _, table := range []string{
		"artlist_clips",
		"artlist_queries",
		"artlist_query_clips",
		"artlist_runs",
		"artlist_download_audit",
		"artlist_search_cache",
	} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&count); err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("retired Artlist table %q must be dropped by 301", table)
		}
	}
}
