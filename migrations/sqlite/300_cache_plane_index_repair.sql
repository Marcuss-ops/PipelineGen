-- database: cache
-- 300_cache_plane_index_repair.sql
--
-- 299 recreated `vidrush_provider_cache` after 264 quarantined the old table as
-- `legacy_cache_vidrush_provider`. The quarantined table still owns the 188
-- index name `idx_vidrush_provider_cache_updated_at`, and SQLite index names
-- are database-global: 299's `CREATE INDEX IF NOT EXISTS` therefore skipped the
-- index on the canonical table, and startup verification refused the schema
-- ("applied migration declares an index that the live schema does not carry on
-- its table").
--
-- 299 is already applied and its bytes are part of the migration identity, so
-- the repair belongs here: drop the index from the quarantined table (a
-- performance-only index on a table nothing reads) and recreate it on the
-- canonical cache table. Idempotent and rebuildable by construction.

DROP INDEX IF EXISTS idx_vidrush_provider_cache_updated_at;
CREATE INDEX IF NOT EXISTS idx_vidrush_provider_cache_updated_at
    ON vidrush_provider_cache(updated_at);
