-- database: observability
--
-- Immutable history for run reports. `run_observability.report_json` stays the
-- current projection (queries and dashboards read it), while every version
-- written by StartReport/SaveReport/RecoverAbandoned is copied here by trigger
-- before the live row changes. The table is append-only: an UPDATE is rejected,
-- so a later save can never rewrite an earlier report.
--
-- Why a trigger instead of a writer change: every recorder path already funnels
-- through INSERT/UPDATE on run_observability. A trigger captures all of them —
-- including parent-summary refreshes and abandoned-run reconciliation — without
-- adding a second write that a future caller could forget.
CREATE TABLE IF NOT EXISTS run_report_snapshots (
    snapshot_id  INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id       TEXT NOT NULL,
    report_json  TEXT NOT NULL,
    captured_at  TEXT NOT NULL,
    FOREIGN KEY(run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_report_snapshots_run
    ON run_report_snapshots(run_id, snapshot_id);

DROP TRIGGER IF EXISTS trg_run_observability_snapshot_insert;
CREATE TRIGGER trg_run_observability_snapshot_insert
AFTER INSERT ON run_observability
BEGIN
    INSERT INTO run_report_snapshots (run_id, report_json, captured_at)
    VALUES (NEW.run_id, NEW.report_json, NEW.updated_at);
END;

DROP TRIGGER IF EXISTS trg_run_observability_snapshot_update;
CREATE TRIGGER trg_run_observability_snapshot_update
AFTER UPDATE OF report_json ON run_observability
WHEN OLD.report_json IS NOT NEW.report_json
BEGIN
    INSERT INTO run_report_snapshots (run_id, report_json, captured_at)
    VALUES (NEW.run_id, NEW.report_json, NEW.updated_at);
END;

-- The snapshot rows are evidence, not a mutable cache. A UPDATE would let the
-- history be rewritten after the fact; forbid it. DELETE is intentionally not
-- blocked: retention may remove a run and the FK cascade must still work.
DROP TRIGGER IF EXISTS trg_run_report_snapshots_append_only;
CREATE TRIGGER trg_run_report_snapshots_append_only
BEFORE UPDATE ON run_report_snapshots
BEGIN
    SELECT RAISE(ABORT, 'run_report_snapshots is append-only');
END;
