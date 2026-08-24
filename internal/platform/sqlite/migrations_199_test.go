package sqlite

import "testing"

func TestMigrations_199_ArtifactCacheRegistry(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	entryCols := scanColumnNames(t, db, "artifact_cache_entries")
	for _, want := range []string{
		"cache_key", "source_sha256", "operation", "parameters_json",
		"processor_version", "artifact_sha256", "size_bytes", "status",
		"lease_id", "lease_until", "last_accessed_at",
	} {
		if _, ok := entryCols[want]; !ok {
			t.Fatalf("artifact_cache_entries missing column %q (present: %v)", want, entryCols)
		}
	}
	metricCols := scanColumnNames(t, db, "artifact_cache_metrics")
	for _, want := range []string{
		"operation", "hit_count", "miss_count", "invalidation_count",
		"avoided_bytes", "avoided_work_ms", "updated_at",
	} {
		if _, ok := metricCols[want]; !ok {
			t.Fatalf("artifact_cache_metrics missing column %q (present: %v)", want, metricCols)
		}
	}
	indexes := mustReadIndexNames(t, db, "artifact_cache_entries")
	for _, want := range []string{"idx_artifact_cache_source_operation", "idx_artifact_cache_status_lease"} {
		found := false
		for _, got := range indexes {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("artifact_cache_entries missing index %q (present: %v)", want, indexes)
		}
	}
}
