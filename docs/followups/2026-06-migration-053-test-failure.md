# Pre-existing: `internal/app` tests fail on migration 053 (unrelated to Phase B)

**Recorded**: June 2026 (during the legacy-directory CI-guard PR)
**Owner**: TBD — file added so the issue is on record; the fix is out of scope
for the Phase A→C PR series.

## Symptom

`go test ./internal/app/...` reports the following on four tests
(`TestWireServicesDoesNotPanicWithoutDriveAndArtlist`,
`TestCleanupCanBeCalledMultipleTimesSafely`,
`TestWireServicesSkipsOptionalHandlersWhenDepsMissing`,
`TestStartupIntegration`):

```
wire_test.go:49: WireServices failed: failed to run database migrations:
failed to run main migrations: storage: apply 053_job_lifecycle_atomic.sql:
cannot commit - no transaction is active
```

## Provenance

Reproduced **with Phase B stashed** on `git log --oneline -5`: the failure
exists on HEAD before any of the Phase A→C changes. The four tests all funnel
through `WireServices → initCoreMinimal → runAllMigrations → ?` and the runner
hits the embedded `BEGIN IMMEDIATE;` transaction-control command in
`migrations/sqlite/053_job_lifecycle_atomic.sql`:

```
-- 3. Replace the old CHECK constraint. SQLite does not support ALTER
--    COLUMN; we rebuild the table. Since this is a development database
--    with manageable row counts, the CREATE TABLE … AS SELECT pattern is
--    safe inside a transaction.

BEGIN IMMEDIATE;
CREATE TABLE jobs_new ( ... );
INSERT INTO jobs_new (...) SELECT ... ;
-- (no COMMIT)
```

When the migration runner wraps the file in its own outer transaction, the
embedded `BEGIN IMMEDIATE;` becomes a nested transaction (which SQLite does
not support) and the runner then attempts to `COMMIT` at the end of its
outer wrapper — by which point the inner transaction has already aborted
the outer one, so the outer `COMMIT` reports "no transaction is active".

## Files involved

- Migration: `migrations/sqlite/053_job_lifecycle_atomic.sql` (the offender)
- Runner: `internal/infrastructure/database/migrations.go` (and `migrations_splitter.go`)
- Tests: `internal/app/wire_test.go` lines 49 ff. (the four failing tests)

## Reproduction recipe

```bash
# Phase B is unrelated — stash it to prove that:
git stash push -- internal/app/dependencies.go internal/app/module_jobs.go
go test ./internal/app/... -count=1 -run TestWireServicesDoesNotPanicWithoutDriveAndArtlist
# Expected: same failure (pre-existing).
git stash pop
```

## Fix (proposed, NOT applied in this PR)

Two options, preferred order:

1. **Strip the embedded `BEGIN IMMEDIATE;` and final `COMMIT;` from
   `053_job_lifecycle_atomic.sql`**, leaving the table-rebuild statements.
   The outer runner transaction handles commits. This matches every other
   migration file in `migrations/sqlite/`.
2. If option 1 breaks a real production-data path, give the migration runner
   a `--no-wrap` flag for files that manage their own transactions, and
   gate migration 53 with it.

## Why this hasn't been fixed yet

The PR series that introduced the legacy-directory CI guard was scoped to:
- Lock down new file additions to legacy directories.
- Pilot the `BuildJobsBundle` ownership inversion for the Jobs module.
- Burn down the two zero-importer legacy packages.

Fixing the migration runner would touch the storage layer (a different
ownership boundary) and likely needs a small migration-runnner audit to
confirm no other migration file embeds `BEGIN`/`COMMIT` statements. That's
a separate PR.

## When to address this

Tracked separately so the Phase-B pilot stays green and reviewable. As soon
as `internal/infrastructure/database/migrations.go` has CI coverage (a
sample migration that crashes the runner → produces a meaningful error
message), this fix becomes tractable as a one-line change.
