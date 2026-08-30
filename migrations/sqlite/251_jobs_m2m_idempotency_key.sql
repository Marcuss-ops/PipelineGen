-- 251_jobs_m2m_idempotency_key.sql
--
-- PG-M2M (Aug 2026): make idempotency_key a persistent part of the job
-- with a UNIQUE constraint on (client_id, idempotency_key) so a remote
-- submitter (PipelineGen / Agent / second PC) that retries a POST after
-- a network drop gets the SAME job_id back instead of a duplicate.
--
-- This is distinct from the existing (type, correlation_id) UNIQUE index
-- (migration 036): correlation_id is derived from the X-Request-ID
-- header / request context and is NOT caller-controlled per-client.
-- The M2M surface needs a caller-controlled, per-client idempotency_key
-- so two different clients can legitimately reuse the same key string
-- ("matt-damon-40-en-001") without colliding — the (client_id,
-- idempotency_key) pair is the canonical dedup key for the M2M surface.
--
-- Why a conditional UNIQUE index (WHERE both != '') instead of a plain
-- UNIQUE: the jobs table is shared by the admin surface (POST /api/jobs
-- with no client_id / idempotency_key) and the M2M surface. Admin and
-- internal fan-out enqueues do not set these fields; a plain UNIQUE
-- would force every non-M2M enqueue to supply a synthetic key. The
-- conditional index mirrors the existing idx_jobs_type_correlation
-- pattern (WHERE correlation_id != '') so non-M2M jobs stay
-- unique-by-id only.
--
-- Idempotent: ADD COLUMN + CREATE INDEX IF NOT EXISTS. Safe to re-run.
-- SQLite does not support ADD COLUMN IF NOT EXISTS, so the migration is
-- single-use (consistent with 021, 053, 100, 132, 161).

-- ─── Column additions ──────────────────────────────────────────────────
-- client_id is the M2M client identifier resolved from the Bearer
-- VELOX_M2M_SECRET by JobClientAuthMiddleware (the non-secret
-- projection stored in the gin context). Empty for admin/internal
-- enqueues. NOT NULL DEFAULT '' so existing rows + non-M2M enqueues
-- stay valid.
ALTER TABLE jobs ADD COLUMN client_id TEXT NOT NULL DEFAULT '';

-- idempotency_key is the caller-controlled dedup key for the M2M
-- surface (e.g. "matt-damon-40-en-001"). Empty for admin/internal
-- enqueues. NOT NULL DEFAULT '' so the conditional UNIQUE index can
-- exclude them.
ALTER TABLE jobs ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';

-- ─── Conditional UNIQUE index ──────────────────────────────────────────
-- Only enforce uniqueness when BOTH client_id and idempotency_key are
-- non-empty. A partial (client_id set, idempotency_key empty) or
-- (client_id empty, idempotency_key set) combination is NOT unique —
-- those are treated as non-idempotent enqueues (the caller did not
-- supply a full dedup pair). The rescue path in Service.Enqueue probes
-- this index via FindByClientAndIdempotencyKey on UNIQUE-constraint
-- collision.
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_client_idempotency
    ON jobs(client_id, idempotency_key)
    WHERE client_id != '' AND idempotency_key != '';

-- ─── Lookup index (non-unique) ─────────────────────────────────────────
-- The pre-check path in Service.Enqueue (before the INSERT) uses
-- FindByClientAndIdempotencyKey which queries by the pair. The UNIQUE
-- index above already covers that lookup, so no separate non-unique
-- index is needed — the UNIQUE index serves both dedup + lookup.

-- ─── Audit verification queries (operators run ad-hoc, not migration) ─
-- PRAGMA table_info(jobs);
-- SELECT client_id, COUNT(*) FROM jobs WHERE client_id != '' GROUP BY client_id;
-- SELECT client_id, idempotency_key, id FROM jobs WHERE client_id != '' LIMIT 10;
