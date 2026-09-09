# PipelineGen SQLite Baseline Freeze — Design (2026-09-09)

> Status: approved design, incremental implementation. Implementing this plan
> does NOT delete any historical migration files; it only introduces the
> baseline file and documents the cutover contract. Deletion of `001`..`267`
> is deferred by one support window (see § Support window).

## 1. What freezes and at what version

* **Freeze version: `N = 267`** — the highest SQLite version prefix at the
  time of this plan (`267_observability_business_quarantine.sql`). The
  baseline captures the **post-`267` schema state** for every non-media table
  on the primary and observability SQLite planes.
* **Postgres media SSOT is NOT frozen**: `migrations/postgres/001..003`
  remain authoritative as-is (3 files, semantic names). This plan only adds
  `004` and drafts `005` on that plane (see §§ 5–7).
* **The `sqlite_jobs` plane (4 files) is NOT frozen** — it stays as-is
  (`jobs/jobs.db.sqlite` is self-contained and tiny).
* File identity: `migrations/sqlite/000_baseline_267.sql`.
  Version `000` sorts lexicographically before every integer-prefixed file
  and is easy to recognise as the consolidated baseline.

## 2. How fresh and old databases diverge

### 2.1 New (empty) database

1. The runner discovers `000_baseline_267.sql` — the earliest unfinished
   migration in `-- database: primary` or `observability` scope.
2. It executes `000_baseline_267.sql` in one transaction, recording the
   baseline row (see § 3: ledger row uses `version = 0` but `migration_id`
   semantics mark the baseline as a **sentinel** for the frozen window;
   alternatively the implementation may record version `267` with the baseline
   checksum — both are feasible; the doc pins the contract).
3. It skips every `001`..`267` file because the ledger shows `baseline`
   applied and covers that version window (skip policy lives in
   `internal/platform/sqlite/migrations_discovery.go::discoverMigrations`
   and `migrations.go::migrateAll`; added incrementally, feature-flagged off
   until baseline generation is verified).
4. It proceeds with `268` (if any) onward normally.

Result: a fresh install touches **one baseline file + any post-N deltas**.

### 2.2 Old (already-populated) database

* Ledger still contains `001`..`267` rows at their original checksums.
* `000_baseline_267.sql` is never applied — either it is skipped because
  `schema_migrations` already covers `version >= 1..267`, or it is applied
  but short-circuits because every `CREATE TABLE IF NOT EXISTS` /
  `CREATE INDEX IF NOT EXISTS` is a no-op on a populated DB and the runner
  writes a compatibility ledger entry. Either implementation is compatible
  with `IF NOT EXISTS`.
* Future `268` onward proceed normally.

**No data loss path exists**: the baseline only adds schema idempotently; it
never drops or rewrites rows.

## 3. Ledger semantics of the baseline row

Three options were considered; we pin **option B** as the cutover contract:

* (A) Baseline as version `0` + sentinel: record `version = 0`,
  `filename = 000_baseline_267.sql` — keeps `1..267` row identities intact on
  old DBs. Fresh DBs seed one row and then skip `1..267` by explicit range
  check. Slightly unusual because `0` is outside the `1..N` monotone.
* (B) Baseline as version `267` alias: on fresh DBs, `000_baseline_267.sql`
  is applied and recorded as `version = 267` with the baseline's file
  checksum — future `268+` deltas follow naturally, and `validateNoDuplicateVersions`
  is updated to treat `000_baseline_267.sql` as occupying `267` for fresh-DB
  installs. **Pinned.**
* (C) Baseline as compressed synthetic `1..267` ledger: fake 267 rows.
  Rejected — checksum fraud.

We proceed with (B) because it preserves the `version = max(N)` ledger
invariant verified by `validateAppliedMigrationSet`, while keeping the
discoverability rule simple:

> If a baseline row for `N` is present in `schema_migrations`, migrations
> `001`..`N` are treated as satisfied for that `targetDB` even when no
> per-version row exists.

## 4. Support window and shim removal

| Phase | What happens | Who it affects |
|-------|--------------|----------------|
| Phase 1 — now | Docs + incremental runner prep + baseline generation (this PR series) | New installs get the baseline; old installs untouched |
| Phase 2 — next minor after verification | Flip the baseline as **default path** for new DBs | New installs skip incremental museum; old installs still validate against `1..267` |
| Phase 3 — one support window later | Stop validating `checksum` of `1..267` on old DBs that have already passed them; only verify `baseline` + `268+` | Very-old DBs that never upgraded past `N` must pass `1..N` before baseline; otherwise fine |
| Phase 4 — far future | Archive `001`..`267` files to `migrations/sqlite/archive/` or remove after the team agrees the oldest supported baseline is `267` | Historical installs no longer need those files |

**Shim removal** follows phase 3: the `legacyCommentChecksums` (30 versions),
`legacyChecksums` (`191,192,193`), per-version shims for `198,195,201,186,109`,
the `253` ledger-compatibility gate, the `262`-skip gate, and
`reconcileHistoricalMigrationIdentities` (`238→239`) become dead code once
fresh DBs never touch `1..267`. They are removed in dedicated follow-up PRs,
never bundled with a feature slice.

## 5. PostgreSQL TIMESTAMPTZ expand — `004_media_timestamps_timestamptz.sql`

Purpose: stop the `TEXT` debt on the media PG SSOT. The SQLite ↔ PG column-
for-column mirror already paid for cutover parity through tight coupling;
going forward the media domain grows on **typed** `TIMESTAMPTZ`.

### 5.1 Columns added (expand)

For every hot-path `TEXT` timestamp that today holds RFC 3339 strings:

* `media_assets.created_at_ts TIMESTAMPTZ`
* `media_assets.updated_at_ts TIMESTAMPTZ`
* `outbox_events.created_at_ts TIMESTAMPTZ`
* `outbox_events.updated_at_ts TIMESTAMPTZ`
* `outbox_events.next_attempt_at_ts TIMESTAMPTZ`
* `outbox_events.completed_at_ts TIMESTAMPTZ`
* `outbox_events.lease_expiry_ts TIMESTAMPTZ` (mirrors `lease_expiry`)
* `asset_locations.created_at_ts TIMESTAMPTZ`
* `asset_locations.updated_at_ts TIMESTAMPTZ`
* Any additional `_at`/`_expiry` `TEXT` columns on the PG media tables
  discovered at generation time may be included; the list above is the
  minimum viable hot path.

Each is:

```sql
ALTER TABLE <tbl> ADD COLUMN <col>_ts TIMESTAMPTZ;
UPDATE <tbl> SET <col>_ts = NULLIF(<col>, '')::timestamptz WHERE <col> <> '';
CREATE INDEX CONCURRENTLY IF NOT EXISTS ... (see § 5.3);
```

The file is idempotent: guarded with `DO $$ IF NOT EXISTS (SELECT 1 FROM
information_schema ... ) THEN ... END $$;`.

### 5.2 Dual-write window

The writer (`internal/platform/postgres/media/committer.go`) performs a
**single-transaction dual-write** (expand phase):

```go
// Expand phase: bind once, write both columns.
// TEXT column: original string. _ts column: NULLIF($N,'')::timestamptz.
```

Readers prefer `_ts` when present, falling back to `TEXT` for rows written
before the cutover — no long-lived double-write in Go, just one
transactional statement that touches both columns.

### 5.3 Indexes

Added in the same migration (planner-statistics friendly):

* B-tree on `media_assets.created_at_ts`, `updated_at_ts` for range scans.
* B-tree on `outbox_events.next_attempt_at_ts`, `created_at_ts` for the claim
  window (`status, next_attempt_at` historically `TEXT`; now `TIMESTAMPTZ`).
* BRIN on `media_assets.created_at_ts` for very-large append-mostly tables
  (selectively, gated behind `CREATE INDEX IF NOT EXISTS ... USING BRIN`).

## 6. Wiring `004`

`004` must be:

* embedded as `MediaTimestampsTimestamptzDDL` in `migrations/postgres/embed_ddl.go` (`//go:embed 004_media_timestamps_timestamptz.sql`),
* appended to the canonical runner lists in `internal/platform/postgres/media/testmain_test.go::applyMediaMigrations` and `internal/platform/postgres/media/backfill.go::RunMediaBackfill` (fourth element after `003`).

## 7. SMALLINT → BOOLEAN — opportunistic `005_media_booleans.sql`

File `migrations/postgres/005_media_booleans.sql` is a **skeleton**: it is
created with `DO NOT APPLY YET` header comments and a placeholder example on a
single boolean surface (e.g. `asset_locations.is_primary`). It is NOT embedded
in `embed_ddl.go` and NOT added to runner lists until the owning table is
mutated for another reason. No sprint owns it until that mutation happens.

## 8. Pgvector consolidation verification

Proved structurally before this plan ships (see addendum § 10 of BASELINE_INVENTORY.md):

* `media_embedding_families` registry + `media_embeddings_validate_family()` trigger (from `002`) gate every `media_embeddings` write.
* Production families `text/intfloat/multilingual-e5-base 768` and `visual/google/siglip-so400m-patch14-384 1152` carry distinct partial HNSW `USING hnsw ((embedding::vector(N)) vector_cosine_ops) WHERE ...`.
* `PostgresIndexWorker` (`internal/platform/postgres/media/outbox_worker.go`) — `asset.index.requested` PG outbox → `media_embeddings` pgvector upsert → `INDEXED` — is the canonical post-cutover index plane; `make certify-media-cutover` pins it.

## 9. Acceptance

This design is accepted when:

* `docs/migrations/BASELINE_INVENTORY.md` and `docs/migrations/BASELINE_PLAN.md` are present and consistent.
* `migrations/postgres/004_media_timestamps_timestamptz.sql` lands idempotently.
* `migrations/postgres/005_media_booleans.sql` skeleton is present and explicitly not wired.
* `migrations/postgres/embed_ddl.go` reflects the new `004`.
* `go vet ./...` and `go run ./cmd/archcheck --strict` are green.
* `go test ./internal/platform/sqlite -run TestMigrations_Smoke_Baseline -count=1` stays green.
* The `architecture/catalog.yaml` media-cutover demolition entry remains green (structural gates `percheck_media_assets_writer_canonical` still `PASS`).

The `000_baseline_267.sql` dump and the runner baseline-skip logic (§ 3 B) are
deferred to the next incremental PR after review of this design so that the
museum is never broken atomically.
