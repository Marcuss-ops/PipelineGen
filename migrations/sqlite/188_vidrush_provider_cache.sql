-- database: primary
-- Durable VidRush L2 cache. Empty provider results are never written by the
-- application, so a transient miss cannot become a permanent negative cache.
CREATE TABLE IF NOT EXISTS vidrush_provider_cache (
    namespace   TEXT NOT NULL,
    cache_key   TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (namespace, cache_key)
);

CREATE INDEX IF NOT EXISTS idx_vidrush_provider_cache_updated_at
    ON vidrush_provider_cache(updated_at);
