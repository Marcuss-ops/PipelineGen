# Entity image catalog retention

The durable PERSON image catalog keeps accepted, active candidates until they
are retired. Rejected and retired rows are operational noise after their retry
window, not durable evidence.

## Policy

- `status = 'retired'` rows are deletable after the configured retention cutoff.
- `semantic_status = 'rejected'` rows are deletable after the same cutoff.
- `active`, `fresh`, `stale` and `broken` rows are never deleted by the sweep:
  recertification still owns their bounded retry path.
- Materializations cascade with their candidate row. No orphan materialization
  is created by the sweep.
- The default cutoff is 30 days, measured from `updated_at`.

## Enforcement

`SQLiteEntityImageCatalogAdapter.PruneRetiredCandidates(ctx, cutoff)` executes
the single DELETE for the policy above. The migration
`294_entity_image_catalog_retention.sql` owns the covering index. The maintenance
command must call the method with the cutoff and record the returned row count.
