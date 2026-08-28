-- database: observability
-- Raw samples are short-lived; per-run aggregates and the canonical envelope
-- are long-lived. observed_at remains the sole sample time source.

CREATE TABLE IF NOT EXISTS run_resource_aggregates (
    run_id          TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL,
    attempt_id      TEXT NOT NULL UNIQUE,
    schema_version  INTEGER NOT NULL,
    sample_count    INTEGER NOT NULL DEFAULT 0,
    first_observed_at TEXT,
    last_observed_at  TEXT,
    aggregate_json  TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_run_resource_samples_retention
    ON run_resource_samples(observed_at);
CREATE INDEX IF NOT EXISTS idx_run_resource_aggregates_retention
    ON run_resource_aggregates(updated_at);
