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
idempotent) — all three iterate the ONE ordered list
`embed_ddl.go::MediaSelfBootstrapDDL` (001→009) instead of spelling their
own, so no runner can apply a silent prefix of the sequence (audit point 9:
with `CREATE TABLE IF NOT EXISTS` everywhere, application ORDER is part of
the contract — only 009's DROP converges a database that already carries the
retired face columns, so 009 must be applied last):

* `internal/platform/postgres/media/testmain_test.go::applyMediaMigrations`
* `internal/platform/postgres/media/backfill.go::RunMediaBackfill`
* `cmd/admin/internal/backfill/media_enrichment.go` (before this contract it
  applied only 001–003+009, leaving a fresh database without the 004 `_ts`
  columns the dual-writers write and without the 006–008 surfaces)
* (`internal/app/wiring/media/database.go` idempotently guards `007` alone at
  the connection boundary — a targeted guard, not a bootstrap.)

New migrations added to `migrations/postgres/` must be embedded in
`embed_ddl.go` AND appended to `MediaSelfBootstrapDDL`; the order contract is
pinned by `migrations/postgres/embed_ddl_contract_test.go`. Deliberate
exceptions: `005_media_booleans.sql` (skeleton, unwired per BASELINE_PLAN
§7) and `010`/`011` (deployment-managed one-shots — 010 is NOT idempotent,
`DROP`/`RENAME` have no `IF EXISTS`, and cannot live in a re-runnable
self-bootstrap list; 011 follows the same policy).

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

## Twin declarations (gemellarità) — audit point 7

Nine table families are declared **twice by hand** — canonical on the primary
plane, re-written as a self-contained copy inside `migrations/sqlite_jobs/` so
the jobs database can boot without the media plane — and `outbox_events` is
declared a **third** time on the PostgreSQL media SSOT:

| Table | Primary (canonical) | Jobs plane (copy) | PostgreSQL (3rd surface) |
|-------|--------------------|-------------------|--------------------------|
| `artifact_stages` | `147` (+ baseline) | `001_jobs_plane.sql` | — |
| `job_checkpoints` | `216`/`272`/`273` (+ baseline) | `001_jobs_plane.sql` | — |
| `preparation_units` | `242`/`244` | `001_jobs_plane.sql` | — |
| `preparation_job_units` | `243`/`245` | `001_jobs_plane.sql` | — |
| `preparation_dependencies` | `244` | `001_jobs_plane.sql` | — |
| `preparation_claim_snapshots` | `246`/`248` | `001_jobs_plane.sql` | — |
| `preparation_attempts` | `247`/`249` | `001_jobs_plane.sql` | — |
| `job_results` | `119` (+ baseline) | `001_jobs_plane.sql` | — |
| `outbox_events` | `092`/`186` (+ baseline) | `001_jobs_plane.sql`/`003` | `001_media_schema.sql` |

There is no generated DDL source: the copies are hand rewrites, and hand
rewrites drift (the `job_checkpoints` disappearance of § 272 is the recorded
instance). The **executable shared source** is
`internal/platform/sqlite/migrations_twin_parity_test.go`: it applies both
SQLite planes to fresh databases, reads the live schemas the writers see, and
requires parity except for the pinned divergence set below. Any new divergence
must be converged on both planes in the same change, or pinned with rationale
here and in the test.

**Pinned divergences** (each deliberate; see the test for the exact pin list):

* **CHECK constraints**: the primary copy carries the FASE-3/Preparation-Fabric
  value-set CHECKs (`artifact_stages.requirement/state`,
  `preparation_units.state/resource_class/speculation_level/cost_class/result_kind`,
  `preparation_dependencies.dependency_kind`,
  `preparation_attempts.execution_mode/status`); the jobs copy drops them.
  SQLite cannot add a CHECK via ALTER, so converging the jobs copy means a
  table rebuild; both planes enforce the same value sets in the application
  layer (typed state enums).
* **DEFAULT expressions**: `artifact_stages.created_at/updated_at` and
  `job_results.created_at` keep their historical defaults on the primary copy
  only; the jobs copy states them without defaults because the shared writers
  always set timestamps explicitly.
* **Column order**: compared as a set, not as a sequence (`preparation_job_units`
  appends its extension columns via ALTER on primary while the jobs copy
  inlines them). Writers use named columns.
* **Non-unique indexes**: not compared — per-plane access patterns legitimately
  differ (e.g. primary `idx_job_checkpoints_job` serves resume/audit lookups;
  the jobs copy is PK-only because every live query is PK-covered). A new
  jobs-plane query on `(job_id, completed_at)` must add the index to the jobs
  plane in the same change.
* **UNIQUE indexes**: hard shape parity (unique + partial + columns), names
  excluded. Uniqueness is correctness — writer `ON CONFLICT` clauses bind to
  it.

**Drift found and repaired by this audit**: `outbox_events.event_key`
uniqueness is PARTIAL (`WHERE event_key != ''`) everywhere the contract is
written down — `Repository.Enqueue`'s conflict target, the jobs copy
(`ux_jobs_outbox_event_key ... WHERE event_key <> ''`), and the PG copy
(`001_media_schema.sql`, commented "Partial unique index (SQLite parity
intent)"). Only the primary copy (`092` + baseline) declared a FULL unique
index while its own comment claimed the partial semantics: with it, SQLite
silently absorbed the second empty-key one-shot insert through the
`ON CONFLICT DO NOTHING` writers and hard-failed it through the plain-INSERT
writer (`artifact_stages.InsertWithOutbox`) — identical shared code behaving
differently per plane. Repaired by
`migrations/sqlite/285_outbox_event_key_partial_unique.sql` (forward migration,
same DROP-then-CREATE idiom as `273`); the pinned behaviour is reproduced by
`TestMigrations_TwinOutboxEventKeyContract` on both planes.

## media_assets mirror fields (campi specchio) — audit point 8

`media_assets` carries live mirror fields. Per-pair inventory and decision
(canonical owner first; evidence = live non-test call sites):

| Pair | Canonical | Mirror | Status / decision |
|------|-----------|--------|-------------------|
| thumbnail | `thumbnail_url` (PG reader prefers `COALESCE(NULLIF(thumbnail_url,''), thumb_url)`; model field `ThumbnailURL`) | `thumb_url` (PG committer writes `UPDATE … SET thumb_url`; SQLite clip reads `COALESCE(thumb_url,'')`) | **Both live**, with a write/read asymmetry (the writer targets the fallback column). `thumbnail_url` is canonical; converging the writer and contracting `thumb_url` is a dedicated follow-up (needs its own expand→contract pair). |
| url family | `drive_link` (233 call sites), `download_link` (61), `clip_page_url` (`Asset.ClipPageURL`, written via the PG committer) | `url` (legacy generic; only read inside `COALESCE` fallback chains in `imagesrepo`) | All four live in some path; `url` is the fallback-of-last-resort. No contract needed now. |
| tags | `tags` (JSON string array) | `tags_norm` (derived space-joined lowercase search string, written by the same committer) | **Intentional denormalization** (search projection), not drift. |
| search | `search_text` (full-text body, 177 call sites) | `search_terms` (JSON keyword array) | Two different facts, not mirrors — recorded here to stop the conflation. |
| location | `asset_locations` table (canonical; the MediaCommitter writes it) | `media_assets.asset_location` column | The column has **no live Go reader or writer** (only `asset_locations`-plural code matches) — dead mirror. 232 already removed the same duplication from the voiceover tables. Candidate for a dedicated DROP contract migration (follow-up, godlike/07 one-PR demolition). |
| digests | `content_sha256` (content address), `binary_sha256` (byte identity) | pre-229 mixed bucket `file_hash` | See digest history below. |

**Digest history** (cleanups 229/232/233 and contracts 281/282/010 verified):
229 (EXPAND) copied `file_hash` → `legacy_file_md5` on 9 tables without
dropping anything; 281 (CONTRACT, SQLite) reconciled valid 64-hex values into
`content_sha256`, renamed the column to `content_sha256` on 6 tables and
dropped `legacy_file_md5` from `media_assets`/`asset_index`; 282 did the same
for `stock_source_cache`; PG `010` mirrors 281 (`011` adds `content_sha256`
to `asset_versions`). The 229-added `voiceovers.legacy_file_md5` vanished in
the 231 table rebuild (no residue). **Residual**: the original `file_hash`
columns survive on 8 tables —229's promised final DROP never landed. It is a
MIXED family: live on `voiceovers` and `asset_subtitle_artifacts` (their own
artifact digests, actively written), dead on `media_assets`/`asset_locations`/
`asset_versions` (only stale comment references remain, e.g.
`stock/enrichment/idempotency.go`). Decision: per-table contract migration in
a dedicated PR — not bundled here (minimum blast-radius; the audit asks for
the decision, not the demolition).

**search_terms format ambiguity (070 `DEFAULT ''` vs 091's `DEFAULT '[]'`
claim vs 097 `DEFAULT ''`) — resolved**: the stored form is a **JSON array of
strings**; `''` (the actual schema default) and `'[]'` are both valid and both
mean "no terms"; malformed legacy JSON degrades to "no terms" on read and
writers leave the column untouched for an empty list. The single owner is
`internal/kernel/asset/detail/search_terms.go`
(`EncodeStringArrayColumn`/`ParseStringArrayColumn`) with the contract test
`search_terms_test.go`; the three hand-copied codecs (`app/wiring`
`encodeStringSliceJSON`, `postgres/media` `clipTagsJSON` and `decodeTagsJSON`)
now delegate to it instead of re-implementing the format. The ambiguity
survived three migrations because the documentation went stale, not the code:
091's header points at `internal/domain/asset/search_terms.go` (deleted in a
later refactor) and at `canonical.go`'s `'[]'` default (no longer declared),
and `search_terms.go` itself kept documenting helpers deleted in the media
read-plane demolition.

## 004 TIMESTAMPTZ expand — audit point 9 decision (EXPAND → CONTRACT)

State, measured against the live code (not the plan): `004` completed
EXPAND + BACKFILL + INDEX; the app-layer dual-write is complete — every
writer sets the TEXT column and its `_ts` sibling in the same statement/tx
(committer, mutations, delete_saga, enrich_state_store, folder_repository,
aux_ports). The **read** side is not: 3 query sites read `_ts` (the
enrichment candidate reader, with `COALESCE(_ts, '-infinity')` fallback) and
**17 query sites still read the TEXT siblings** (`ORDER BY created_at /
updated_at` in asset_store, dedup_group_reader, embedding_backfill_readers,
folder_repository, media_clip_resolver, media_local_search,
media_sourcing_lookup, …).

**Decision: keep the dual-write EXPAND window open — do NOT contract yet.**
Both audit options ("completare il CONTRACT" / "ritirare le colonne TEXT")
end by dropping the TEXT columns; the census shows that would break 17 live
read paths today. BASELINE_PLAN §5.2 already pins the sequence — contract
"only after the read path has flipped to *_ts (the next increment after this
file)" — and the shim-removal discipline (§4) requires such steps in
dedicated PRs, never bundled.

Exit criteria for the contract phase (then a dedicated migration may drop
the TEXT columns and remove the dual-write):

1. flip the remaining 17 read sites to the `_ts` columns with the
   `COALESCE(_ts, <TEXT sibling>)` fallback shape used by
   `enrichment_candidate_reader.go` (pre-cutover rows have `_ts` NULL);
2. one support window with no TEXT-side reads;
3. contract migration (DROP the TEXT columns) + removal of the dual-write
   setters, in one dedicated PR.

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

## Museum residues — audit point 10 (verdicts)

### Missing version prefixes + squashed baseline

The gap list (002–007, 038–050, 071–088, 125/126, 150, 181, 253–259, 261) is
**intentional** — renumbered or removed historical migrations, logged as
warnings by `migrations_discovery.go::warnOnGaps` and treated informationally.
`000_baseline_267.sql` is shipped and wired: fresh databases bootstrap from
the single dump and skip `001..267`; old databases keep their per-version
rows and skip the baseline (both paths exercised by
`TestMigrations_Smoke_Baseline`, green). The runner records the baseline as
the **version-0 sentinel** (option A of BASELINE_PLAN § 3 — see the as-built
amendment there); no action needed.

### Checksum shim families (198, 195/201, 186, 109, legacyCommentChecksums,
legacyChecksums 191–193, + the 253 gate, the 262 skip and the 238→239
reconcile)

**Decision: keep all of them until BASELINE_PLAN § 4 Phase 3**, then remove
in dedicated follow-up PRs (§ 4's own rule: "never bundled with a feature
slice"). They are not dead weight: each family reconciles the SHA-256 ledger
identity of an ALREADY-DEPLOYED database (the shim keys are exact historical
hashes, so arbitrary edits still fail closed), and each is pinned by a
dedicated test — `migrations_198_test.go` (198),
`migrations_ledger_test.go` (legacy-ledger certification 194/197, 238→239
reconcile AND rejection of unknown historical checksums),
`migrations_verify_rules_test.go` (253 recorded-drop-without-file, 262
execution-cutover skip). Phase 3 requires "one support window later" after
Phase 2 (baseline as default path — shipped); no doc declares that window
closed, so removal today would strand every very-old database mid-upgrade.
When Phase 3 is declared, the removal PR deletes the shim maps, the gates and
their tests together — the inventory table above is the checklist.

### Quarantine `legacy_*` tables (263, 264, 265, 267 + out-of-band archival)

**Decision: KEEP — do not drop definitively (yet).** Grounds:

* the quarantine headers state explicit retention intent — 263: "Preserve
  historical rows for audit/recovery"; 265: "Keep the historical primary
  schema readable during rolling upgrades"; 267 preserves "under explicit
  recovery names". No documented expiry window exists for that data (the
  support window of BASELINE_PLAN § 4 covers migration FILES, not rows), and
  a DROP is irreversible — the decision to destroy must come from a dated
  retention decision, not from an audit cleanup.
* The harm class these tables CAN cause — the index-name theft of the 272/273
  incident (a renamed table keeps its indexes under canonical names, so a
  later `CREATE INDEX IF NOT EXISTS` silently no-ops) — is already
  **neutralized**: `migrations_verify.go::verifyDeclaredTables` reports at
  every boot any declared index that is missing or owned by another table
  (deliberately non-fatal so drifted DBs still boot), the 273 repair idiom
  (unconditional DROP then CREATE under the canonical name) is documented for
  forward repairs, and `migrations_verify_rules_test.go` pins the
  stolen-index shape end to end. The declared-tables suite is silent on a
  clean database (verified green).
* Drop trigger (recorded so the decision is revisitable): when the support
  window closes (BASELINE_PLAN § 4 Phase 4 decision — "the oldest supported
  baseline is 267"), a dedicated migration may DROP the `legacy_*` tables of
  263/264/267 and the out-of-band archival copies in one PR, after a data
  owner signs off the audit/recovery retention.

Inventory: 101 `RENAME TO legacy*` statements — 263 (3 tables, primary),
264 (7 cache tables, primary), 267 (~91 `legacy_observability_*`,
observability plane) — plus the out-of-band archival copies on primary
(`legacy_job_checkpoints` et al., see the 272/273 story), whose finite
in-scope set is `migrations_verify.go::executionPlaneArchivedTables`.

## Why this file exists

Once `BASELINE_PLAN.md` is approved and `000_baseline_267.sql` lands, this
inventory becomes the archived museum manifest. The historical files `001`
through `267` will be kept for one support window before archival removal so
that very-old databases can still validate against it.
