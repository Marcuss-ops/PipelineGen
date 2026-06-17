-- 023_create_translation_cache.sql
-- Translation cache to avoid re-translating the same texts via LLM
-- Key is SHA256(source_text + target_language) for deterministic lookup

CREATE TABLE IF NOT EXISTS translation_cache (
    cache_key TEXT PRIMARY KEY,
    source_text_hash TEXT NOT NULL,
    target_language TEXT NOT NULL,
    translated_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_translation_cache_source_hash ON translation_cache(source_text_hash);
CREATE INDEX IF NOT EXISTS idx_translation_cache_last_used ON translation_cache(last_used);
CREATE INDEX IF NOT EXISTS idx_translation_cache_lang ON translation_cache(target_language);
