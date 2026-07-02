-- PipelineGen migration 121 — execution_steps (resumable step store, §12-3).
--
-- Per PipelineGen stock-cutover §12-3: persistent checkpoint store that
-- records the outcome of every phase in a multi-step pipeline run. The
-- canonical UNIQUE constraint (job_id, step_key, input_fingerprint)
-- gate idempotency-on-replay (a retry with the same fingerprint is a
-- no-op; a retry with a different fingerprint inserts a new row, which
-- acts as a fingerprint-version log for downstream auditing).
--
-- godlike/06 SSOT: this is the canonical owner of the "did phase X
-- complete for job Y" fact. Prior code paths that tracked phase state
-- in scattered local maps are forward-pointer migration targets.
--
-- godlike/07 typed-error contract: the port that owns this table
-- (internal/application/execution/steps.Store) returns typed
-- sentinel errors (ErrExecutionStepStaleFingerprint, etc.) so callers
-- can `errors.Is(err, <sentinel>)` from any seam. The table itself
-- is pure SQLite — typed-error surface lives in Go code, not the
-- migration.

CREATE TABLE IF NOT EXISTS execution_steps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    step_key TEXT NOT NULL,
    input_fingerprint TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempt INTEGER NOT NULL DEFAULT 0,
    result_json TEXT NOT NULL DEFAULT '{}',
    artifact_refs_json TEXT NOT NULL DEFAULT '[]',
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT ''
);

-- godlike/06 SSOT: the canonical dedup surface. Three columns form
-- the identity; a retry with the same triple is a no-op (any of
-- the Mark* methods is idempotent against this surface).
CREATE UNIQUE INDEX IF NOT EXISTS uniq_execution_steps_dedup
    ON execution_steps (job_id, step_key, input_fingerprint);

-- godlike/06 SSOT: the canonical "find first non-completed step"
-- query path. The (job_id, status, step_key) ordering matches the
-- FirstNonCompleted scan: WHERE job_id = ? AND status != 'completed'
-- ORDER BY step_key ASC LIMIT 1.
CREATE INDEX IF NOT EXISTS ix_execution_steps_resume
    ON execution_steps (job_id, status, step_key);

-- godlike/06 SSOT: the canonical "audit all steps for a job" scan
-- path. (job_id, step_key) ASC matches ListByJob's ORDER BY
-- step_key ASC.
CREATE INDEX IF NOT EXISTS ix_execution_steps_audit
    ON execution_steps (job_id, step_key);
