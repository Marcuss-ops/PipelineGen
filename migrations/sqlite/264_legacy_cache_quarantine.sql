-- database: primary
-- Cache Plane cutover.
--
-- All runtime cache repositories are now constructed with cache.db.sqlite.
-- Preserve historical primary rows for operator recovery, but remove the
-- cache table names from media.db.sqlite so a future writer cannot silently
-- reintroduce cache churn into the media WAL.
ALTER TABLE artlist_search_cache RENAME TO legacy_cache_artlist_search;
ALTER TABLE research_cache RENAME TO legacy_cache_research;
ALTER TABLE transcript_cache RENAME TO legacy_cache_transcript;
ALTER TABLE translation_cache RENAME TO legacy_cache_translation;
ALTER TABLE stock_source_cache RENAME TO legacy_cache_stock_source;
ALTER TABLE media_query_cache RENAME TO legacy_cache_media_query;
ALTER TABLE vidrush_provider_cache RENAME TO legacy_cache_vidrush_provider;
ALTER TABLE artifact_cache_entries RENAME TO legacy_cache_artifact_entries;
ALTER TABLE artifact_cache_metrics RENAME TO legacy_cache_artifact_metrics;
