-- migrations/sqlite/110_qdrantprojection_checkpoints.sql
--
-- TODO #8 (June 2026) — renumbered from 105_qdrantprojection_checkpoints.sql
-- to avoid collision with 105_asset_versions.sql. The original 105 plan
-- was renumbered out of the way (avoiding the existing
-- 105_asset_versions.sql); this file landed at 110 instead. The header
-- now reflects the CURRENT filename so a future grep or git blame
-- matches reality.
-- PR 8 (June 2026) — feat/qdrant-reindex-v2.
-- Verdict Qdrant section #15: the previous ReindexAll loaded all
-- media_assets.id into memory + did an in-process Cursor loop.
-- Stopping mid-run (kill -9, OOM, deployment restart, network
-- partition) meant the entire progress was lost — restart from 0.
-- This migration introduces the canonical resume machinery:
--   - qdrantprojection_checkpoints:  one row per reindex JOB,
--     updated atomically as each batch commits to Qdrant.
--   - qdrantprojection_dlq:         one row per VALIDATION failure
--     (e.g. missing asset_id, dimension mismatch, content_hash
--     absent). The document is NOT silently dropped — it lands in
--     the DLQ so operators can inspect at /admin/qdrant/dlq/list.
--
-- Migration is additive only. No renames, no column drops. Both
-- tables are scope-local (no FK to qdrant_collections because
-- collection_name is a string in the projection world).
--
-- NOTE on numbering: the prior migration `103_qdrant_collections.sql`
-- shares its number with the pre-existing
-- `103_create_voiceovers_table.sql` — that collision is a hygiene
-- follow-up, NOT caused by this PR. PR 8 uses 105 to avoid piling onto
-- the precedent; an operator hygiene PR can renumber deterministically.

CREATE TABLE IF NOT EXISTS qdrantprojection_checkpoints (
    -- job_id: caller-supplied UUID-string. Unique per job so multiple
    --   concurrent reindex jobs (e.g. parallel admin runs) cannot
    --   collide on the same checkpoint row.
    job_id              TEXT PRIMARY KEY,

    -- target_collection: the physical Qdrant collection the run is
    --   writing into. Recorded so a resume can verify operator intent
    --   (parking on a different target collateral-destroys intent).
    target_collection   TEXT NOT NULL,

    -- last_indexed_id: media_assets.id (string) of the LAST row whose
    --   Qdrant upsert was ACKNOWLEDGED by the Qdrant REST API (NOT
    --   queued in the client caller). The keyset pagination cursor
    --   on resume is `SELECT ... WHERE id > last_indexed_id ORDER BY id LIMIT ?`.
    last_indexed_id     TEXT NOT NULL DEFAULT '',

    -- indexed_count: cumulative count of rows whose Qdrant upsert was
    --   ACKNOWLEDGED. Mirrors the IndexedAssets field of ReindexResult.
    indexed_count       INTEGER NOT NULL DEFAULT 0,

    -- error_count: cumulative count of rows that failed validation
    --   AND landed in qdrantprojection_dlq. The sum of (indexed_count
    --   + error_count + (rows still in flight at checkpoint time))
    --   equals the number of SELECT rows processed since job start.
    error_count         INTEGER NOT NULL DEFAULT 0,

    -- skipped_count: cumulative count of rows that did not match the
    --   filter (e.g. lifecycle_state NOT IN eligible). Logged-only.
    skipped_count       INTEGER NOT NULL DEFAULT 0,

    -- started_at: ISO8601 UTC of the FIRST batch that wrote this row
    --   (resume restarts the started_at to the original timestamp,
    --   NOT the resume time — operators reading the metric can
    --   reason on "since job start").
    started_at          TEXT NOT NULL,

    -- finished_at: ISO8601 UTC of the LAST COMMIT. Empty while the
    --   job is still in flight or if it crashed mid-batch without
    --   a clean shutdown. Resume picks up WHERE last_indexed_id left
    --   off — finished_at is informational, not load-bearing for
    --   resume logic.
    finished_at         TEXT,

    -- last_batch_at: ISO8601 UTC of the most recent batch commit.
    --   Distinguishes "started but never progressed" (last_batch_at
    --   = started_at) from "actively running" (last_batch_at recent).
    last_batch_at       TEXT,

    -- status: lifecycle phase. Bounded values:
    --   'running'    — at least one batch committed, more pending
    --   'succeeded'  — terminal: AllBatchesCommitted reached
    --   'failed'     — terminal: unrecoverable error (DLQ exhausted,
    --                   retried past MaxAttempts, etc.)
    --   'abandoned'  — terminal: operator manual discard
    -- The five value set matches the lifecycle separation forced by
    -- PR 8 (the previous 'failed' meaning "ran but produced errors"
    -- overlapped with non-terminal running); the new vocabulary
    -- makes dashboards trivially distinguishable.
    status              TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running','succeeded','failed','abandoned')),

    -- updated_at: last mutation timestamp. The checkpoint loop bumps
    -- this on every batch (UPDATE qdrantprojection_checkpoints SET
    -- ... updated_at = datetime('now')). Operators alert on
    -- `now() - updated_at > N minutes` AND `status='running'` to
    -- catch "stuck" jobs that didn't transition to a terminal status.
    updated_at          TEXT NOT NULL,

    -- last_error: the most recent transient error message (kept
    -- short — operators should consult the application log for the
    -- full stack). Empty when last_batch succeeded.
    last_error          TEXT NOT NULL DEFAULT ''
);

-- Hot path: resume reads the latest checkpoint by job_id (PK lookup),
-- already covered by the cluster index. The additional composite
-- covers "all running jobs ordered by last_batch_at DESC" — the
-- catch-stuck-jobs dashboard query above.
CREATE INDEX IF NOT EXISTS idx_qdrantprojection_checkpoints_status_lastbatch
    ON qdrantprojection_checkpoints (status, last_batch_at);

-- DLQ: per-validation-failure row. Operators query this via the
-- admin CLI (`admin dr-qdrant list-dlq --job=<id>`) per the runbook
-- PR 9 documented.
CREATE TABLE IF NOT EXISTS qdrantprojection_dlq (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,

    -- job_id: the reindex job this failure belongs to. Indexed for
    --   the canonical "show me all failures of job X" admin query.
    job_id              TEXT NOT NULL,

    -- asset_id: media_assets.id that failed validation. Empty when
    --   the failure is pre-row (e.g. schema-level vec dim mismatch
    --   on the WHOLE batches's payload).
    asset_id            TEXT NOT NULL DEFAULT '',

    -- reason_category: bounded failure taxonomy. Mirrors a partial
    --   subset of the validation failures enumerated in PR 8 §7:
    --   'embedding_obsolete' | 'content_hash_missing' |
    --   'dimension_mismatch' | 'payload_invalid' | 'other'.
    -- Operators filter dashboards per-category. Adding categories
    -- requires a SQL ALTER + CHECK constraint update — out of scope
    -- for PR 8 (the migration CHECK lists the current 5).
    reason_category     TEXT NOT NULL DEFAULT 'other'
        CHECK (reason_category IN ('embedding_obsolete','content_hash_missing','dimension_mismatch','payload_invalid','other')),

    -- last_error: human-readable failure detail. Truncated to TEXT
    --   column default cap; operators consult log for full message.
    last_error          TEXT NOT NULL DEFAULT '',

    -- observed_at: ISO8601 UTC when the failure was first observed.
    observed_at         TEXT NOT NULL,

    -- resolved_at: ISO8601 UTC when an operator cleared the row
    --   (manual triage). Empty while unresolved.
    resolved_at         TEXT,

    -- resolved_by: free-form operator identifier (text). Empty while
    --   unresolved. Audit-trail hygiene — operators clear via the
    --   admin CLI which stamps this column.
    resolved_by         TEXT NOT NULL DEFAULT ''
);

-- Hot paths:
--  1. "List failures of job X" (admin CLI): covered by idx_dlq_job.
--  2. "All unresolved failures" (dashboard): the resolved_at-NULL
--     partial index would be ideal but SQLite does not support
--     partial indexes with WHERE; use a full composite sorted so
--     dashboards can filter cheaply.
CREATE INDEX IF NOT EXISTS idx_qdrantprojection_dlq_job_observed
    ON qdrantprojection_dlq (job_id, observed_at);

CREATE INDEX IF NOT EXISTS idx_qdrantprojection_dlq_resolved_observed
    ON qdrantprojection_dlq (resolved_at, observed_at);

CREATE INDEX IF NOT EXISTS idx_qdrantprojection_dlq_category
    ON qdrantprojection_dlq (reason_category);
