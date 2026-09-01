package sqlite

import "testing"

func TestMigrations_264_QuarantinesLegacyCacheTables(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()
	live := []string{
		"artlist_search_cache", "research_cache", "transcript_cache",
		"translation_cache", "stock_source_cache", "media_query_cache",
		"vidrush_provider_cache", "artifact_cache_entries", "artifact_cache_metrics",
	}
	for _, table := range live {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("cache table %q must not remain in media.db.sqlite", table)
		}
	}
	for _, table := range []string{
		"legacy_cache_artlist_search", "legacy_cache_research", "legacy_cache_transcript",
		"legacy_cache_translation", "legacy_cache_stock_source", "legacy_cache_media_query",
		"legacy_cache_vidrush_provider", "legacy_cache_artifact_entries", "legacy_cache_artifact_metrics",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("legacy cache archive %q missing", table)
		}
	}
}
