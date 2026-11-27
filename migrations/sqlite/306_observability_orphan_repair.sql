-- database: observability
-- DB-AUDIT-01 repair: telemetry children whose run_observability parent was
-- deleted by an older retention path that ran without foreign-key
-- enforcement. They can never be joined to a run again, so they are removed
-- here instead of remaining permanently invisible. Scoped to the
-- observability plane: primary/cache copies do not necessarily carry run_*.

DELETE FROM run_operation_observations
WHERE NOT EXISTS (
    SELECT 1 FROM run_observability r
    WHERE r.run_id = run_operation_observations.run_id
);

DELETE FROM run_stage_observations
WHERE NOT EXISTS (
    SELECT 1 FROM run_observability r
    WHERE r.run_id = run_stage_observations.run_id
);

DELETE FROM run_artifact_observations
WHERE NOT EXISTS (
    SELECT 1 FROM run_observability r
    WHERE r.run_id = run_artifact_observations.run_id
);

DELETE FROM run_child_observations
WHERE NOT EXISTS (
    SELECT 1 FROM run_observability r
    WHERE r.run_id = run_child_observations.parent_run_id
);

DELETE FROM run_report_snapshots
WHERE NOT EXISTS (
    SELECT 1 FROM run_observability r
    WHERE r.run_id = run_report_snapshots.run_id
);
