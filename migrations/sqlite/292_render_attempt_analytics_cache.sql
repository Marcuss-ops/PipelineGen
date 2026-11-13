-- database: cache
-- Migration 292: restore/upgrade render_attempt_analytics in the cache database.
--
-- The cache database's consolidated baseline was applied from a cache-only
-- schema dump, while `000_baseline_267.sql` is now the full `database: all`
-- baseline. Its ledger row (version 0) therefore covers a file that declares
-- render_attempt_analytics, but the table itself was never created in cache.
-- The cache-plane rebuild (274) intentionally recreates cache-owned tables
-- only, so the schema-contract verifier reports the table as drift on every
-- startup. This is the NEW numbered migration the verifier's remedy asks for.
--
-- The CREATE statement is the 215+227 baseline shape, because that is what a
-- restored cache database already has. Every later column is added with ALTER
-- below; the runner soft-skips a duplicate ADD COLUMN, so the same file both
-- upgrades an old table and no-ops on a database already at the current shape.
-- It must NOT rely on CREATE TABLE IF NOT EXISTS to add later columns: on the
-- database this migration repairs the table already exists, and an index on a
-- missing run_id then fails the whole migration.
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

-- 271: per-overlay tracing and the worker-profile facts.
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

-- 275: numeric content census.
ALTER TABLE render_attempt_analytics ADD COLUMN number_count INTEGER NOT NULL DEFAULT 0;

-- 276: queue/wait observation.
ALTER TABLE render_attempt_analytics ADD COLUMN submit_started_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN submit_accepted_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN wait_started_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN wait_finished_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN queue_queued_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN queue_started_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN queue_completed_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN artifact_available_at TEXT;
ALTER TABLE render_attempt_analytics ADD COLUMN outcome TEXT NOT NULL DEFAULT '';

-- 279: batch identity.
ALTER TABLE render_attempt_analytics ADD COLUMN batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE render_attempt_analytics ADD COLUMN batch_item_ids_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE render_attempt_analytics ADD COLUMN batch_start_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN batch_end_us INTEGER NOT NULL DEFAULT 0;

-- 280: cache timings.
ALTER TABLE render_attempt_analytics ADD COLUMN cache_lookup_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_probe_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_store_us INTEGER NOT NULL DEFAULT 0;
ALTER TABLE render_attempt_analytics ADD COLUMN cache_outcome TEXT NOT NULL DEFAULT '';

-- 287: explicit run correlation, backfilled with the same derivation.
ALTER TABLE render_attempt_analytics ADD COLUMN run_id TEXT NOT NULL DEFAULT '';

UPDATE render_attempt_analytics
   SET run_id = CASE
        WHEN instr(job_id, ':item:') > 1 THEN substr(job_id, 1, instr(job_id, ':item:') - 1)
        WHEN substr(job_id, 1, 4) = 'run_' THEN job_id
        ELSE run_id
       END
 WHERE run_id = ''
   AND (instr(job_id, ':item:') > 1 OR substr(job_id, 1, 4) = 'run_');

CREATE INDEX IF NOT EXISTS idx_render_attempt_analytics_job
    ON render_attempt_analytics(job_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_render_attempt_analytics_run
    ON render_attempt_analytics(run_id, recorded_at);
