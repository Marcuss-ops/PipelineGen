-- 057_worker_nodes_and_workflows.sql
-- Canonical worker identity and persisted workflow tables.

CREATE TABLE IF NOT EXISTS worker_nodes (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    status              TEXT NOT NULL,
    session_id          TEXT NOT NULL,
    session_expires_at  TEXT NOT NULL,
    capabilities_json   TEXT NOT NULL,
    version             TEXT NOT NULL,
    hostname            TEXT NOT NULL,
    last_seen_at        TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_worker_nodes_status ON worker_nodes(status);
CREATE INDEX IF NOT EXISTS idx_worker_nodes_session ON worker_nodes(session_id);

CREATE TABLE IF NOT EXISTS workflows (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    version         INTEGER NOT NULL,
    status          TEXT NOT NULL,
    correlation_id  TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL DEFAULT '',
    input_json      TEXT NOT NULL,
    output_json     TEXT NOT NULL DEFAULT '{}',
    error_code      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    revision        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    started_at      TEXT,
    completed_at    TEXT,
    cancelled_at    TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS workflows_idempotency_idx
    ON workflows(type, idempotency_key)
    WHERE idempotency_key != '';

CREATE TABLE IF NOT EXISTS workflow_steps (
    id              TEXT PRIMARY KEY,
    workflow_id     TEXT NOT NULL,
    step_key        TEXT NOT NULL,
    step_type       TEXT NOT NULL,
    status          TEXT NOT NULL,
    position        INTEGER NOT NULL,
    job_id          TEXT,
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 3,
    input_json      TEXT NOT NULL DEFAULT '{}',
    output_json     TEXT NOT NULL DEFAULT '{}',
    error_code      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    available_at    TEXT,
    started_at      TEXT,
    completed_at    TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,

    FOREIGN KEY(workflow_id) REFERENCES workflows(id),
    FOREIGN KEY(job_id) REFERENCES jobs(id),
    UNIQUE(workflow_id, step_key)
);

CREATE TABLE IF NOT EXISTS workflow_step_dependencies (
    workflow_id        TEXT NOT NULL,
    step_id            TEXT NOT NULL,
    depends_on_step_id TEXT NOT NULL,

    PRIMARY KEY(step_id, depends_on_step_id),
    FOREIGN KEY(workflow_id) REFERENCES workflows(id),
    FOREIGN KEY(step_id) REFERENCES workflow_steps(id),
    FOREIGN KEY(depends_on_step_id) REFERENCES workflow_steps(id)
);
