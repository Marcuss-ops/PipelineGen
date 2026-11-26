package sqlite

import "testing"

// TestMigrations_199_ArtifactCacheRegistryIsRetired pins the end-state of the
// artifact-cache registry after DEMOL-13: migration 199 moved it into the cache
// cutover and 264 quarantined it under legacy_cache_artifact_*; migration 301
// drops those archives once the census certified zero runtime references and
// zero rows. Neither generation may live in the schema.
func TestMigrations_199_ArtifactCacheRegistryIsRetired(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	for _, table := range []string{
		"artifact_cache_entries",
		"artifact_cache_metrics",
		"legacy_cache_artifact_entries",
		"legacy_cache_artifact_metrics",
	} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&count); err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("table %q must not exist after the 301 retirement drop", table)
		}
	}
}
