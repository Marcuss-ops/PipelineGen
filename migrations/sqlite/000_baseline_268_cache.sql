-- 000_baseline_268_cache.sql — plane-pure SQLite baseline (cache)
-- database: cache
-- baseline-covers: 308
--
-- Generated from a clean database that ran the WHOLE incremental corpus
-- (including 307/308), so this dump contains exactly the objects the plane
-- owns: no business table is created and then dropped on a fresh install.
-- The runner applies it in preference to the frozen shared dump
-- 000_baseline_268.sql (see migrations_discovery.go::selectBaselineForPlane).
--
-- DO NOT EDIT MANUALLY — regenerate via `go run ./cmd/gen_baseline`
-- and verify with `go test ./internal/platform/sqlite -run TestMigrations_PlaneBaselines`.

CREATE TABLE IF NOT EXISTS admin_mutation_audit (
    id TEXT PRIMARY KEY,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    action TEXT NOT NULL,
    previous_json TEXT,
    next_json TEXT,
    changed_fields_json TEXT,
    actor TEXT NOT NULL,
    request_id TEXT,
    idempotency_key TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    success INTEGER NOT NULL DEFAULT 1,
    error_message TEXT
);
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_entity
    ON admin_mutation_audit(entity_type, entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_actor
    ON admin_mutation_audit(actor, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_created_at
    ON admin_mutation_audit(created_at DESC);
CREATE TABLE IF NOT EXISTS render_attempt_analytics (
    attempt_id      TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    phrase_count    INTEGER NOT NULL DEFAULT 0,
    word_count      INTEGER NOT NULL DEFAULT 0,
    image_count     INTEGER NOT NULL DEFAULT 0,
    leak_count      INTEGER NOT NULL DEFAULT 0,
    render_ms       INTEGER NOT NULL DEFAULT 0,
    encode_ms       INTEGER NOT NULL DEFAULT 0,
    width           INTEGER NOT NULL DEFAULT 0,
    height          INTEGER NOT NULL DEFAULT 0,
    fps_num         INTEGER NOT NULL DEFAULT 0,
    fps_den         INTEGER NOT NULL DEFAULT 0,
    frame_count     INTEGER NOT NULL DEFAULT 0,
    duration_us     INTEGER NOT NULL DEFAULT 0,
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    sha256          TEXT NOT NULL DEFAULT '',
    drive_file_id   TEXT NOT NULL DEFAULT '',
    drive_link      TEXT NOT NULL DEFAULT '',
    recorded_at     TEXT NOT NULL
, completion_wait_ms INTEGER NOT NULL DEFAULT 0, polling_sleep_ms INTEGER NOT NULL DEFAULT 0, polling_interval_ms INTEGER NOT NULL DEFAULT 0, poll_count INTEGER NOT NULL DEFAULT 0, item_id TEXT NOT NULL DEFAULT '', backend TEXT NOT NULL DEFAULT '', chronon_version TEXT NOT NULL DEFAULT '', profile_id TEXT NOT NULL DEFAULT '', codec TEXT NOT NULL DEFAULT '', codec_profile TEXT NOT NULL DEFAULT '', container TEXT NOT NULL DEFAULT '', pixel_format TEXT NOT NULL DEFAULT '', materialize_ms INTEGER NOT NULL DEFAULT 0, plan_ms INTEGER NOT NULL DEFAULT 0, probe_ms INTEGER NOT NULL DEFAULT 0, hash_ms INTEGER NOT NULL DEFAULT 0, upload_ms INTEGER NOT NULL DEFAULT 0, drive_publish_ms INTEGER NOT NULL DEFAULT 0, metrics_json TEXT NOT NULL DEFAULT '', chronon_telemetry TEXT NOT NULL DEFAULT '', chronon_timing_storage_key TEXT NOT NULL DEFAULT '', chronon_timing_url TEXT NOT NULL DEFAULT '', chronon_timing_sha256 TEXT NOT NULL DEFAULT '', chronon_timing_size_bytes INTEGER NOT NULL DEFAULT 0, chronon_timing_content_type TEXT NOT NULL DEFAULT '', number_count INTEGER NOT NULL DEFAULT 0, submit_started_at TEXT, submit_accepted_at TEXT, wait_started_at TEXT, wait_finished_at TEXT, queue_queued_at TEXT, queue_started_at TEXT, queue_completed_at TEXT, artifact_available_at TEXT, outcome TEXT NOT NULL DEFAULT '', batch_id TEXT NOT NULL DEFAULT '', batch_item_ids_json TEXT NOT NULL DEFAULT '[]', batch_start_us INTEGER NOT NULL DEFAULT 0, batch_end_us INTEGER NOT NULL DEFAULT 0, cache_lookup_us INTEGER NOT NULL DEFAULT 0, cache_probe_us INTEGER NOT NULL DEFAULT 0, cache_store_us INTEGER NOT NULL DEFAULT 0, cache_outcome TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_render_attempt_analytics_job ON render_attempt_analytics(job_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_render_attempt_analytics_run
    ON render_attempt_analytics(run_id, recorded_at);
CREATE TABLE IF NOT EXISTS research_cache (
    key TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    language TEXT NOT NULL,
    max_steps INTEGER NOT NULL,
    source_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_used TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
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
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_cache_research_expiry ON research_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_cache_research_last_used ON research_cache(last_used);
CREATE TABLE IF NOT EXISTS transcript_cache (
    video_id TEXT PRIMARY KEY,
    transcript_text TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'en',
    cached_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE IF NOT EXISTS translation_cache (
    cache_key TEXT PRIMARY KEY,
    source_text_hash TEXT NOT NULL,
    target_language TEXT NOT NULL,
    translated_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_used TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
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
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_cache_stock_state ON stock_source_cache(state);
CREATE TABLE IF NOT EXISTS vidrush_provider_cache (
    namespace TEXT NOT NULL,
    cache_key TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (namespace, cache_key)
);
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
CREATE TABLE IF NOT EXISTS semantic_plan_cache (
    cache_key TEXT PRIMARY KEY,
    payload_json TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    provider_lane TEXT NOT NULL DEFAULT '',
    stored_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_semantic_plan_cache_expires ON semantic_plan_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_semantic_plan_cache_provider ON semantic_plan_cache(provider);
CREATE INDEX IF NOT EXISTS idx_semantic_plan_cache_provider_lane ON semantic_plan_cache(provider_lane);
CREATE INDEX IF NOT EXISTS idx_vidrush_provider_cache_updated_at
    ON vidrush_provider_cache(updated_at);
