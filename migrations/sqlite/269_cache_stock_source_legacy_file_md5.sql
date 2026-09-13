-- database: cache
-- 269_cache_stock_source_legacy_file_md5.sql
--
-- Cache-plane repair for the stock source download cache. Two distinct
-- defects are closed here:
--
--   1. The cache-plane `stock_source_cache` table was created by
--      260_cache_plane.sql with the legacy column name `file_hash`, while
--      229_rename_file_hash_to_legacy_file_md5.sql (scope: primary) cut the
--      runtime repository over to the canonical `legacy_file_md5` name.
--      Because 229 never ran against the cache DB, stocksourcecache.Repository
--      .Upsert/GetByCacheKey failed with "table stock_source_cache has no
--      column named legacy_file_md5" and the StockStager silently degraded
--      every run to a cache miss.
--
--   2. Fresh installs skip every historical migration covered by the
--      consolidated 000_baseline_267.sql (which contains only the quarantined
--      primary copy `legacy_cache_stock_source`), so the cache-plane table was
--      never created at all and the cache repositories failed with
--      "no such table: stock_source_cache".
--
-- EXPAND + BACKFILL phase (godlike/06 SSOT — expand → backfill → cutover →
-- contract): we ONLY create the canonical table / ADD the canonical column.
-- The legacy `file_hash` column is retained and backfilled, so both the
-- legacy and canonical names are readable during the minimum-blast-radius
-- window; a future contract-phase PR may DROP `file_hash`. The ALTER is
-- idempotent on fresh DBs because the runner soft-skips duplicate ADD COLUMN.
CREATE TABLE IF NOT EXISTS stock_source_cache (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cache_key TEXT NOT NULL UNIQUE,
    provider TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL,
    local_path TEXT NOT NULL,
    file_size INTEGER NOT NULL DEFAULT 0,
    file_hash TEXT NOT NULL DEFAULT '',
    legacy_file_md5 TEXT NOT NULL DEFAULT '',
    download_section TEXT NOT NULL DEFAULT '',
    merge_format TEXT NOT NULL DEFAULT '',
    force_keyframes INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'invalidated', 'expired')),
    last_verified_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_cache_stock_state ON stock_source_cache(state);
ALTER TABLE stock_source_cache ADD COLUMN legacy_file_md5 TEXT NOT NULL DEFAULT '';
UPDATE stock_source_cache SET legacy_file_md5 = file_hash WHERE file_hash != '';
