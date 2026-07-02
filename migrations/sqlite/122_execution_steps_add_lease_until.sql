-- PipelineGen migration 122 — execution_steps.lease_until (Step 10 / Stock Cutover C1/4).
--
-- Adds a per-row heartbeat column to the canonical execution_steps SSOT
-- so the steps.Store can detect "started-but-stalled" rows (lease_until
-- < now AND status='pending') without a separate state-machine outbox.
--
-- godlike/06 SSOT: this column extends the ONE canonical owner of the
-- "did phase X complete for job Y" fact. It does NOT introduce a second
-- table; the existing execution_steps surface (migration 121) is the
-- single source of truth for step state. The user-spec wording for
-- "(stock_execution_steps.run_id, stage, lease_until)" maps 1:1 onto
-- the existing (job_id, step_key, lease_until) triple — the SSOT
-- contract is preserved (no parallel table, no separate repository,
-- no recovery.go with MAX(stage)).
--
-- godlike/07 fail-closed semantics: lease_until is SET on MarkStarted
-- (now + DefaultLeaseTTL) and CLEARED on MarkCompleted / MarkFailed.
-- A row with lease_until != '' AND lease_until < now AND status !=
-- 'completed' AND status != 'failed' is the canonical "stalled run"
-- signal — operators query ix_execution_steps_leased_stale to find
-- runs whose worker crashed mid-flight (SIGKILL, OOM, deploy restart).
-- The orchestrator's resume path uses steps.ErrStepAlreadyCompleted
-- on MarkStarted for the canonical skip-already-completed behavior
-- (see stockpipeline.Orchestrator.RunResilient post-Step-10 commit);
-- lease_until is a parallel monitoring aid, NOT a recovery contract.
--
-- Migration is additive only: no column drops, no renames, no row
-- migrations. Existing execution_steps rows get lease_until='' via
-- the new column's DEFAULT — read paths treat '' as "lease expired
-- but row is terminal (Completed/Failed)" so historical rows stay
-- idempotent against the new index.

ALTER TABLE execution_steps ADD COLUMN lease_until TEXT NOT NULL DEFAULT '';

-- Heartbeat-stale scan index: leveraged by the operator dashboard
-- / admin query "list runs whose worker crashed mid-flight". The
-- partial-index form (lease_until != '') keeps the index narrow:
-- only non-terminal rows with active heartbeat are indexed, so the
-- crash-detection scan does NOT include the millions of completed
-- rows that have lease_until = ''. SQLite supports partial indexes
-- via WHERE — matches the canonical godlike/06 SSOT pattern from
-- 036_job_idempotency.sql.
CREATE INDEX IF NOT EXISTS ix_execution_steps_leased_stale
    ON execution_steps (lease_until)
    WHERE lease_until != '';
