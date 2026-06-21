// Package assets — SQLite-specific cache types for the YouTube pipeline.
//
// PR2 (June 2026): YouTubeCacheEntry was previously defined in
// internal/application/youtube/ports.go as a convenience DTO but that
// violated AGENTS.md Pattern 8 (application layer = thin transport only;
// SQL row shapes belong in the database adapter layer). It now lives here
// alongside the other SQLite-specific row types (MonitoredSourceRow, etc.).
//
// The single consumer is internal/app/youtube_adapters.go::cacheStoreAdapter.
// When the adapter is migrated to a dedicated repository (future Wave 5
// follow-up), this type will be consumed exclusively by that repository.
package assets

// YouTubeCacheEntry is the row shape returned when listing hot metadata
// cache entries from the youtube_video_metadata_cache table. It carries
// no db:"..." tags because the consumer (cacheStoreAdapter.ListHotMetadata)
// scans columns positionally via rows.Scan(&e.VideoID, &e.MetadataJSON).
//
// Previously defined in internal/application/youtube/ports.go (June 2026).
type YouTubeCacheEntry struct {
	VideoID      string `json:"video_id"`
	MetadataJSON string `json:"metadata_json"`
}
