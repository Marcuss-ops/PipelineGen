# Pre-existing: `internal/application/jobs/service_test.go::setupTestDB` schema is stale (missing `lease_id` / `revision`)

**Recorded**: June 2026 (during the Wave 16 alias-removal PR)
**Owner**: TBD — out of scope for Wave 16; documented so future cleanup
agents don't re-lose the same debug cycle when the failure resurfaces in
CI on a freshly-cloned branch.

## Symptom

`go test -count=1 ./internal/application/jobs/...` reports:

```
no such column: lease_id
```

for every test that uses `setupTestDB` / `setupTestService` and calls
methods that hit the canonical SQLite queries (`Create` / `Get` / `List`
/ `ClaimNext` / `Transition` / `RefreshMetrics` / `ListEvents`).

The first failing assertion in a typical run is in
`TestCreateJobStoresPendingJob` (line 79) — `svc.Enqueue` → `repo.Create`
→ `INSERT INTO jobs (...) VALUES (?, ..., ?, ..., ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
crashes because the test schema has no `lease_id` (16th column in the
column list).

## Provenance

Reproduced **with Wave 16 stashed** (`git stash push -- internal/application/jobs/store.go`)
on `git log --oneline -5`: the failure exists on `main` before any
Wave 16 changes. The test schema in `setupTestDB` was written before
the Wave 5 PR 1 migration (`migrations/sqlite/053_job_lifecycle_atomic.sql`)
added two new columns to the `jobs` table:

- `lease_id TEXT DEFAULT ''`
- `revision INTEGER NOT NULL DEFAULT 1`

The SQLite implementation expects both — the canonical column list lives
in `internal/infrastructure/database/sqlite/jobs/repository.go::jobColumns`
(line 18) which lists every column the implementation reads/writes,
and `jobColumns` includes both `lease_id` and `revision`. The test's
in-memory schema was not updated to match.

## Files involved

- Test: `internal/application/jobs/service_test.go::setupTestDB` (lines 26–58).
- SQLite impl: `internal/infrastructure/database/sqlite/jobs/repository.go::jobColumns` (line 18).
- Migration that introduced the columns: `migrations/sqlite/053_job_lifecycle_atomic.sql`.

## Reproduction recipe

```bash
# Wave 16-affected files: the failure is pre-existing, but stash anyway
# to prove there's no Wave 16 contribution.
git stash push -- \
  internal/application/jobs/store.go \
  internal/application/jobs/service.go \
  internal/application/jobs/worker.go \
  internal/application/jobs/service_test.go \
  internal/app/module_jobs.go \
  internal/app/dependencies.go \
  internal/infrastructure/jobs/local/broker.go

go test -count=1 -run TestCreateJobStoresPendingJob ./internal/application/jobs/...
# Expected: same failure (pre-existing).

git stash pop
```

## Fix (proposed, NOT applied in this PR)

Add the two missing columns to the `CREATE TABLE jobs` statement inside
`setupTestDB` (`internal/application/jobs/service_test.go::setupTestDB`)
so the test schema matches the prod schema applied by migration 053:

```sql
CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    ...all existing columns unchanged...
    lease_id TEXT DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_jobs_status_priority
    ON jobs(status, priority DESC, created_at ASC);
-- (also keep the existing idx_jobs_active_key and idx_jobs_type_correlation)
```

After this one-line addition the Wave 16-era idempotency coverage
(`TestEnqueue_Idempotence_*`), the rescue-path test
(`TestEnqueueRescuePathMultiService`), the stale-running-failed test
(`TestJobsMarkStaleRunningJobsFailed`), and the concurrent-creation test
(`TestConcurrentJobCreationDoesNotRace`) all flip from `FAIL` to `PASS`.

## Why this hasn't been fixed yet

Wave 16 was scoped to alias removal from
`internal/application/jobs/store.go` (3 type-aliases + 1 constructor
deleted; 6 files updated to import
`internal/infrastructure/database/sqlite/jobs` directly). Touching the
test schema goes one layer deeper (a test contract change), and the
failing tests are exactly the same ones the Wave 16 review *verified
still compile* — i.e. the failure is purely schema, not Wave 16
regression. Keeping the fix out of Wave 16 keeps that PR surgical and
reviewable.

Additional concern: the `CREATE TABLE` in `setupTestDB` is missing every
column the SQL implementation reads since Wave 5 PR 1, not just
`lease_id` / `revision`. A complete fix would also re-derive the schema
from a real-migration replay (e.g. `drive.NewTestDBWithMigrations(t, ...)`)
to avoid drifting again. That's an overkill for the immediate Wave 16
followup.

## When to address this

Tracked separately so Wave 16 stays a clean, surgical PR. A natural home
for the fix is the next PR that touches the jobs test suite — or the
mechanical Wave 17.1 cleanup (drop dup `Store` interface) which already
forces a re-validation of the SQLite contract. One-line diff; merges
into either with zero risk.
