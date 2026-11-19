-- database: cache
-- 299_cache_plane_repair.sql
--
-- The consolidated baseline declares `-- baseline-covers: 298` but was dumped
-- from a schema that only carries the quarantined legacy_cache_* tables: the
-- canonical cache plane (260 -> 274), the semantic plan cache (283) and the
-- render analytics cache (292) are absent. Because the baseline threshold
-- covers those versions, every fresh cache database skips them, so the live
-- cache answers `no such table: vidrush_provider_cache` /
-- `no such table: semantic_plan_cache` and every run repeats provider work.
--
-- This is the numbered repair migration the drift demands, mirroring 292 for
-- the whole cache plane. It is idempotent: every table and index uses
-- IF NOT EXISTS, and the runner soft-skips a duplicate ADD COLUMN, so an
-- older cache database is upgraded and a current one no-ops.
--
-- Every table here is rebuildable and has no foreign keys into business
-- state: a cache outage is a miss, never a failed run.

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
-- 188 shipped this index name on the primary database; keep it so an existing
-- index is reused instead of duplicated.
CREATE INDEX IF NOT EXISTS idx_vidrush_provider_cache_updated_at
    ON vidrush_provider_cache(updated_at);

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

-- 283: durable L2 cache for the semantic production planner.
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

-- 292: the render analytics cache (215+227 shape plus every later column).
CREATE TABLE IF NOT EXISTS render_attempt_analytics (
    attempt_id      TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    phrase_count    INTEGER NOT NULL DEFAULT 0,
    word_count      INTEGER NOT NULL DEFAULT 0,
    image_count     INTEGER NOT NULL DEFAULT 0,
    leak_count      INTEGER NOT NULL DEFAULT 0,
    render_ms       INTEGER NOT NULL DEFAULT 0,
    encode_ms       INTEGER NOT NULL DEFAULT 0,
    completion_wait_ms INTEGER NOT NULL DEFAULT 0,
    polling_sleep_ms INTEGER NOT NULL DEFAULT 0,
    polling_interval_ms INTEGER NOT NULL DEFAULT 0,
    poll_count      INTEGER NOT NULL DEFAULT 0,
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
);
ALTER TABLE render_attempt_analytics ADD COLUMN item_id TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN backend TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_version TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN profile_id TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN codec TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN codec_profile TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN container TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN pixel_format TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN materialize_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN plan_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN probe_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN hash_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN upload_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN drive_publish_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN metrics_json TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_telemetry TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_storage_key TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_url TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_size_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN chronon_timing_content_type TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN number_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN submit_started_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN submit_accepted_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN wait_started_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN wait_finished_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN queue_queued_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN queue_started_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN queue_completed_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN artifact_available_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN batch_item_ids_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE render_attempt_analytics ADD COLUMN batch_start_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN batch_end_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_lookup_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_probe_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_store_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN run_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_render_attempt_analytics_job
    ON render_attempt_analytics(job_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_render_attempt_analytics_run
    ON render_attempt_analytics(run_id, recorded_at);
