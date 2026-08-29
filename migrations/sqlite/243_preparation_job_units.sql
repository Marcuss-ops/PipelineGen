-- 243_preparation_job_units.sql
-- Durable job→unit mapping: which prepared-unit fingerprints each job depends
-- on, whether the unit is required, and when the job adopted the prepared
-- result at execution time. Two jobs sharing a fingerprint share one row in
-- preparation_units (cross-job singleflight); this table is the per-job view.
-- database: primary

CREATE TABLE IF NOT EXISTS preparation_job_units (
    job_id       TEXT NOT NULL,
    unit_id      TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,

    required     INTEGER NOT NULL DEFAULT 1,
    adopted      INTEGER NOT NULL DEFAULT 0,

    queue_rank   INTEGER,

    planned_at   TEXT NOT NULL,
    adopted_at   TEXT,

    PRIMARY KEY (job_id, unit_id)
);

CREATE INDEX IF NOT EXISTS idx_preparation_job_units_fingerprint
    ON preparation_job_units(fingerprint);
CREATE INDEX IF NOT EXISTS idx_preparation_job_units_job_adopted
    ON preparation_job_units(job_id, adopted);