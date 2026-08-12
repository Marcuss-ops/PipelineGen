-- database: observability
-- Canonical workflow checkpoint payload for capability-specific durable state.
-- It is not a second run ledger: identity, lifecycle and timing remain in
-- run_observability; this column carries the script request/result envelope.
ALTER TABLE run_observability ADD COLUMN workflow_payload_json TEXT NOT NULL DEFAULT '{}';
