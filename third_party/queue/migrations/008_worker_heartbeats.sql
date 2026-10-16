-- worker_heartbeats: append-only heartbeat ledger for rendering workers.
--
-- rendering_workers.last_heartbeat_at holds the *current* liveness; this table
-- holds the *history* of every heartbeat so outages can be reconstructed.
CREATE TABLE worker_heartbeats (
    id           BIGSERIAL PRIMARY KEY,
    worker_id    TEXT NOT NULL REFERENCES rendering_workers(id) ON DELETE CASCADE,
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_worker_heartbeats_worker_time
    ON worker_heartbeats (worker_id, heartbeat_at DESC);
