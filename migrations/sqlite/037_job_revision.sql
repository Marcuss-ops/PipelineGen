-- Migration 037 — Optimistic locking for the jobs state machine.
--
-- Adds a per-row monotonic `revision` counter incremented on every status
-- transition, so future-proofs the system against concurrent worker
-- overwrites (e.g. two workers claiming the same job after lease
-- expiry races, or a transition colliding with a Cancel issued by the
-- operator).
--
-- PR-1 introduces a centralised `JobService.Transition(ctx, TransitionRequest)`
-- that does:
--
--     UPDATE jobs
--     SET status = ?, revision = revision + 1, ...
--     WHERE id = ? AND revision = ? AND status = ?;
--
-- See internal/core/domain/job for the canonical request shape.

ALTER TABLE jobs ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;

-- Composite (id, revision) index lets the optimistic-lock check resolve
-- uniqueness in O(1) and accelerates the canonical
-- `SELECT … WHERE id = ? AND revision = ?` probe inside Transition.
CREATE INDEX IF NOT EXISTS idx_jobs_id_revision ON jobs(id, revision);
