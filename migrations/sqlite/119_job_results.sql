-- Migration 119: job_results table (P0 Commit 7, July 2026).
--
-- The Sender-side atomic CompleteJob (internal/application/jobs/completion/
-- complete_job_service.go) persists the canonical result payload keyed
-- on (job_id, attempt, result_hash). The UNIQUE INDEX on the same triple
-- is the load-bearing surface for ON CONFLICT (job_id, attempt,
-- result_hash) DO NOTHING -- repeated complete calls with the same
-- (jobID, attempt, resultHash) collapse to a single row at the SQLite
-- level (godlike/07 no-fake-availability; dedup is the source of truth).
--
-- godlike/06 one canonical owner per fact: the job result persistence
-- lives here, NOT on the core jobs.result_json column. The core table
-- stays narrow (job identity + lifecycle + audit metadata). The result
-- payload is isolated to keep large JSON payloads out of bulk-claim
-- scans (ClaimNext / List / RefreshMetrics all read jobs.* and would
-- tail-spike on a large result_json).
--
-- Schema rationale (godlike/06 SSOT):
--   - id INTEGER PRIMARY KEY AUTOINCREMENT: canonical SQLite rowid;
--     reserved for joins from future audit tables.
--   - job_id TEXT NOT NULL: references jobs(id) ON DELETE CASCADE.
--     Index ix_job_results_job_id anchors per-job lookups.
--   - attempt INTEGER NOT NULL: matches jobs.retry_count lifecycle.
--     The (job_id, attempt) pair addresses the specific attempt's result.
--   - result_hash TEXT NOT NULL: hex-encoded SHA-256 of result_payload,
--     produced by remote.CompleteJobIdempotencyKey (C7) / canonical
--     helper. 64-char lowercase hex; case-insensitive validator in
--     domain/remote/complete_job_idempotency.go::IsValidCompleteJobIdempotencyKey.
--   - codec_id TEXT NOT NULL: the canonical ResultCodec ID from the
--     compiled registry (C2 surface: def.ResultCodec.CodecID()). This
--     anchors the wire-format discriminator per codec install.
--   - result_payload TEXT NOT NULL: the encoded json.RawMessage; the
--     canonical on-disk form (no envelope wrapper, no encoding colon).
--   - created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')):
--     RFC3339Nano UTC for canonical ordering + ON CONFLICT detection
--     of stale-vs-fresh write race (advisory only; the UNIQUE
--     constraint is the authoritative gate).
--
-- Migration is idempotent: CREATE TABLE IF NOT EXISTS + CREATE INDEX
-- IF NOT EXISTS so reruns on already-migrated databases no-op.

CREATE TABLE IF NOT EXISTS job_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    result_hash TEXT NOT NULL DEFAULT '',
    codec_id TEXT NOT NULL DEFAULT '',
    result_payload TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

-- Canonical idempotency-key dedup surface (C7). Repeated CompleteJob
-- calls with the same (jobID, attempt, resultHash) produce the same
-- key; ON CONFLICT (job_id, attempt, result_hash) DO NOTHING collapses
-- to a single row. The adapter (internal/infrastructure/ ... )
-- implements INSERT ... ON CONFLICT (job_id, attempt, result_hash)
-- DO NOTHING RETURNING id so a repeated call returns the original
-- row's id WITHOUT re-emitting outbox events.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_job_results_dedup
    ON job_results (job_id, attempt, result_hash);

-- Per-job scan (audit + reconciliation lookups). NOT a UNIQUE
-- index -- a single job may have N attempts (each with its own
-- result_hash).
CREATE INDEX IF NOT EXISTS ix_job_results_job_id
    ON job_results (job_id, attempt DESC);
