-- 114_youtube_discoveries_v2.sql
--
-- PR-C-YouTube-Cutover Commit 3/6 (June 2026, P1 #5 + #6 + #7):
-- retryable ledger + cycle-end watermark + policy_version gate.
--
-- This migration REPLACES the existing `youtube_discoveries` table
-- (created in 113) with a v2 schema that supports:
--
--   1. A typed state machine (pending → enqueued | rejected_retryable
--      → rejected_terminal | completed) replacing the old
--      outcome TEXT + enqueued INTEGER pair. The state machine is
--      the source of truth; outcome is preserved as a back-compat
--      shadow column for downstream analytics.
--
--   2. A retryable sub-state: rejected_retryable rows carry
--      next_retry_at + attempt_count. The channel monitor's
--      TryReserve gate becomes the canonical retry trigger:
--      if state='rejected_retryable' AND next_retry_at <= now,
--      TryReserve RE-INSERTS a fresh row (new attempt_count) so
--      the per-video workflow re-runs without leaking a duplicate
--      job to the broker.
--
--   3. policy_version: every ledger row carries the policy_version
--      active at insert time. UNIQUE(channel_id, video_id,
--      policy_version) replaces UNIQUE(channel_id, video_id) so a
--      policy_version bump (e.g. transcript segmentation logic v2)
--      naturally produces a fresh ledger row alongside the
--      historical v1 audit row. The cross-process race that once
--      collapsed two distinct policies into one winner is now
--      PREVENTED at the SQL level.
--
--   4. Lease plumbing: lease_owner + lease_until are surfaced at
--      the row level so a separate future commit can wire external
--      dispatchers (multi-instance channel monitor fanout) onto
--      the same gate. future commits may also wire job_id +
--      last_error onto this surface for back-compat.
--
-- Strategy: clean break via table swap. The old schema is
-- recreated as youtube_discoveries_v1, the rows are mapped into
-- youtube_discoveries_v2 with the new state derived from (enqueued,
-- outcome) of the old row, then the old table is dropped and the
-- new one renamed. This preserves the original 113 lifecycle
-- column set as shadow columns in v2 for back-compat with any
-- downstream analytics that read outcome='enqueued' literals.
--
-- Downgrade (rollback) is symmetric: rebuild youtube_discoveries_v1
-- from the v2 rows by mapping state → (enqueued, outcome) and
-- dropping the v2-only columns. The DOwngrade block is documented
-- at the bottom of this file; apply it ONLY when rolling back the
-- 114 migration.
--
-- Index strategy:
--   - PK + UNIQUE(channel_id, video_id, policy_version) gates the
--     TryReserve leader-election.
--   - idx_youtube_discoveries_retry ON (next_retry_at) WHERE
--     state='rejected_retryable' makes the retry-eligibility scan
--     O(1) — the channel monitor's scheduler queries this every
--     cycle to gate which rejected rows are ready to re-enter
--     TryReserve.
--   - idx_youtube_discoveries_lease ON (lease_until) WHERE state IN
--     ('pending','analyzing') makes the lease-expiry reclaim
--     lookup O(1) — same index, different filter.

-- ─────────────────────────────────────────────────────────────────
-- UPGRADE: 113 (v1) → 114 (v2)
-- ─────────────────────────────────────────────────────────────────

CREATE TABLE youtube_discoveries_v2 (
    id                TEXT PRIMARY KEY,
    channel_id        TEXT NOT NULL,
    video_id          TEXT NOT NULL,
    policy_version    TEXT NOT NULL DEFAULT 'v1',
    state             TEXT NOT NULL DEFAULT 'pending',
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    discovered_at     TEXT NOT NULL DEFAULT (datetime('now')),
    enqueued_at       TEXT,
    next_retry_at     TEXT,
    lease_owner       TEXT,
    lease_until       TEXT,
    job_id            TEXT,
    last_error        TEXT,
    source_url        TEXT,
    title             TEXT,
    -- back-compat shadow: derives from state per the lookup below.
    -- Preserved so any downstream analytics reading
    -- outcome='enqueued' literals still works.
    outcome           TEXT NOT NULL DEFAULT 'pending',
    rejection_reason  TEXT,
    updated_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel_id, video_id, policy_version)
);

-- Map v1 → v2: each v1 row produces one v2 row.
--   - enqueued=1 + outcome='enqueued'  → state='enqueued'
--   - enqueued=0 + outcome='rejected'  → state='rejected_terminal'
--   - enqueued=0 + outcome='pending'   → state='pending'
--   - enqueued=0 + outcome=anything-else → state='completed' (legacy)
-- The state column carries the new ground truth; outcome is the
-- legacy shadow (set to match state for back-compat readers).
INSERT INTO youtube_discoveries_v2 (
    id, channel_id, video_id, policy_version, state,
    attempt_count, discovered_at, enqueued_at,
    source_url, title, outcome, rejection_reason,
    updated_at
)
SELECT
    id, channel_id, video_id, 'v1' AS policy_version,
    CASE
        WHEN enqueued = 1                  THEN 'enqueued'
        WHEN outcome = 'rejected'          THEN 'rejected_terminal'
        WHEN outcome = 'pending'           THEN 'pending'
        ELSE 'completed'
    END AS state,
    0 AS attempt_count,
    discovered_at,
    enqueued_at,
    source_url,
    title,
    -- Legacy outcome column shadow for back-compat.
    CASE
        WHEN enqueued = 1                  THEN 'enqueued'
        WHEN outcome = 'rejected'          THEN 'rejected'
        ELSE outcome
    END AS outcome,
    rejection_reason,
    -- updated_at: pre-Commit-3/6 rows have no separate updated_at
    -- column; use enqueued_at when present, else discovered_at, so
    -- the v2 watermark row is never a zero timestamp.
    COALESCE(enqueued_at, discovered_at) AS updated_at
FROM youtube_discoveries;

DROP TABLE youtube_discoveries;

ALTER TABLE youtube_discoveries_v2 RENAME TO youtube_discoveries;

-- ── Indices on the new schema ─────────────────────────────────────

-- Existing index from 113: keeps the cycle-end MAX(discovered_at)
-- scan fast (the watermark derivation moved to a state-filtered
-- query in the repository; this index still serves the
-- state-filtered version).
CREATE INDEX IF NOT EXISTS idx_youtube_discoveries_watermark
    ON youtube_discoveries(channel_id, discovered_at DESC);

-- New: retry-eligibility scan.
-- The channel monitor queries
--   SELECT id, attempt_count FROM youtube_discoveries
--   WHERE channel_id = ? AND state = 'rejected_retryable'
--     AND next_retry_at IS NOT NULL AND next_retry_at <= ?
-- every cycle to gate which retryable rows are eligible to
-- re-enter TryReserve. Partial index keeps it small.
CREATE INDEX IF NOT EXISTS idx_youtube_discoveries_retry
    ON youtube_discoveries(next_retry_at)
    WHERE state = 'rejected_retryable';

-- New: lease-expiry reclaim.
-- A future commit (multi-instance dispatcher) will query
--   SELECT id FROM youtube_discoveries
--   WHERE state IN ('pending','analyzing')
--     AND lease_owner = ? AND lease_until IS NOT NULL
--     AND lease_until <= ?
-- to reclaim stale analysis leases. Partial index, same shape
-- as the retry index.
CREATE INDEX IF NOT EXISTS idx_youtube_discoveries_lease
    ON youtube_discoveries(lease_until)
    WHERE state IN ('pending', 'analyzing');

-- ─────────────────────────────────────────────────────────────────
-- DOWNGRADE (ROLLBACK): 114 (v2) → 113 (v1)
-- Apply this block ONLY when rolling back the 114 migration.
-- Wrapped in a comment so a forward migration runner doesn't
-- execute it.
-- ─────────────────────────────────────────────────────────────────
-- CREATE TABLE youtube_discoveries_v1 (
--     id                TEXT PRIMARY KEY,
--     channel_id        TEXT NOT NULL,
--     video_id          TEXT NOT NULL,
--     discovered_at     TEXT NOT NULL DEFAULT (datetime('now')),
--     enqueued          INTEGER NOT NULL DEFAULT 0,
--     enqueued_at       TEXT,
--     source_url        TEXT,
--     title             TEXT,
--     outcome           TEXT NOT NULL DEFAULT 'pending',
--     rejection_reason  TEXT,
--     UNIQUE(channel_id, video_id)
-- );
-- INSERT INTO youtube_discoveries_v1 (
--     id, channel_id, video_id, discovered_at,
--     enqueued, enqueued_at, source_url, title,
--     outcome, rejection_reason
-- )
-- SELECT
--     id, channel_id, video_id, discovered_at,
--     CASE WHEN state = 'enqueued' THEN 1 ELSE 0 END,
--     enqueued_at,
--     source_url, title,
--     CASE
--         WHEN state = 'enqueued'             THEN 'enqueued'
--         WHEN state IN ('rejected_terminal','rejected_retryable') THEN 'rejected'
--         ELSE state
--     END,
--     last_error
-- FROM youtube_discoveries;
-- DROP TABLE youtube_discoveries;
-- ALTER TABLE youtube_discoveries_v1 RENAME TO youtube_discoveries;
-- CREATE INDEX idx_youtube_discoveries_watermark
--     ON youtube_discoveries(channel_id, discovered_at DESC);
-- DROP INDEX IF EXISTS idx_youtube_discoveries_retry;
-- DROP INDEX IF EXISTS idx_youtube_discoveries_lease;
