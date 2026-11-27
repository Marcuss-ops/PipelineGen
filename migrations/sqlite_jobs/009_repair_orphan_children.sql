-- database: jobs
-- DB-AUDIT-01 repair (2026-11-27): job_stage_status rows whose parent job was
-- deleted while the writer ran without foreign_keys=ON. The cascade never
-- fired, so these rows survived their job. Remove every job child whose
-- parent no longer exists; the same class of orphan applies to results,
-- payloads, steps, events and asset relations.
DELETE FROM job_stage_status
WHERE NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id = job_stage_status.job_id);

DELETE FROM job_steps
WHERE NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id = job_steps.job_id);

DELETE FROM job_asset_relations
WHERE NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id = job_asset_relations.job_id);

DELETE FROM job_events
WHERE NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id = job_events.job_id);

DELETE FROM job_results
WHERE NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id = job_results.job_id);

DELETE FROM job_payloads
WHERE NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id = job_payloads.job_id);
