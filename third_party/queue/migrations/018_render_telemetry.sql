-- Raw Chronon job telemetry is durable central provenance. Local worker
-- SQLite and the temporary .timing.json are transport/debug copies only.
CREATE TABLE render_telemetry (
    job_id       TEXT NOT NULL REFERENCES render_jobs (id) ON DELETE CASCADE,
    attempt_id   TEXT,
    schema       TEXT NOT NULL,
    telemetry    JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, attempt_id)
);

CREATE INDEX idx_render_telemetry_job ON render_telemetry (job_id, created_at);
