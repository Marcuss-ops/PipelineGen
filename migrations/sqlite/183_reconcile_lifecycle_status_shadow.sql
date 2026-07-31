-- 183_reconcile_lifecycle_status_shadow.sql
--
-- Reconcile the migration-152 shadow column after discovering that its
-- DEFAULT 'ACTIVE' prevented the original conditional backfill from
-- correcting existing lifecycle_state/lifecycle_status divergences.
--
-- `media_assets.lifecycle_state` remains the sole operational source of
-- truth. lifecycle_status is retained only as a shadow/compatibility
-- column and must never be used to decide whether an asset is usable.
-- This forward-only, idempotent update repairs every existing mismatch,
-- including NULL shadow values, without changing lifecycle_state.

UPDATE media_assets
SET lifecycle_status = lifecycle_state
WHERE lifecycle_status IS NOT lifecycle_state;
