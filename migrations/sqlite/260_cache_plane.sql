-- database: cache
-- Cache tables are created only in the dedicated cache DB. The legacy primary
-- migration ledger remains authoritative for historical schema-contract tests.
-- Cache Plane schema. Every table in this migration is rebuildable and has
-- no foreign keys into business state. Cache outage is therefore equivalent
-- to a miss and must never block canonical media or job mutations.
CREATE TABLE IF NOT EXISTS research_cache (
    key TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    language TEXT NOT NULL,
    max_steps INTEGER NOT NULL,
    source_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used TEXT NOT NULL DEFAULT (datetime('now')),
    source_text_hash TEXT NOT NULL DEFAULT '',
    research_report_json TEXT NOT NULL DEFAULT '',
    sources_count INTEGER NOT NULL DEFAULT 0,
    claims_verified INTEGER NOT NULL DEFAULT 0,
    claims_rejected INTEGER NOT NULL DEFAULT 0,
    search_query_count INTEGER NOT NULL DEFAULT 0,
    pages_fetched INTEGER NOT NULL DEFAULT 0,
    concept_id TEXT,
    topic_fingerprint TEXT,
    source_fingerprint TEXT,
    resolver_version TEXT,
    research_version TEXT,
    hit_count INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_cache_research_expiry ON research_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_cache_research_last_used ON research_cache(last_used);

CREATE TABLE IF NOT EXISTS artlist_search_cache (
    term TEXT PRIMARY KEY,
    clips_json TEXT NOT NULL,
    cached_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS transcript_cache (
    video_id TEXT PRIMARY KEY,
    transcript_text TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'en',
    cached_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS translation_cache (
    cache_key TEXT PRIMARY KEY,
    source_text_hash TEXT NOT NULL,
    target_language TEXT NOT NULL,
    translated_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_cache_translation_last_used ON translation_cache(last_used);

CREATE TABLE IF NOT EXISTS stock_source_cache (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cache_key TEXT NOT NULL UNIQUE,
    provider TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL,
    local_path TEXT NOT NULL,
    file_size INTEGER NOT NULL DEFAULT 0,
    file_hash TEXT NOT NULL DEFAULT '',
    download_section TEXT NOT NULL DEFAULT '',
    merge_format TEXT NOT NULL DEFAULT '',
    force_keyframes INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'invalidated', 'expired')),
    last_verified_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_cache_stock_state ON stock_source_cache(state);

CREATE TABLE IF NOT EXISTS vidrush_provider_cache (
    namespace TEXT NOT NULL,
    cache_key TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (namespace, cache_key)
);
CREATE INDEX IF NOT EXISTS idx_cache_vidrush_updated ON vidrush_provider_cache(updated_at);

CREATE TABLE IF NOT EXISTS media_query_cache (
    id TEXT PRIMARY KEY,
    phrase_fingerprint TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL,
    request_json TEXT NOT NULL,
    result_json TEXT NOT NULL,
    provider_state_json TEXT,
    hit_count INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cache_media_query_expiry ON media_query_cache(expires_at);

CREATE TABLE IF NOT EXISTS artifact_cache_entries (
    cache_key TEXT PRIMARY KEY,
    source_sha256 TEXT NOT NULL,
    operation TEXT NOT NULL,
    parameters_json TEXT NOT NULL,
    processor_version TEXT NOT NULL,
    artifact_sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    mime_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'READY' CHECK (status IN ('BUILDING','READY','FAILED','INVALID')),
    lease_id TEXT NOT NULL DEFAULT '',
    lease_until TEXT,
    created_at TEXT NOT NULL,
    last_accessed_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_cache_artifact_status_lease ON artifact_cache_entries(status, lease_until);
CREATE TABLE IF NOT EXISTS artifact_cache_metrics (
    operation TEXT PRIMARY KEY,
    hit_count INTEGER NOT NULL DEFAULT 0,
    miss_count INTEGER NOT NULL DEFAULT 0,
    invalidation_count INTEGER NOT NULL DEFAULT 0,
    avoided_bytes INTEGER NOT NULL DEFAULT 0,
    avoided_work_ms INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
