-- database: primary
-- Process phase timing is represented by run_stage_observations and
-- run_operation_observations. The former table had no active production
-- writer after the canonical recorder cutover.
DROP TABLE IF EXISTS process_phase_metrics;
