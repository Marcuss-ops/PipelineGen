-- database: primary
-- pipeline_runs was the pre Job -> Attempt -> Run script ledger. All active
-- reads/writes now use observability.run_observability.workflow_payload_json.
DROP TABLE IF EXISTS pipeline_runs;
