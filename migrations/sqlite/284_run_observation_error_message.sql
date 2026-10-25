-- database: observability
-- A failed stage/operation must carry its CAUSE next to the fact: error_code
-- alone degrades to "error" for every untyped error and cannot distinguish a
-- certification failure from a provider outage from a deferral. The message is
-- the operator's first attribution surface (the job_steps mirror already had
-- error_message; run_stage_observations/run_operation_observations were the
-- only projection that dropped it).

ALTER TABLE run_stage_observations ADD COLUMN error_message TEXT NOT NULL DEFAULT '';
ALTER TABLE run_operation_observations ADD COLUMN error_message TEXT NOT NULL DEFAULT '';
