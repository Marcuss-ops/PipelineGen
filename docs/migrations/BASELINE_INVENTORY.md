# Baseline Inventory Snapshot — 2026-09-09

> Purpose: freeze inventory of the PipelineGen migration museum before the
> baseline cutover described in BASELINE_PLAN.md. Every count here is a
> directly observable fact from `ls refactored/migrations/**/*`.

## Counts

| Surface | Directory | Files | Highest version prefix |
|---------|-----------|-------|------------------------|
| SQLite primary + observability + cache (canonical SSOT for non-media) | `migrations/sqlite/` | **224** `*.sql` files (**218** at the freeze; `268`–`272` added after) | `272` (`272_job_checkpoints_restore.sql`) |
| SQLite jobs plane (isolated `jobs/jobs.db.sqlite`) | `migrations/sqlite_jobs/` | **6** files | `006` (`006_job_deferral.sql`) |
| PostgreSQL + pgvector media SSOT (`pipelinegen_media`) | `migrations/postgres/` | **3** files (+ 1 `embed_ddl.go` bridge) | `003` (`003_media_hnsw_indexes.sql`) |

Total distinct version prefixes across SQLite: `218` (see raw list below).
Postgres uses semantic names (`001_media_schema.sql` etc.) rather than bare
version integers.

## SQLite version prefixes — raw enumeration

```
001 003 008 009 010 011 012 013 014 015 016 017 018 019 020 021 022 023 024
025 026 027 028 029 030 031 032 033 034 035 036 037 051 052 053 054 055 056
057 058 059 061 062 063 064 065 066 067 068 069 070 089 090 091 092 093 094
095 096 097 098 099 100 101 102 103 104 105 106 107 108 109 110 111 112 113
114 115 116 117 118 119 120 121 122 123 124 127 128 129 130 131 132 133 134
135 136 137 138 139 140 141 142 143 144 145 146 147 148 149 151 152 153 154
155 156 157 158 159 160 161 162 163 164 165 166 167 168 169 170 171 172 173
174 175 176 177 178 179 180 182 183 184 185 186 187 188 189 190 191 192 193
194 195 196 197 198 199 200 201 202 203 204 205 206 207 208 209 210 211 212
213 214 215 216 217 218 219 220 221 222 223 224 225 226 227 228 229 230 231
232 233 234 235 236 237 238 239 240 241 242 243 244 245 246 247 248 249 250
251 252 260 262 263 264 265 266 267 268 269 270 271 272 273
```

`268`–`273` are the post-freeze additions (the museum manifest above stops at
`267`); `272` and `273` are the drift repairs described below.

## Gaps (version integers absent from `migrations/sqlite/`)

These gaps are **intentional** — they correspond to migrations that were
renumbered or removed during historical refactoring (see
`internal/platform/sqlite/migrations_discovery.go::warnOnGaps` which logs
them as warnings, not errors).

```
002 004 005 006 007 038 039 040 041 042 043 044 045 046 047 048 049 050 060
071 072 073 074 075 076 077 078 079 080 081 082 083 084 085 086 087 088 125
126 150 181 253 254 255 256 257 258 259 261
```

The runner treats gaps informationally; `validateAppliedMigrationSet` only
fails closed on a gap when a later version is recorded while an earlier
in-scope version is missing.

## Active checksum shims in `internal/platform/sqlite/migrations.go`

These are the narrow historical reconciliations kept alive to preserve the
SHA-256 ledger invariant for already-deployed databases. They are candidates
for removal once the baseline is adopted (see BASELINE_PLAN.md § Shim removal).

| Shim key | Versions involved | Reason |
|----------|-------------------|--------|
| `198` ledger fix | `198` canonical `control_plane_meta` singleton form vs legacy `database_id` PK | `isLegacyControlPlaneMetaSchema` gate |
| `201` missing file | `201` `media_registry_taxonomy` deployed but file removed | `hasMediaTaxonomyColumns` gate |
| `195` likewise | `195` same taxonomy family | same gate |
| `186` local checksum | `186` `outbox_events` priority `aaa…` vs `06d…` | exact-hash pair |
| `109` `-- database:` header | `109` added `TODO-8-SCOPE-FLAG-RECONCILE-109` marker + scope line | marker-guarded header addition |
| `legacyCommentChecksums` | 30 versions (`037,068,089,090,091,092,094,095,096,097,098,102,105,108,120,123,129,137,148,151,154,155,158,159,160,162,163,169,170,230`) | stale package-path comment edits — SQL identical |
| `legacyChecksums` | `191,192,193` | removed historical migrations — schema already present |

Additionally:

* `internal/platform/sqlite/migrations_discovery.go::validateAppliedMigrationSet` accepts the historical `253_drop_assembly_sessions.sql` ledger identity when `assembly_sessions` is already gone.
* `internal/platform/sqlite/migrations_discovery.go::skipMigrationAfterExecutionCutover` skips `262` when the execution-plane quarantine (`265`) already removed `jobs`.
* `internal/platform/sqlite/migrations_reconcile.go::reconcileHistoricalMigrationIdentities` maps ledger `238 → 239` (observability `run_resource_reports`) on exact hash + live-table gate.

## PostgreSQL embed DDL state

`migrations/postgres/embed_ddl.go` re-exports three compile-time `//go:embed`
strings:

* `MediaSchemaDDL` → `001_media_schema.sql` — transactional core (`media_assets`, `asset_locations`, `outbox_events`, `media_asset_sources`, `registry_events`, `asset_text_tracks`, `asset_renditions`, `asset_text_track_segments`).
* `MediaVectorSurfacesDDL` → `002_media_vector_surfaces.sql` — derived surfaces (`media_asset_features`, `media_embedding_families`, `media_embeddings` + `media_embeddings_validate_family()` trigger, GIN + FTS indexes). `media_asset_features` carries the two measurables of the enrichment pipeline (`dominant_color`, `motion_score`); the face descriptors it used to declare are RETIRED by `009`.
* `MediaHNSWIndexesDDL` → `003_media_hnsw_indexes.sql` — production ANN (`media_embedding_families` rows `text/intfloat/multilingual-e5-base 768` + `visual/google/siglip-so400m-patch14-384 1152` + per-family `USING hnsw ((embedding::vector(N)) vector_cosine_ops) WHERE ...` partial indexes).
* `MediaDropAssetFacesDDL` → `009_drop_media_asset_faces.sql` — retires the face descriptor columns (`has_faces`, `face_count`, `largest_face_ratio`) from `media_asset_features`. It is applied AFTER `002` because the `CREATE TABLE IF NOT EXISTS` there cannot converge a database that already carries the columns. The endpoint the descriptors depended on (`POST /detect_faces`) is served by no service, so requiring it made the whole features row unproducible.

Canonical self-bootstrapping call sites (each `IF NOT EXISTS` so re-exec is
idempotent):

* `internal/platform/postgres/media/testmain_test.go::applyMediaMigrations`
* `internal/platform/postgres/media/backfill.go::RunMediaBackfill`
* `cmd/admin/internal/backfill/media_enrichment.go`

New migrations added to `migrations/postgres/` must be embedded here and
appended to each runner site; the baseline plan introduces `004` and `005`
following this contract.

## SQLite jobs-plane state

`migrations/sqlite_jobs/` intentionally contains only 6 files:

* `001_jobs_plane.sql` — `jobs` table + `idx_jobs_outbox_claim`.
* `002_operations_plane.sql` — `operations`-plane tables.
* `003_outbox_priority.sql` — `idx_outbox_events_status_priority_claim`.
* `004_jobs_payload_columns.sql` — payload column additions.
* `005_job_scheduling.sql` — deferred scheduling (`job_schedules`, `job_scheduler_counters`) + per-stage status (`job_stage_status`).
* `006_job_deferral.sql` — `jobs.deferred_until` + `idx_jobs_deferred_until`:
  the non-consuming deferral hint (`job.OutcomeDeferred`), i.e. a wait that does
  not spend the retry budget.

This surface is live but small; it is excluded from the primary 218-count and
from the freeze.

## Post-baseline drift repair (`272`)

Applied migrations are skipped by version, and their checksum is compared —
that guards the FILE, never the live schema. Observed on 2026-09-28:
`data/media/media.db.sqlite` carried a `schema_migrations` row for `216`
(`216_job_checkpoints.sql`) whose checksum matched the file byte-for-byte, while
`job_checkpoints` was absent from the database. Nothing noticed, so every durable
checkpoint write failed with `no such table` and resume silently fell back to the
best-effort path.

Two halves close it:

* `272_job_checkpoints_restore.sql` — forward repair (primary scope, idempotent
  `CREATE TABLE IF NOT EXISTS` + index), schema-identical to what `216` declares.
  Editing `216` cannot work: the ledger skips an applied file.
* `273_job_checkpoints_index_restore.sql` — the same session's second repair.
  `272` recreated the table with the canonical
  `CREATE INDEX IF NOT EXISTS idx_job_checkpoints_job`, and SQLite silently
  skipped it: index names are unique per DATABASE and that name belonged to
  `legacy_job_checkpoints`, the quarantined copy left by the out-of-band
  archival. `273` frees the name and re-creates the canonical index. The legacy
  copy cannot be re-indexed under a legacy-qualified name in SQL — the runner
  aborts on a failed statement, and `CREATE INDEX … ON legacy_job_checkpoints`
  fails wherever the archival never ran.
* `internal/platform/sqlite/migrations_verify.go` — boot-time check
  (`verifyDeclaredTables`) that every table AND index declared by an in-scope
  APPLIED migration exists in the live schema, and that a declared index is
  owned by the table that declares it (an index that exists under the right name
  on the wrong table is the silent-skip shape above). Gaps are logged at error
  level with the declaring file (the newest numbered declarer), its version, the
  target DB and the remedy. Deliberately non-fatal: a drifted database must still
  boot so the operator can apply the forward migration.

Only four things are ever excluded, and each is a stated reason rather than a
heuristic:

1. some in-scope migration removes the object LATER than it last declares it
   (order-aware, so a rebuild or a re-created table is not written off);
2. the object is on `executionPlaneArchivedTables` — the finite set of
   execution-plane tables the data-plane archival documented by `265` may have
   moved to the jobs database (every one of them is declared by
   `migrations/sqlite_jobs/`). `job_checkpoints` is deliberately NOT on that list;
   the primary runtime still opens its primary copy, so its absence is drift;
3. an APPLIED ledger row whose file is no longer in the corpus names a drop of
   the object (`253_drop_assembly_sessions.sql` is the live example: the ledger
   is the only surviving statement of intent);
4. the live schema has the object (trivially).

Contract tests: `internal/platform/sqlite/migrations_declared_tables_test.go`
(reported on both planes, silent on a clean DB, dropped-for-good tables not
reported) and `internal/platform/sqlite/migrations_verify_rules_test.go` (one
test per exclusion rule, each with its positive control, plus the stolen-index
shape reproduced end to end).

## Why this file exists

Once `BASELINE_PLAN.md` is approved and `000_baseline_267.sql` lands, this
inventory becomes the archived museum manifest. The historical files `001`
through `267` will be kept for one support window before archival removal so
that very-old databases can still validate against it.
