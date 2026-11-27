-- database: primary,observability,cache
-- DB-07 (2026-11-27): drop indexes that duplicate a canonical sibling.
-- Both pairs index the same column with the same order; keeping both only
-- doubles write cost and disk. The canonical survivors are
-- idx_media_assets_lifecycle_state (latest definition) and
-- idx_vidrush_provider_cache_updated_at (referenced by the cache contract).

DROP INDEX IF EXISTS idx_media_assets_lifecycle;
DROP INDEX IF EXISTS idx_cache_vidrush_updated;
