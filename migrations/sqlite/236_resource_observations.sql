-- database: primary
-- Migration 236: sampled host resource observations.
-- Raw samples are persisted once; aggregates remain derived at read-time.

CREATE TABLE IF NOT EXISTS resource_observations (
    observation_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    worker_id TEXT NOT NULL DEFAULT '',
    host TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL,

    cpu_avg_pct REAL,
    cpu_peak_pct REAL,
    rss_avg_bytes INTEGER,
    rss_peak_bytes INTEGER,
    gpu_avg_pct REAL,
    gpu_peak_pct REAL,
    vram_peak_bytes INTEGER,
    encoder_avg_pct REAL,
    temperature_peak_c REAL,
    disk_read_bytes INTEGER,
    disk_write_bytes INTEGER,
    network_rx_bytes INTEGER,
    network_tx_bytes INTEGER,

    metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_resource_observations_run
    ON resource_observations(run_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_resource_observations_job
    ON resource_observations(job_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_resource_observations_host
    ON resource_observations(host, observed_at);
