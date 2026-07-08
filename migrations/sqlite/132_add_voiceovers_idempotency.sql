-- Migration 132: Add idempotency support to voiceovers table
-- FASE 3 VO-OPERATIONAL-READINESS (July 2026): true retry-safe idempotency.
--
-- idempotency_key is a caller-derived canonical string (typically
-- jobID:language:textHash) that is stable across retries. The UNIQUE
-- INDEX ensures a retried job cannot create duplicate voiceover rows
-- for the same logical item. Empty string is excluded from the index
-- so legacy rows (pre-migration) continue to coexist.
--
-- job_id is the parent job that produced this voiceover. It is the
-- first component of the idempotency_key and is stored separately for
-- operator-side diagnostics (SELECT by job_id without parsing).

ALTER TABLE voiceovers ADD COLUMN job_id TEXT NOT NULL DEFAULT '';
ALTER TABLE voiceovers ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';

-- Partial unique index: only enforce uniqueness when idempotency_key
-- is non-empty. Legacy rows (empty string) are excluded so the
-- migration is backward-compatible. SQLite 3.8.0+ (bundled with
-- mattn/go-sqlite3 since ~2018) supports partial indexes.
CREATE UNIQUE INDEX IF NOT EXISTS idx_voiceovers_idempotency
    ON voiceovers(idempotency_key) WHERE idempotency_key != '';
