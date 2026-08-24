-- 160_stock_source_cache.sql — cross-run source download cache for stock pipeline.
--
-- Purpose: avoid downloading the same YouTube/Drive video 12+ times when
-- multiple rounds reference the same source URL. The cache maps a
-- deterministic key (provider + canonical URL + download section) to the
-- on-disk path of a previously downloaded file. The StockStager checks
-- this cache before invoking yt-dlp and populates it after a fresh
-- download completes.
--
-- Companion code path:
--   internal/platform/sqlite/stocksourcecache/repository.go
--   internal/application/assets/providers/stock/stockpipeline/stager_adapter.go
--
-- Down migration: DROP TABLE IF EXISTS stock_source_cache;

CREATE TABLE IF NOT EXISTS stock_source_cache (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    cache_key       TEXT    NOT NULL,
    provider        TEXT    NOT NULL DEFAULT '',
    external_id     TEXT    NOT NULL DEFAULT '',
    source_url      TEXT    NOT NULL,
    local_path      TEXT    NOT NULL,
    file_size       INTEGER NOT NULL DEFAULT 0,
    file_hash       TEXT    NOT NULL DEFAULT '',
    download_section TEXT   NOT NULL DEFAULT '',
    merge_format    TEXT    NOT NULL DEFAULT '',
    force_keyframes INTEGER NOT NULL DEFAULT 0,
    state           TEXT    NOT NULL DEFAULT 'active'
                            CHECK (state IN ('active', 'invalidated', 'expired')),
    last_verified_at TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_stock_source_cache_key
    ON stock_source_cache(cache_key);

CREATE INDEX IF NOT EXISTS idx_stock_source_cache_state
    ON stock_source_cache(state);

CREATE INDEX IF NOT EXISTS idx_stock_source_cache_provider_external
    ON stock_source_cache(provider, external_id);
