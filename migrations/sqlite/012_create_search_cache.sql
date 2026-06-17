-- 012_create_search_cache.sql
-- Persistent cache for Artlist live search results (Level 1).
-- Sopravvive ai riavvii del server, riducendo le richieste a Chromium.
-- I record scaduti (> TTL) vengono rimossi al prossimo accesso.

CREATE TABLE IF NOT EXISTS artlist_search_cache (
    term TEXT PRIMARY KEY,
    clips_json TEXT NOT NULL,
    cached_at TEXT NOT NULL DEFAULT (datetime('now'))
);
