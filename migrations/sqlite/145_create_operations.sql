-- 145_create_operations.sql
--
-- FASE 2 (July 2026): introduces the canonical `operations` table backing
-- the new application-layer `GenerationSubmissionService` use case
-- (internal/application/operations/generation_submission_service.go).
--
-- Replaces the script.generate-side Idempotency-Key + cached-response
-- pattern in handler_enqueue.go::enqueueEnvelopeFn (the pre-FASE-2
-- `store.TryInsert` + `store.Complete` 24h-replay dance) with an
-- authoritative per-request operation record. The HTTP 202 response
-- is no longer cached; clients re-fetch via `GET /api/jobs/{job_id}/full`
-- for the canonical job snapshot.
--
-- Columns (per FASE 2 user-spec, byte-exact):
--   operation_id            TEXT PRIMARY KEY
--   scope                   TEXT NOT NULL
--   idempotency_key         TEXT NOT NULL
--   request_hash            TEXT NOT NULL
--   job_id                  TEXT NOT NULL
--   state                   TEXT NOT NULL
--   created_at              TEXT NOT NULL
--   updated_at              TEXT NOT NULL
--   supersedes_operation_id TEXT NOT NULL DEFAULT ''
--
-- Why NO UNIQUE INDEX on (scope, idempotency_key, request_hash):
--   `force_refresh` legitimately creates a new operation with the
--   SAME (scope, key, hash) triple while pointing supersedes_*
--   at the prior operation. A UNIQUE index would 409 the
--   force_refresh path. Concurrent-insert safety is provided by
--   the application-layer `BEGIN IMMEDIATE` transaction
--   (canonical godlike/07 fail-fast-at-input contract: serialise
--   on the connection, not on a database-level UNIQUE constraint
--   that would mis-attribute force_refresh as a key collision).
--
-- Lookup index: composite (scope, idempotency_key, created_at DESC)
-- is the canonical query shape for "most recent operation for a
-- (scope, key) pair" — used by the idempotency-hit path AND the
-- force_refresh-supersedes path. The DESC suffix lets SQLite use
-- the index for `ORDER BY created_at DESC LIMIT 1` without a sort.
--
-- supersedes_operation_id semantics:
--   - The NEW operation writes supersedes_operation_id = <old.operation_id>
--     (one-way: the new one knows whom it supersedes).
--   - The OLD operation's `state` is updated to 'SUPERSEDED' in the
--     same atomic TX (the symmetric signal: "this one was superseded
--     by someone" comes from querying for rows with state='SUPERSEDED'
--     in the (scope, key) bucket, NOT from a superseded_by_* column).
--   - The old `job_id` is left in place — supersession is a logical
--     operation-replacement, NOT a job cancellation. The old job
--     continues to run to terminal state; clients watching it
--     can still poll its status.
--
-- godlike/06 SSOT: this table is the SOLE canonical owner of the
-- per-request operation record. Cross-package drift tests were
-- DROPPED per godlike/07 minimum-blast-radius (see
-- repository_lifecycle_dualwrite_test.go header for the explicit
-- rationale — same precedent applies here).
--
-- godlike/07 fail-closed: state is a free-form TEXT column; the
-- application layer (`internal/domain/operations/types.go`)
-- owns the typed `State` enum. The repository validates values
-- in the `internal/domain/operations.ValidState` set before
-- accepting them, returning `ErrInvalidOperationState` for any
-- out-of-set value (godlike/07 NO-FAKE-AVAILABILITY — a bogus
-- state from a misconfigured caller is rejected, not silently
-- accepted).
--
-- Idempotent: IF NOT EXISTS everywhere. Re-applying on a database
-- that has the table from ad-hoc bootstrapping is a no-op.
-- Verified after migration by `PRAGMA table_info(operations)`
-- matching the INSERT projection in Repository.Insert.

CREATE TABLE IF NOT EXISTS operations (
    operation_id            TEXT PRIMARY KEY,
    scope                   TEXT NOT NULL,
    idempotency_key         TEXT NOT NULL,
    request_hash            TEXT NOT NULL,
    job_id                  TEXT NOT NULL,
    state                   TEXT NOT NULL,
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    supersedes_operation_id TEXT NOT NULL DEFAULT ''
);

-- Canonical lookup: "most recent operation for a (scope, key) pair".
-- ORDER BY created_at DESC LIMIT 1 uses this index directly.
CREATE INDEX IF NOT EXISTS idx_operations_idem_lookup
    ON operations(scope, idempotency_key, created_at DESC);

-- Operator-dashboard index: filter by state (e.g. show all SUPERSEDED
-- in the last 24h, all FAILED in the last 7d). Separate from the
-- lookup index because the cardinalities are different.
CREATE INDEX IF NOT EXISTS idx_operations_state_created
    ON operations(state, created_at DESC);
