# 2026-06 — Migration 091 redundant, deletion followup

## Context

Earlier in this branch (commit `572970f2`) we added
`migrations/sqlite/091_add_media_assets_search_terms.sql` containing an
`ALTER TABLE media_assets ADD COLUMN search_terms TEXT NOT NULL DEFAULT ''`
statement. The companion-code work in this branch (the API middleware
typed-port cascade then this DeriveSearchTerms + store.Save wiring)
surfaced that the column was already declared in
`internal/infrastructure/database/canonical.go` (`CanonicalMediaAssetsSchema`
line 84) as `search_terms TEXT NOT NULL DEFAULT '[]'`, so any live
database created from the canonical schema already had the column. The
original ALTER would therefore fail with `duplicate column name`.

The intermediate commit `5d969295` rewrote 091 to a `SELECT 1;` no-op so
that running it on any database (existing column or pending) succeeded
without ALTER errors. That was a stopgap.

## Resolution

The followup code on this branch — `DeriveSearchTerms` /
`mergeSearchTerms` / `BackfillDeps`-chunked `applyBackfillBatch` — is
the actual companion code for migration 091. There is no schema change
needed; the work happens on the application ingestion path.

Migration 091 is being deleted in this turn (commit "feat(media-assets):
auto-derive search_terms on ingest + drop redundant migration 091").
Deleting: `migrations/sqlite/091_add_media_assets_search_terms.sql`.
After deletion, the migration sequence number `091` is no longer
occupied; if a future migration needs that number, it can take the
slot without conflict.

## Operational impact

- Databases created via the canonical schema: no migration to run;
  legacy data still needs an opt-in backfill via
  `go run ./cmd/admin backfill-media-assets-search-terms --apply` to
  populate the column for rows that were ingested before the
  DeriveSearchTerms wiring shipped.
- Databases that somehow have a partial / older schema: the canonical
  `CREATE TABLE IF NOT EXISTS` will reconcile on next startup.
- Migration-runner tooling that iterates `migrations/sqlite/*.sql`:
  total file count drops by 1; nothing in `cmd/admin/migrate*` or
  the migration registry requires 091's existence (it has always been
  additive-idempotent).

## Followups

- `PG-006.2`: Add a sqlite-backed integration test `internal/domain/asset/store_save_terms_test.go`
  that asserts `store.Save` with `SearchTerms == nil` ends up writing
  a non-empty `search_terms` JSON to the column. Closes round-3 reviewer
  concern #4.
- `PG-006.3`: Promote the inline adapter trios (`serverXxxAdapter` /
  `genDocsXxxAdapter` / `middlewareXxxAdapter`) into
  `pkg/middleware/adapters.go` so a single canonical implementation is
  reachable from `internal/api/`, `cmd/admin/`, and `internal/app/`.
