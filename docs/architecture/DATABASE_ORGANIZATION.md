# Clean Database Organization

> STATUS: ACTIVE
>
> Goal: one explicit database architecture, no ad-hoc connections, no duplicate persistence owners and reproducible backup/restore.

## 1. Current state

The repository already has a strong primary direction:

- one canonical operational SQLite database: `media.db.sqlite`;
- one `SQLiteDB` wrapper under `internal/infrastructure/database`;
- WAL mode, foreign keys and busy timeout enabled;
- versioned SQL migrations under `migrations/sqlite`;
- migration checksums stored in `schema_migrations`;
- each migration applied inside a transaction;
- backup script uses `VACUUM INTO` and verifies integrity;
- Qdrant is backed up separately.

Remaining cleanliness problems:

1. `internal/app/bootstrap.go` opens `api_requests.db.sqlite` directly with `sql.Open`.
2. Composition code runs direct SQL for `clip_folders` during Drive folder resolution.
3. The primary DB and observability DB are not represented by one explicit `DatabaseSet` contract.
4. The backup script protects `media.db.sqlite`, but the logging DB has no explicit retention/backup policy.
5. Data paths are partly convention and partly hardcoded.
6. Repository ownership by table is not documented as a strict contract.
7. Schema validation is mostly migration-driven instead of being exposed through one database doctor command.

## 2. Source-of-truth model

| Data | Canonical source | Derived/cached copies |
|---|---|---|
| assets and metadata | primary SQLite | Qdrant, local caches |
| asset locations/versions | primary SQLite | Drive/local filesystem references |
| jobs, leases, workers, events | primary SQLite | metrics/logs |
| scripts, scenes, generation state | primary SQLite | Google Docs, exported files |
| final binary media | Drive or configured object/file storage | local workspace/cache |
| vector index | Qdrant | rebuildable from primary SQLite |
| API request logs | observability store | disposable after retention period |
| temporary files | workspace/temp directories | never canonical |

Qdrant must never become the only copy of asset metadata. Local paths must never be used as the canonical asset identity.

## 3. Allowed database files

Maximum-cleanliness target permits only these SQLite databases:

```text
primary operational DB:
  data/db/media.db.sqlite

optional observability DB:
  data/db/observability.db.sqlite
```

During transition, the existing compatibility path remains allowed:

```text
data/media.db.sqlite
```

Do not move the deployed primary file merely for aesthetics. Path migration requires backup, controlled copy, config change, checksum/integrity verification and rollback.

No package may create additional `.db`, `.sqlite` or `.db.sqlite` files without updating this document and the database registry.

## 4. Target data directory

```text
data/
  db/
    media.db.sqlite
    media.db.sqlite-wal
    media.db.sqlite-shm
    observability.db.sqlite        # optional
  blobs/
    inputs/
    outputs/
  cache/
    downloads/
    metadata/
  workspaces/
    jobs/
  exports/
  tmp/
```

Backups must live outside the active data directory:

```text
/var/backups/pipelinegen/<timestamp>/
```

Rules:

- DB files only in `data/db` after path migration;
- temporary job work only in `data/workspaces` or OS temp with one cleanup owner;
- caches are disposable;
- exports are reproducible deliverables, not application state;
- backups never share the same volume as the only live database copy.

## 5. Database construction ownership

Only infrastructure may open SQL connections.

Target package:

```text
internal/infrastructure/database/
  set.go
  sqlite.go
  migrations.go
  health.go
  backup.go
  sqlite/
    assets/
    content/
    jobs/
    observability/
    scripts/
```

Suggested contract:

```go
type DatabaseSet struct {
    Primary       *SQLiteDB
    Observability *SQLiteDB // optional
}

func OpenSet(cfg Config, log *zap.Logger) (*DatabaseSet, error)
func (s *DatabaseSet) Migrate(log *zap.Logger) error
func (s *DatabaseSet) Health(ctx context.Context) error
func (s *DatabaseSet) Close() error
```

`internal/app` may call `OpenSet`, but must not call `sql.Open`, define DDL or execute business SQL.

## 6. Required code moves

### DB-1 — Move API request logging ownership

Current direct DB opening in `internal/app/bootstrap.go` must move to:

```text
internal/infrastructure/database/sqlite/observability
```

The package should own:

- connection opening;
- schema/migrations;
- request-log repository;
- retention/pruning;
- health check;
- optional backup policy.

API middleware receives an interface, not `*sql.DB`.

### DB-2 — Move Drive folder SQL out of composition

Direct `clip_folders` queries/inserts in bootstrap must move to the canonical assets repository or a focused folder repository under:

```text
internal/infrastructure/database/sqlite/assets
```

Application orchestration may request:

```go
ResolveOrCreateFolder(ctx, source, path)
```

but must not know SQL statements.

### DB-3 — Introduce a database registry/set

The composition root should receive one `DatabaseSet`, then pass narrow repository interfaces to use cases.

Do not pass the full DB set into API handlers or domain services.

### DB-4 — Normalize paths

Add explicit configuration fields rather than building paths in many packages:

```text
storage.data_dir
storage.primary_db_path
storage.observability_db_path
storage.workspace_dir
storage.cache_dir
storage.export_dir
```

Defaults must preserve current deployments until a controlled path migration is executed.

## 7. Table ownership

Each table has one repository owner.

### Assets owner

```text
internal/infrastructure/database/sqlite/assets
```

Typical tables:

```text
media_assets
asset_locations
asset_versions
asset_processing
clip_folders
search_queries
indexing checkpoints
provider/source metadata
```

### Jobs owner

```text
internal/infrastructure/database/sqlite/jobs
```

Typical tables:

```text
jobs
job_events
worker_nodes
outbox events
retry/dead-letter state
```

### Scripts owner

```text
internal/infrastructure/database/sqlite/scripts
```

Typical tables:

```text
scripts
script_sections
script generation cache/memory
narrative plans and persisted generation metadata
```

### Content owner

```text
internal/infrastructure/database/sqlite/content
```

Books/lessons tables only if they are not part of the script aggregate.

### Observability owner

```text
internal/infrastructure/database/sqlite/observability
```

Typical tables:

```text
api_requests
operational audit events
```

Observability tables must not be queried by domain use cases.

## 8. Schema rules

### Identity

- stable IDs are TEXT unless SQLite row IDs are intentionally internal;
- asset identity is not a path or Drive URL;
- external provider IDs are stored with provider/source identity;
- one unique constraint prevents duplicate canonical assets.

### Time

- store UTC consistently;
- use one format per table family;
- every mutable aggregate has `created_at` and `updated_at`;
- terminal job timestamps are explicit.

### Relations

- enable and test foreign keys;
- use explicit delete behavior;
- do not rely on application code to emulate all referential integrity;
- no orphan location/version/processing records.

### JSON

Use JSON only for extension metadata that is not commonly filtered or joined.

Promote a JSON field to a real column/table when:

- it is queried frequently;
- it needs an index;
- it participates in uniqueness;
- it is required for lifecycle transitions;
- multiple packages parse it independently.

### Indexes

Naming convention:

```text
idx_<table>__<column_or_purpose>
ux_<table>__<unique_purpose>
```

Every production query used in polling, search or lifecycle transitions must have an `EXPLAIN QUERY PLAN` review.

### Lifecycle state

Avoid storing the same lifecycle truth in several tables. The canonical state owner must be documented. Derived status columns must be recomputable or transactionally maintained.

## 9. Connection rules

All SQLite settings must be configured in one place.

Required baseline:

```text
journal_mode = WAL
foreign_keys = ON
busy_timeout = explicit
synchronous = explicit and documented
```

Connection-pool limits must be explicit and measured. Do not allow each repository to open its own pool.

Transactions:

- keep write transactions short;
- never hold a transaction during HTTP, Drive, Qdrant, LLM or FFmpeg calls;
- perform external work first or use an outbox/state transition pattern;
- job claim/lease/finalization remain fenced atomic operations;
- repository methods accept context.

## 10. Migration policy

Canonical primary migrations:

```text
migrations/sqlite/NNN_description.sql
```

Optional observability migrations:

```text
migrations/sqlite_observability/NNN_description.sql
```

Rules:

1. Version prefix is unique.
2. Applied migration files are never edited or renamed.
3. SHA-256 checksum mismatch is a hard failure.
4. Each migration is transactional where SQLite permits it.
5. Schema changes and data backfills are separated when risk is high.
6. Large backfills are resumable and observable.
7. A migration includes fresh-DB and upgrade tests.
8. A migration never depends on undocumented manual SQL.
9. Duplicate-column soft skipping is defense-in-depth, not a substitute for canonical schema tests.
10. Every release records the latest migration version.

## 11. Database doctor

Provide or consolidate admin commands:

```text
pipelinegen-admin db status
pipelinegen-admin db check
pipelinegen-admin db migrations
pipelinegen-admin db backup
pipelinegen-admin db restore --verify
```

`db check` should report:

- resolved paths;
- open/read/write status;
- migration version and pending migrations;
- checksum mismatches;
- `PRAGMA integrity_check`;
- `PRAGMA foreign_key_check`;
- journal mode;
- busy timeout;
- WAL size;
- table/index counts;
- expected critical columns/indexes;
- Qdrant connectivity separately.

## 12. Backup policy

### Primary DB

Required:

- consistent online backup using SQLite backup API or `VACUUM INTO`;
- checksum;
- integrity check on backup;
- off-host copy;
- retention policy;
- restore drill.

### Observability DB

Choose one explicit policy:

- disposable with retention and no disaster-recovery requirement; or
- included in backup manifest.

Do not leave the policy implicit.

### Qdrant

Qdrant is derived but snapshots reduce recovery time. Backup manifests should include collection name, aliases and snapshot reference.

### Files/Drive

Backup documentation must distinguish:

- canonical Drive/object-storage files;
- local cache/workspace files that need no backup;
- local-only deliverables that must be copied before cleanup.

## 13. Restore order

1. deploy the exact application/migration version;
2. restore primary SQLite backup;
3. run integrity and foreign-key checks;
4. start application with migrations in verification mode;
5. restore or rebuild Qdrant;
6. verify Drive/file references;
7. restore observability only if policy requires it;
8. run E2E read/write smoke tests;
9. measure RTO and RPO.

## 14. Required PR sequence

```text
DB-0 inventory and table ownership map
DB-1 observability DB adapter
DB-2 folder repository extraction from app/bootstrap
DB-3 DatabaseSet and central connection policy
DB-4 path normalization and controlled data-dir migration
DB-5 doctor, backup and restore verification
DB-6 strict SQL ownership gate
```

Each PR must be independently deployable.

## 15. Strict gates

CI should eventually fail on:

```bash
rg 'sql\.Open\(' internal --type go \
  | grep -v 'internal/infrastructure/database'

rg 'database/sql' internal/api internal/application internal/domain --type go

find data -type f \( -name '*.db' -o -name '*.sqlite' -o -name '*.db.sqlite' \)
```

The last command is an operational check: only registered database paths are allowed.

## 16. Exit gate

Database cleanup is complete when:

- all DB connections are opened by infrastructure;
- app/API/application layers contain no direct SQL;
- every table has one repository owner;
- only registered database files exist;
- primary and observability policies are explicit;
- migrations are immutable and tested;
- backup and restore are verified;
- DB doctor passes;
- schema ownership documentation matches real code;
- CI prevents new ad-hoc database access.