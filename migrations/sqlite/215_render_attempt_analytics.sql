-- database: primary
-- Migration 215: canonical render-attempt analytics.
--
-- One row per Chronon render attempt produced through the RenderingGen
-- queue. The record is a pure projection of the semantic OverlayPlan
-- (content census) plus the certified queue artifact (render/encode
-- durations, output metrics, SHA-256, Drive identity). It is durable
-- analytics history — Prometheus and dashboards remain derived views.
--
-- attempt_id is the idempotency key: re-recording the same attempt
-- converges on the same row (ON CONFLICT upsert), never appends a
-- duplicate. It is the queue job id for the single-attempt enqueue path
-- (EnqueueChrononPlan uses plan id == job id as its idempotency key).

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
);
CREATE INDEX IF NOT EXISTS idx_render_attempt_analytics_job ON render_attempt_analytics(job_id, recorded_at);
