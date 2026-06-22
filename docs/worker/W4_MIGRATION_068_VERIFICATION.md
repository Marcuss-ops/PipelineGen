# W4 — Migration 068 verification and schema safety

> PRIORITY: P1
>
> STATUS: pending
>
> MAY RUN IN PARALLEL WITH W1 ONLY IF FILE SCOPE DOES NOT OVERLAP

## Objective

Verify that migration 068 safely brings every supported `media_assets` schema to the canonical shape required by search and clip queries:

```text
width INTEGER NOT NULL DEFAULT 0
height INTEGER NOT NULL DEFAULT 0
group_name TEXT NOT NULL DEFAULT ''
```

The goal is not merely to make one staging database work. The migration must be predictable for:

- a clean database built from all migrations;
- a database at migration 067;
- an existing database missing all three columns;
- known partially modified databases;
- repeated startup after successful migration;
- backup and restore.

## Current state

Migration file:

```text
migrations/sqlite/068_add_media_assets_width_height.sql
```

Current statements:

```sql
ALTER TABLE media_assets ADD COLUMN width INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN height INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_assets ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
```

This is valid for a normally versioned database where migration 068 runs once and none of the columns already exist. It may fail on a manually or partially modified database.

## Required branch

```text
codex/migration-068-verification
```

## Allowed scope

```text
migrations/sqlite/068_add_media_assets_width_height.sql
internal/infrastructure/database/**migration**
internal/infrastructure/database/sqlite/**/*migration*_test.go
internal/infrastructure/database/sqlite/**/*schema*_test.go
scripts/migrations/**
testdata/migrations/**
docs/worker/W4_MIGRATION_068_VERIFICATION.md
docs/certification/**
```

## Out of scope

- worker registry;
- broker behavior;
- new media fields;
- query redesign;
- broad schema refactor;
- PostgreSQL;
- deleting production data;
- editing old migrations without proof and compatibility review.

## Safety rule for applied migrations

Before changing migration 068, determine whether it has been applied outside disposable development environments.

If yes:

- do not rewrite it silently;
- prefer a new corrective migration;
- document checksum/version behavior;
- test upgrade from the applied state.

If no:

- editing 068 is allowed only before merge/deployment and with full migration tests.

Record the decision in the PR.

## Phase 0 — Discover the migration runner

Run:

```bash
rg 'migrations/sqlite|schema_migrations|migration' internal cmd scripts --type go
rg '068_add_media_assets' .
rg 'media_assets' migrations/sqlite
```

Document:

| Question | Answer |
|---|---|
| How are migrations ordered? | |
| Where is applied version stored? | |
| Are migrations transactional? | |
| Is checksum stored? | |
| What happens on statement 2 failure? | |
| Is a failed migration retried? | |
| Can migration files contain Go-controlled conditional logic? | |

Stop if these semantics are unclear. Add focused tests for the migration runner before hardening 068.

## Phase 1 — Define canonical schema assertions

Create a helper that inspects:

```sql
PRAGMA table_info(media_assets);
```

Assert exactly:

| Column | Type | Not null | Default |
|---|---|---:|---|
| width | INTEGER | 1 | 0 |
| height | INTEGER | 1 | 0 |
| group_name | TEXT | 1 | '' |

Also assert:

- existing rows receive defaults;
- inserts omitting fields succeed;
- reads using `COALESCE` succeed;
- relevant repository queries no longer return `no such column`;
- `PRAGMA integrity_check` returns `ok`;
- `PRAGMA foreign_key_check` returns no rows.

## Phase 2 — Test matrix

### Case A — Clean database

1. create empty temporary database;
2. run every migration in order;
3. assert current migration version;
4. assert canonical schema;
5. run repository search/query smoke tests.

### Case B — Database at 067

1. apply through migration 067;
2. insert representative `media_assets` rows;
3. apply 068;
4. assert rows preserved;
5. assert new defaults;
6. assert queries succeed.

### Case C — Reopen after successful 068

1. apply all migrations;
2. close database;
3. reopen and run migrator again;
4. assert no second execution failure;
5. assert schema unchanged.

This relies on migration version tracking, not on making raw SQL independently idempotent.

### Case D — Partial manual schema

Create three fixtures:

```text
width only
width + height
group_name only
```

Decide supported policy:

- fail fast with actionable diagnosis and require repair; or
- automatically repair through a corrective migration/tool.

Do not let the database remain half-migrated without a clear error.

### Case E — Existing data edge cases

Include:

- zero rows;
- many rows;
- null-like legacy values where possible;
- Unicode group names after migration;
- large dimensions;
- transactions active before migration if the runner allows it.

## Phase 3 — Failure atomicity

Determine whether the migration runner wraps the whole migration file in a transaction.

Test forced failure after the first `ALTER TABLE`.

Expected safe outcomes:

```text
A. transaction rollback leaves zero of the three columns
```

or

```text
B. migration runner records failure and repair procedure safely resumes from the partial schema
```

Unsafe outcome:

```text
one column added, migration version marked complete, remaining columns absent
```

If SQLite/runner behavior cannot guarantee atomicity for multiple `ALTER TABLE` statements, choose one:

1. one migration per column;
2. Go migration with schema inspection;
3. corrective follow-up migration with explicit detection.

Do not invent custom SQL syntax unsupported by the deployed SQLite version.

## Phase 4 — Production preflight command

Add or document a read-only preflight command that reports:

```text
database path
current migration version
media_assets present
width present/type/default
height present/type/default
group_name present/type/default
integrity_check
foreign_key_check
```

Suggested location:

```text
cmd/admin schema-check
```

or existing admin diagnostics command.

Requirements:

- no mutation by default;
- machine-readable exit code;
- secrets not printed;
- supports staging and backup copies;
- clear repair guidance.

## Phase 5 — Backup and restore drill

Before applying to a non-disposable database:

1. stop/quiesce writers according to runbook;
2. create SQLite-consistent backup;
3. record checksum;
4. restore backup to a temporary location;
5. run migration on restored copy;
6. run schema assertions and representative queries;
7. only then migrate target environment.

Record:

```text
backup path/reference
checksum
source migration version
post-migration version
row counts before/after
integrity result
restore result
```

## Phase 6 — Repository query smoke tests

Test the paths that motivated migration 068:

- media search;
- clip search;
- row scanning/mapping;
- insert/update using default dimensions/group;
- insert/update using explicit dimensions/group.

Minimum assertions:

```text
no "no such column: width"
no "no such column: height"
no "no such column: group_name"
row count unchanged
search returns expected record
```

## Phase 7 — Migration naming consistency

The filename and internal comment currently use slightly different descriptions.

Normalize documentation without changing migration identity unexpectedly.

Ensure:

- migration number unique;
- filename matches repository convention;
- no second 068 exists;
- comments identify all three columns;
- migration tracker references correct filename.

## Required tests

Example commands, adapted to actual package paths:

```bash
go test ./internal/infrastructure/database/...
go test ./internal/infrastructure/database/sqlite/...
go test -run 'Migration068|MediaAssetsSchema' ./...
go vet ./internal/infrastructure/database/...
go build ./cmd/admin
go build ./...
```

SQLite assertions:

```bash
sqlite3 "$DB" 'PRAGMA table_info(media_assets);'
sqlite3 "$DB" 'PRAGMA integrity_check;'
sqlite3 "$DB" 'PRAGMA foreign_key_check;'
```

## Exit gate

W4 is complete only when:

- [ ] migration runner behavior documented;
- [ ] clean database test passes;
- [ ] upgrade from 067 test passes;
- [ ] repeat startup test passes;
- [ ] partial schema policy implemented and tested;
- [ ] failure atomicity tested;
- [ ] existing rows preserved;
- [ ] canonical defaults verified;
- [ ] search/clip repository smoke tests pass;
- [ ] integrity and foreign-key checks pass;
- [ ] backup/restore drill documented;
- [ ] CI is green;
- [ ] post-merge verification runs on `main`.

## Rollback

Before deployment:

- restore database backup;
- deploy previous application image;
- verify previous migration version and queries.

Do not attempt to drop the three columns in-place as an emergency rollback unless a separately tested migration exists. SQLite column removal and dependent indexes/views require explicit planning.
