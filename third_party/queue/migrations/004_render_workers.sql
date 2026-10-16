-- rendering_workers: registry of workers that claim jobs from the queue.
--
-- last_heartbeat_at doubles as the heartbeat ledger: a worker that stops
-- heartbeating can be drained and its running jobs requeued.
CREATE TABLE rendering_workers (
    id                     TEXT PRIMARY KEY,
    hostname               TEXT,
    status                 TEXT NOT NULL DEFAULT 'unknown',
    renderinggen_version   TEXT,
    chronon_version        TEXT,
    overlay_schema_version INTEGER,
    gpu_backend            TEXT,
    gpu_device             TEXT,
    gpu_driver             TEXT,
    started_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_heartbeat_at      TIMESTAMPTZ
);

ALTER TABLE rendering_workers
    ADD CONSTRAINT rendering_workers_status_check
    CHECK (status IN ('unknown', 'ready', 'busy', 'draining', 'offline'));
