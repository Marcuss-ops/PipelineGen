# MEDIA LEGACY READ-PLANE DEMOLITION — SQLite reader classification

**Date:** 2026-09-20
**Milestone owner:** media-postgres-cutover
**Scope:** the 32 exact-file entries under `internal/platform/sqlite/` in
`cmd/archcheck/scan/boundaries/percheck_sqlite_media_reader_zone_inventory.go`
(the `sqliteMediaReaderInventoriedZoneFiles` register).

This is the WORKLIST for the read-plane half of the cutover. The Qdrant media
inventory (9 entries) and the `cmd/admin` inventory (17 entries) are tracked by
the same register and are handled by separate waves — see
`docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md` and
`architecture/catalog.yaml` (P2-9 Phase 2).

## Status after the 2026-09-20 pass

| Item | State |
| --- | --- |
| INDEXED writer canonical paths | **2 → 1 (done).** Only `internal/platform/postgres/media/`. The gate note now names `PostgresIndexWorker` as the sole owner; `clipindexer.setIndexedAt` is deleted and is only referenced as removed history. |
| Qdrant media compatibility seam | **Demolished in the working tree.** `indexing_api.go`, `indexing_api_persistence.go`, `indexing_hash.go`, `indexing_skip.go`, `vectorstore.go` and their tests are deleted; `IndexClip`/`IndexAsset` are pure delegators onto `pgmedia.ReindexRequester`. |
| SQLite media inventory | **32 → 25.** Seven rows executed: `clips_enrich_state.go`, `dedup_queries.go`, `clips_resolution.go` and the three `assets/operatorread/` rows (deleted, replaced by `pgmedia.OperatorInventoryReader`), plus `adminmedia_source.go` (parallel wave). 25 rows remain. |
| Qdrant media inventory | 9 → 2 (`asset_store.go`, `asset_store_fetch.go`); the clipindexer half is gone. |
| Admin media inventory | 17 → 9 (staged for the `cmd/admin` wave: 7 `internal/backfill/`, 1 `internal/cleanup/`, 1 `internal/drive/`). |
| Total inventoried readers | 58 → 36. |

Remaining SQLite worklist: **25 files — `imagesregistry` 15, `imagesrepo` 4,
`mediaregistry` 3, `controlplane` 1, `deletion` 1, `metadataexport` 1.**

## Feasibility recon — `imagesregistry`, `imagesrepo`, `qdrant` (2026-09-20)

The three slices below were reconned against the PostgreSQL DDL **before** any
code was moved, because a row whose media read needs a table PostgreSQL does not
declare is not a MIGRATE row — it is a schema-parity blocker wearing a MIGRATE
label. Evidence per slice:

| Slice | Files | Verdict | Evidence |
| --- | --- | --- | --- |
| `imagesregistry` | 15 | **Feasible now.** | Every file's media read is `media_assets`; PostgreSQL declares all ~35 columns the slice uses (media_assets in `001_media_schema.sql`), and the two auxiliary tables it touches — `clip_folders` (PG migration 007) and `outbox_events` (001) — exist too. `clips_list` / `clips_core` / `clips_queries` read like table names but are **prose in comments** (they name the pre-Wave-C source files), so they are not blockers. The one genuinely SQLite-only table is `api_requests` (`maintenance_repository.go:33`, `DELETE FROM api_requests`), and it is the *non-media half* of a SPLIT row — it stays on SQLite by design. |
| `imagesrepo` | 4 | **3 of 4 blocked on schema parity.** | `images_aggregate.go`, `images_generated.go` and `images_insert_update.go` join `retrieved_image_details` / `generated_image_details`. Those tables exist **only in SQLite** (migrations 117 / 124); `rg 'retrieved_image_details|generated_image_details' migrations/postgres/*.sql` returns nothing. Serving `ListImages` / `GetGeneratedDetails` / `GetRetrievedDetails` / `ListImagesByOrigin` from PostgreSQL therefore requires a PG migration + backfill for both tables first. `images_search.go` is the exception: pure `media_assets` reads, feasible now. |
| `qdrant/indexing` | 2 | **Feasible, one decision needed.** | PostgreSQL declares every column of the Qdrant `canonicalQuery` (35 columns: identity, taxonomy, the four embedding_json channels, youtube/workspace/license projections, semantic_hash), and `asset_text_tracks` exists (column is `text_content`, not SQLite's `text`). Two items need an explicit call, both recorded below. |

### The two Qdrant items that need a decision, not a port

1. **`asset_visual_summaries` has no PostgreSQL counterpart.** The Qdrant
   `canonicalQuery` resolves `current_semantic_hash` with the precedence
   `asset_visual_summaries.source_hash` ∪ `media_assets.semantic_hash` ∪ `''`
   (`asset_store.go`, and the airlock in `index_airlock.go` trusts the resolved
   value). The table exists only in SQLite (migration 151). A PostgreSQL port of
   that store must either (a) accept the fallback only — resolved = `NULLIF(semantic_hash, '')`
   — and document that the VLM fingerprint does not exist on the media SSOT, or
   (b) carry the table to PostgreSQL first. This is a real behavioural fork: with
   (a) an asset whose VLM pass produced a `source_hash` different from
   `semantic_hash` resolves differently than it does today.
2. **The Qdrant media store has three live consumers**, so this is a MIGRATE and
   not a DELETE: `runtime.go` (PayloadMapper / IndexWriter / `SetReindexVerifier`,
   plus `search.NewSearchAdapter(searcher, store, log)` which is wired as
   `VectorStorePort`), and `cmd/admin/reconcile/dr_qdrant.go:358`, whose SQLite
   read carries an explicit contradiction with the register: the comment there
   states the read is *"DELIBERATE and scope-limited to disaster recovery … NOT a
   media read path … explicitly listed as out of scope"*, while
   `percheck_sqlite_media_reader_zone_inventory.go` lists the same two files as
   media-read debt. One of the two records is wrong and something must give:
   either the store moves to the media SSOT (register entry → 0, DR verifier
   reads PostgreSQL) or the register entry gets a documented, dated exemption
   instead of a silent listing. Note the media write plane itself is already
   retired (`build_process_qdrant.go`: *"Qdrant media write plane retired —
   QdrantRuntime retained for the non-media IndexDeleteHandler path"*).

Current register composition, measured on `4e4fce441`: 36 entries =
`sqlite/assets/imagesregistry` 15 + `cmd/admin` 9 + `sqlite/assets/imagesrepo` 4
+ `sqlite/mediaregistry` 3 + `qdrant/indexing` 2 + `sqlite/controlplane` 1 +
`sqlite/deletion` 1 + `sqlite/metadataexport` 1.

## Why the register is the SSOT

The converted-zone inventory replaced three path prefixes with an exact-file
list, so from 2026-09-20 a **new** SQLite reader of `media_assets` anywhere under
`internal/` or `cmd/` fails
`percheck_sqlite_media_reader_ban` unless someone names it in a register.

Two pins make the register a ratchet rather than an allowlist:

1. **No ghosts** — `TestSQLiteMediaReaderGrandfatheredFilesExist` fails if an
   entry names a file that no longer exists.
2. **No staleness** — `sqliteMediaReaderRegisterEntryIsLive` fails if an entry's
   file no longer contains a SQLite-dialect (`?`-bound) read of `media_assets`.

Consequence for every row below: **the inventory entry must be deleted in the
same change as its consumer is migrated or removed.** A migration that leaves
the entry behind is a red gate, not a completed migration.

## Destinations (exactly three, per file)

| Verb | Meaning | Definition of done |
| --- | --- | --- |
| **DELETE** | The file's media read has no production consumer left (or its media half now lives in PostgreSQL). | File removed; inventory entry removed; obsolete tests removed; stale allowlist/archcheck references removed. |
| **MIGRATE TO PG** | The file's media read is live and the whole file serves media. | Consumers move to a narrow PostgreSQL port (`internal/platform/postgres/media/`): consumer → narrow media read port → `pgmedia` implementation — **never** consumer → generic `*sql.DB`. |
| **SPLIT** | The file mixes a legacy media read with useful non-media operational SQL. | The media read moves to a PostgreSQL port; only the operational (non-media) half stays on SQLite. |

`DELETE` is the terminal verb for a file whose *entire* content is the legacy
media read; `SPLIT` is correct whenever a non-media responsibility would
otherwise be destroyed by a deletion.

## Classification — all 32 files

Legend: **file** — destination — the media read — what survives / why.

### Slice 1 — `assets/imagesregistry/` (18 files)

| File | Verb | Media read | Notes |
| --- | --- | --- | --- |
| `asset_store.go` | MIGRATE TO PG | `Get`/`Save`/`Delete`/`List` projections over `media_assets` | The `AssetStoreSQLite` media facade; live via `root.Repos.Assets`. The canonical save/delete hooks already delegate to the committer, so only the read half is still SQLite. |
| `asset_store_batch.go` | MIGRATE TO PG | `BatchGetByIDs` (batched `MediaAssetColumns` read) + LRU cache | Pure media batch read, no non-media half. |
| `clip_list_queries.go` | MIGRATE TO PG | `ListClips`, `CountClips`, `LastUpdatedAtForTerm` | All three are `media_assets` aggregates. |
| `clips_enrich_state.go` | ~~MIGRATE TO PG~~ **DELETED (2026-09-20)** | `SetEnrichState`, `SetEnrichStateIfCurrent`, `GetEnrichState` | Executed. `pgmedia.MediaEnrichStateStore` already implemented the same three methods and is the only implementation wired (`build_bundles_domain_assets.go` resolves `EnrichRepositoryPort` from `enrichStateStoreFromCommitter`), so the SQLite methods had zero production and zero test callers. File removed, the two now-dead `UpdateMediaAssetEnrichState*` helpers removed with it, inventory entry removed. |
| `clips_index_state.go` | MIGRATE TO PG | `SoftDelete`, `SetIndexState`, `GetIndexState`, `DeleteClipByDriveLink` | Media lifecycle/index transitions. `SetIndexState` must keep refusing `INDEXED` (owned by `PostgresIndexWorker`). |
| `clips_queries.go` | MIGRATE TO PG | `Count(filter)` | Single media aggregate. |
| `clips_repository_queries.go` | SPLIT | `CountAll`, `CountIndexed`, `CountIndexable`, `List`, `StreamAssetIDs` | **Survives:** `CountPendingOutbox`, `CountDeadLetter` (`outbox_events` is operational, not media). |
| `clips_resolution.go` | ~~MIGRATE TO PG~~ **DELETED (2026-09-20)** | `ResolveByMediaAssetID`, `ResolveByYouTubeVideoID`, `ResolveByDriveFileID`, `ResolveByExternalProviderID`, `GetClipFolderByVideoID`, `GetByDriveFileID` | Executed. `pgmedia.MediaSearcher` held two of the four resolver methods already (`media_clip_resolver.go`); `ResolveByYouTubeVideoID` + `ResolveByExternalProviderID` were added there with the retired SQLite semantics (`yt_<videoID>_%` fan-out; `google_drive` → `drive_file_id`, otherwise `source` + the metadata `external_id` projection). The three consumers now read PostgreSQL: the artlist `processor.NewClipResolver` and the SceneTextGenerator `ClipAssetResolver` take `pgmedia.NewMediaSearcher(mediaDB)`, and `ClipSourceBuilder` already preferred it (its SQLite fallback was removed). The two non-resolver wrappers went with it: `GetByDriveFileID` was inlined at its single caller (`artifacts/source_catalog.go` now calls the canonical `GetClipByDriveFileID`) and `GetClipFolderByVideoID` had no callers. |
| `clips_statistics.go` | MIGRATE TO PG | `CountBySource`, `CountPersistedSince` | Media counters. |
| `dedup_queries.go` | ~~MIGRATE TO PG~~ **DELETED (2026-09-20)** | `FindByYouTubeVideoID`, `FindByLegacyFileMD5`, `FindBySourceURL`, `FindByName`, `FindDuplicatesByYouTubeVideoID` | Executed. The three live methods were reached only through `SourcingClipStoreAdapter`; the PostgreSQL equivalents already existed (`pgmedia.MediaSearcher.FindClipID{ByName,ByYouTubeVideoID,BySourceURL}`, wired before this pass as `SourcingClipStorePGAdapter`). `newSourcingClipStore` now returns nil — never the mirror — when the media handle is absent, `sourcing.youtube.Service.dedupCheck` fails closed on a nil port, and the SQLite adapter (`youtube_adapters_store.go`) + its constructor were deleted. `FindByLegacyFileMD5` and `FindDuplicatesByYouTubeID` already had zero callers (`DuplicateAssetIDsByYouTubeID` on `pgmedia.MediaDuplicateGroupReader` supersedes the latter). |
| `folder_queries.go` | SPLIT | `CountByFolderID` | **Survives:** `clip_folders` CRUD (`UpsertFolder`, `DeleteFolder`, `GetFolder`, `GetFolderByVideoID`, `ListByFolderID`, `ListByFolderPath`, `ListFolders`) — folders are not media rows. |
| `maintenance_repository.go` | SPLIT | `ScanLocalOrphans`, `ScanDriveOrphans` | **Survives:** `DeleteOldAPIRequests` (`api_requests`), `WALCheckpoint`, `IncrementalVacuum`, `FullVacuum` — SQLite operational maintenance. |
| `media_asset_mutations.go` | MIGRATE TO PG | `CheckAndIncrementMediaAssetVersion` read + `UPDATE media_assets` primitives | Media mutation primitives; the catalog records that only non-media mutation primitives were meant to survive, so this file must lose the media half. |
| `primitives.go` | SPLIT | `HardDeleteTx` parent read `SELECT 1 FROM media_assets` | **Survives:** the non-media child deletes and their `init()` allowlist (`asset_locations`, `asset_processing`, `asset_versions`). |
| `repo_queries.go` | MIGRATE TO PG | `assetRepositoryAdapter` (`detail.Repository`) + `listAssetsByFilter` | Media repository adapter. |
| `search_queries.go` | MIGRATE TO PG | `SearchClips`, `SearchClipsByKeywords`, `SearchClipsAdvanced`, `SearchStockByKeywords` | Media search. Note: `ScanClipsByKeywords` has **zero** production references — drop it in the same change. |
| `source_version.go` | MIGRATE TO PG | `SourceVersionFor` (`media_assets.file_hash` / `source_version`) | Consumed through `detail.SourceVersionQuerier`; `pgmedia.MediaAssetSourceReader` is the nearest existing port (it exposes `AssetSource` today, so the fingerprint needs either a new method on it or a sibling reader). |
| `store_helpers.go` | SPLIT | `MediaAssetColumns` / `SoftDeleteFilter` / `buildMediaAssetQuery` / `FindByPHash` / `MarkUsed` | **Survives:** `buildClipFolderQuery`, `GetFolderChildren`, `clipSearchColumns` (folders). Also: every media query builder in the slice imports this file, so it can only be emptied **last**. |

### Slice 2 — `assets/imagesrepo/` (4 files)

| File | Verb | Media read | Notes |
| --- | --- | --- | --- |
| `images_aggregate.go` | MIGRATE TO PG | `ListImages` joins `media_assets` + `retrieved_image_details` / `generated_image_details` | The detail tables are image-extension rows keyed by `media_assets.id`, so the join follows the media row to PostgreSQL. |
| `images_generated.go` | MIGRATE TO PG | `ListImagesByOrigin`, `GetGeneratedDetails`, `GetRetrievedDetails` | Same keying as above. |
| `images_insert_update.go` | SPLIT | `assetIdentityByHash`, `UpdateEmbeddingData` metadata read, `AddImage` write path | **Survives:** `GetSubjectBySlugOrAlias` / `CreateSubject` (`subjects` is taxonomy, not media). |
| `images_search.go` | MIGRATE TO PG | `GetImageByHash`, `GetByID`, `Delete`, `GetByDriveFileID`, `ListImagesBySubject`, `ListAll` | Pure media reads. |

### Slice 3 — `assets/operatorread/` (3 files) — **EXECUTED 2026-09-20**

Deleted as a package (6 files: the three registered rows plus `repository.go`,
`state_projection.go`, `repository_test.go`); the three register entries went in
the same change. The read plane is `pgmedia.OperatorInventoryReader`
(`operator_inventory_reader.go` = surface + facet arm,
`operator_inventory_queries.go` = every SELECT and the single scan site), wired
in `registry_operator_admin_api.go` from the canonical media handle.

| File | Verb | Media read | Outcome |
| --- | --- | --- | --- |
| `detail_query.go` | MIGRATE TO PG | `get`, `getItem`, `loadMetadata` (`media_assets m` + metadata) | `get` / `getItem` / `listLocations` / `listProcessing` / `listOutboxEvents` / `loadMetadata`. `listLocations` / `listProcessing` / `listOutboxEvents` read non-media child tables but are only reachable through the media asset id, so they moved with the read. |
| `facets_query.go` | MIGRATE TO PG | `facets`, `runAssetStateFacetQuery` | `facets` + `runFacetQuery` / `runAssetStateFacetQuery` / `mergeCanonicalFacet` / `facetsFromMap`. |
| `list_query.go` | MIGRATE TO PG | `list`, `countList` (`listBaseSQL` / `listCountSQL`) | `list` / `countList` / `scanOperatorItems` — one projection, one scan site for both arms. |

Two divergences were forced by the engine change, not chosen:

1. **`LIKE` had to become `ILIKE`.** SQLite's `LIKE` is case-INsensitive for
   ASCII by default, PostgreSQL's is case-sensitive, so a verbatim port would
   have silently broken Content Library search ("beluga" would stop matching
   "Beluga underwater"). The live-PG test pins the case-insensitive behaviour.
2. **`last_error` cannot come from `media_assets.error`** because PostgreSQL
   does not declare that column at all. It now reads the live signal,
   `metadata_json.$.last_index_error`, which `pgmedia.SetIndexState` maintains
   (`mutations.go`) and which the SQLite committer surface maintained too.

Coverage: 13 live-PostgreSQL tests port the retired SQLite
`repository_test.go` properties (soft-deleted rows never surface, READY
asset-state and STALE index-health stay orthogonal, limit+1 pagination
partitions without repeats, facet counts exclude soft-deleted rows while every
canonical enum member still appears at count 0, `NULL started_at` stays nil,
and absent/soft-deleted ids return `(nil, nil)`).

### Slice 4 — `mediaregistry/` (3 files)

| File | Verb | Media read | Notes |
| --- | --- | --- | --- |
| `canonical_identity.go` | SPLIT | `ResolveContent` (`SELECT id FROM media_assets`) | **Survives:** `ResolveSource` + `Backfill` / `BackfillTaxonomy` over `media_asset_sources` (the canonical identity ledger). |
| `content_link_backfill.go` | MIGRATE TO PG | `BackfillContentLinks`, `BackfillContentSHA256` (`media_assets.content_sha256` scope) | One-shot backfills; classify as MIGRATE while they are still callable, DELETE if the backfill is declared complete. |
| `ledger.go` | SPLIT | `ReadCounts` (`media_assets`) | **Survives:** `registry_events`, `registry_runs`, `projection_registry`, `backup_registry`, `asset_text_tracks` — the operational registry ledger. |

### Slice 5 — `metadataexport/` (1 file)

| File | Verb | Media read | Notes |
| --- | --- | --- | --- |
| `asset_resolver.go` | SPLIT | `ResolveAssetIDs` (`outbox_events`), `LoadTechnicalSection`, `LoadProvenanceSection` (`media_assets`) | **Survives:** `LoadDeliverySection` (`delivery_log`). Wired via `sqmetadataexport.NewSQLiteAdapter(dbs.DualPool.Writer)` in `build_outbox_handlers.go`. |

### Slice 6 — `controlplane/` (1 file)

| File | Verb | Media read | Notes |
| --- | --- | --- | --- |
| `verifier.go` | SPLIT | `Verify` asset/CAS/broken-link counts (`media_assets`) | **Survives:** the rest of the control-plane health check (`schema_migrations`, `sqlite_master`, `jobs`, `outbox_events`, `content_objects`, `registry_runs`, `registry_events`, `projection_registry`, `performance_*`). |

### Slice 7 — `deletion/` (1 file)

| File | Verb | Media read | Notes |
| --- | --- | --- | --- |
| `stuck_row_scanner.go` | MIGRATE TO PG | `ListStuckRows` (deletion-chain states on `media_assets`) | The reconciler's stuck-row source. |

### Slice 8 — `artlist/` (1 file)

| File | Verb | Media read | Notes |
| --- | --- | --- | --- |
| `adminmedia_source.go` | ~~MIGRATE TO PG~~ **DELETED (2026-09-20)** | `ListSoundEffects` (sound-effect projection) | Executed by the parallel wave in the same tree (file + inventory entry removed). |

### Tally

| Verb | Count |
| --- | --- |
| DELETED this pass | 7 (`clips_enrich_state.go`, `dedup_queries.go`, `clips_resolution.go`, the three `operatorread/` rows, `adminmedia_source.go`) |
| MIGRATE TO PG remaining | 17 − 3 (`operatorread/`) = 14 |
| SPLIT remaining | 11 |
| **Remaining** | **25** |

These rows were the ones whose PostgreSQL counterpart already existed, so
the change was consumer rewiring rather than new SQL: `clips_enrich_state.go`
(`pgmedia.MediaEnrichStateStore`), `dedup_queries.go`
(`SourcingClipStorePGAdapter`), `clips_resolution.go` (`pgmedia.MediaSearcher`
resolver — two methods added, same semantics), `adminmedia_source.go`.

Every remaining file was verified per-symbol to have at least one live
production reference, so it can only leave via MIGRATE or SPLIT. `DELETE`
otherwise becomes reachable only as the terminal step of a MIGRATE/SPLIT row,
once the media half is gone and nothing else in the file remains.

## Execution order

1. **`imagesregistry` — sub-wave A (already-existing PostgreSQL ports, rewire only):**
   ~~`clips_enrich_state.go`~~ (done), ~~`dedup_queries.go`~~ (done),
   ~~`clips_resolution.go`~~ (done). The change is consumer rewiring + inventory
   entry deletion, with no new SQL: each had a `pgmedia` implementation already
   (`MediaEnrichStateStore`, `SourcingClipStorePGAdapter`, `MediaSearcher`).

   **Correction (2026-09-20 recon): `search_queries.go` is NOT in this group.**
   A name-level check against `internal/platform/postgres/media/` finds **no**
   counterpart for any of its four methods (`SearchClips`,
   `SearchClipsByKeywords`, `SearchClipsAdvanced`, `SearchStockByKeywords`), so
   it is a new-port row, not a rewire-only row. Two of its four methods
   (`SearchClipsByKeywords`, `SearchStockByKeywords`) have zero production call
   sites and are delete-then-migrate candidates; `SearchClipsAdvanced` has one
   live consumer (`youtube_adapters_store.go:57`).
### Dead-method map for the `imagesregistry` rows (verified 2026-09-20)

A method-name scan (production and test call sites, method-value references
included) separates "this file still has live consumers" from "this file is
only kept alive by its own fixture":

| File | Zero-call-site methods |
| --- | --- |
| `asset_store_batch.go` | `BatchGetByIDs`, `SetBatchCache` (both: 0 prod, 0 test) |
| `clip_list_queries.go` | `ListClips` |
| `clips_index_state.go` | `DeleteClipByDriveLink` |
| `clips_repository_queries.go` | `StreamAssetIDs` |
| `clips_statistics.go` | `CountPersistedSince` (0 prod, 9 test — kept alive only by `clips_statistics_test.go`); its sibling `CountBySource` is **live** (`artlist/diagnostics.go:230` via the `artlist/ports.go` `CountBySource` port) |
| `folder_queries.go` | `CountByFolderID`, `SearchFolders` (private helpers excluded from this scan) |
| `search_queries.go` | `SearchClipsByKeywords`, `SearchStockByKeywords` |
| `store_helpers.go` | `MarkClipsUsed` |

Two consequences. First, a row can often be split for free: delete the dead
methods, and what remains is the live surface that actually has to be moved.
Second, `clips_statistics.go` is the smallest complete unit in the slice —
delete `CountPersistedSince` (dead) + its dedicated test, move the live
`CountBySource` to a `pgmedia` implementation, rewire the artlist diagnostics,
and the file plus its inventory entry die in the same change.

2. **`imagesregistry` — sub-wave B (new ports):** `source_version.go`,
   `clips_index_state.go`, `clips_statistics.go`, `clips_queries.go`,
   `clip_list_queries.go`, `asset_store_batch.go`, `media_asset_mutations.go`,
   `repo_queries.go`, `asset_store.go`.
3. **`imagesregistry` — sub-wave C (SPLIT, media half last):**
   `clips_repository_queries.go`, `folder_queries.go`,
   `maintenance_repository.go`, `primitives.go`, then `store_helpers.go`
   (it can only be emptied once every other file stopped importing its
   projection helpers).
4. **`imagesrepo` → `deletion` → `artlist`** (`operatorread` is done; MIGRATE rows first,
   SPLIT rows after).
5. **`mediaregistry` → `metadataexport` → `controlplane`** (SPLIT-heavy; the
   non-media halves are the operational registry/health surfaces and must keep
   working on SQLite).

## Per-change checklist

Every row, in the same commit:

```
DELETE the media read (or move it to the narrow PostgreSQL port)
DELETE the sqliteMediaReaderInventoriedZoneFiles entry
DELETE the now-dead tests for the removed surface
DELETE stale archcheck allowlist / comment references
```

Then:

```bash
go test ./internal/platform/sqlite/assets/<slice>/... -count=1
go test ./internal/platform/postgres/media/... -count=1
go run ./cmd/archcheck --strict
make verify-agent
```

## Milestone acceptance

`MEDIA LEGACY READ-PLANE DEMOLITION` closes when:

```
Qdrant media inventory        9 -> 0
SQLite media inventory       32 -> 0
Admin media inventory        17 -> 0
total inventoried readers    58 -> 0
indexed writer canonical paths 2 -> 1
canonical INDEXED owner       PostgresIndexWorker only
```

with:

```bash
deadcode -test ./...
go build ./...
go vet ./...
go run ./cmd/archcheck --strict
make verify-agent
make verify-fast
```

and, only at milestone completion:

```bash
make verify-main
git push origin main
```
