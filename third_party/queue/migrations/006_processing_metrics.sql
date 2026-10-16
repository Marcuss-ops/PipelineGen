-- processing_metrics: job-correlated timing and resource metrics.
--
-- Prometheus keeps realtime/aggregate operational metrics; this table keeps
-- the per-job numbers that answer "how long did exactly this job take?".
--
-- metric_name is the worker's declared vocabulary (renderinggen/internal/
-- metricnames): phase timings carry the unit in their name (overlay_compile_us,
-- asset_materialize_us, render_us, sha256_us, objectstore_upload_us,
-- drive_upload_us, total_us, render_ms, render_frames_done/total, render_fps)
-- and the unit column is derived from that suffix by metricUnit(). The comment
-- used to advertise queue_wait_ms/frames_rendered, names no writer has ever
-- produced.
CREATE TABLE processing_metrics (
    id           BIGSERIAL PRIMARY KEY,
    job_id       TEXT NOT NULL REFERENCES render_jobs (id) ON DELETE CASCADE,
    attempt_id   TEXT,
    metric_name  TEXT NOT NULL,
    metric_value DOUBLE PRECISION NOT NULL,
    unit         TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
