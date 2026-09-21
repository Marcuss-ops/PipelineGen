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
| SQLite media inventory | **32 → 22.** Seven rows executed on 2026-09-20: `clips_enrich_state.go`, `dedup_queries.go`, `clips_resolution.go` and the three `assets/operatorread/` rows (deleted, replaced by `pgmedia.OperatorInventoryReader`), plus `adminmedia_source.go` (parallel wave). Three more closed on 2026-09-21: `asset_store_batch.go` (DELETE), `folder_queries.go` (SPLIT, media half gone) and `clips_statistics.go` (MIGRATE: the per-source count moved to the media SSOT). Two more closed in the 2026-09-21 sub-wave B pass: `source_version.go` (DELETE) and `clips_index_state.go` (SPLIT, media read gone). One more closed in the sub-wave B' pass: `clip_list_queries.go` (SPLIT reached its terminal state; file deleted). 19 rows remain. |
| Qdrant media inventory | 9 → 2 (`asset_store.go`, `asset_store_fetch.go`); the clipindexer half is gone. |
| Admin media inventory | 17 → 9 (staged for the `cmd/admin` wave: 7 `internal/backfill/`, 1 `internal/cleanup/`, 1 `internal/drive/`). |
| Total inventoried readers | 58 → 30. |

Remaining SQLite worklist: **19 files — `imagesregistry` 9, `imagesrepo` 4,
`mediaregistry` 3, `controlplane` 1, `deletion` 1, `metadataexport` 1.**

## 2026-09-21 pass — dead-method demolition executed, two rows closed

This pass executed the "split a row for free" step the 2026-09-20 recon named,
and the register's own pins did the verification work instead of the author.

### Rows closed (inventory entry deleted in the same change)

| Row | Verb | Evidence |
| --- | --- | --- |
| `asset_store_batch.go` | **DELETE** | `BatchGetByIDs` (the SQLite search-result hydrator plus its 60 s `batchCache`) and `SetBatchCache` had zero production and zero test call sites; the Qdrant hydration they were written for is retired; the file had no non-media half. File and entry removed; the `batchCache` field it required was removed from `asset_store.go` in the same change, which stays in the inventory because it still reads `media_assets` for Get/Save/List. |
| `folder_queries.go` | **SPLIT, terminal** | Its only `media_assets` read was `CountByFolderID` (zero call sites). With it gone nothing in the file reads `media_assets` — what remains is `clip_folders` CRUD and folders are not media rows — so the entry left the inventory while the file stayed in the tree. **Found by the gate, not by the author:** `TestSQLiteMediaReaderRegisterHasNoStaleEntries` failed on the kept entry and named the file. |
| `clips_statistics.go` | **MIGRATE TO PG** | Its last live read, `CountBySource` (the `/api/artlist/diagnostics` per-source total), now resolves on the media SSOT through the new `pgmedia.MediaStatisticsReader`. File and entry removed. See the port decision below — the count was moved OFF `artlist.AssetStore` rather than stubbed, which is what kept all 35 operational-store test fixtures compiling unchanged. |

### Dead methods deleted (rows that remain, now smaller)

A method-name scan (production and test call sites, method-value references
included) confirmed zero callers for each of these before deletion; the tests
that existed only to keep a method alive were deleted with it.

| File | Deleted |
| --- | --- |
| `clip_list_queries.go` | `ListClips` |
| `clips_index_state.go` | `DeleteClipByDriveLink` (already a fail-closed stub) |
| `clips_repository_queries.go` | `StreamAssetIDs` |
| `folder_queries.go` | `CountByFolderID`, `SearchFolders` |
| `search_queries.go` | `SearchClipsByKeywords`, `SearchStockByKeywords` |
| `clips_statistics.go` | `CountPersistedSince` (and its whole test file) — the file itself left the inventory in the same pass, see the rows above |
| `store_helpers.go` | `MarkClipsUsed`, `MarkUsed` |
| `media_asset_mutations.go` | `UpdateMediaAssetUsage` and `persistMediaAssetUsage` — the chained dead tail once `MarkUsed` lost its caller; the reuse-counter successor already exists on the SSOT (`pgmedia/mutations.go`) |

Chained liveness was followed deliberately: deleting the documented dead method
(`MarkClipsUsed`) exposed two more zero-caller links (`MarkUsed`,
`UpdateMediaAssetUsage`), which were removed in the same pass rather than left
as a second dead generation.

### The `CountBySource` decision — the count moved OFF the asset-store port

The first pass recorded this row as blocked on a maintainer contract decision:
`CountBySource` was the file's last live read, it was declared by
`artlist.AssetStore` (`internal/capabilities/assets/providers/artlist/ports.go`),
and the composition root satisfied that port by converting the operational
repository (`assetStore := artlist.AssetStore(bundle.ClipsRepo)`), so deleting
the SQLite method would have broken the `bundle.MediaDB == nil` path.

That framing was right about the constraint and wrong about the fix. The first
implementation kept the port and added a composition-root adapter that fails the
count closed — which broke **35 test fixtures across 13 files** that pass a
`*imagesregistry.ClipsRepository` as the port, because the port demanded a
media-SSOT method from an operational-store type. The distribution of those
fixtures is the evidence that the method never belonged on that interface.

**The decision applied: the count is its own port.**

| Change | Where |
| --- | --- |
| `CountBySource` removed from `AssetStore` | `artlist/ports.go` |
| `SourceCounter` port added (`CountBySource(ctx, source) (int, error)`), documented OPTIONAL | `artlist/ports.go` |
| `ServicePorts.SourceCounter` field + `Service.sourceCounter` | `artlist/service_deps.go`, `artlist/service.go` |
| Diagnostics reads `d.svc.sourceCounter` behind a nil guard | `artlist/diagnostics.go` |
| `pgmedia.MediaStatisticsReader` implements it; pinned by `_ artlist.SourceCounter` | `internal/platform/postgres/media/media_statistics.go`, `internal/app/wiring/artlist_runs_adapter.go` |
| Wired only on the PG branch; left nil otherwise | `internal/app/wiring/build_bundles_artlist_artlist.go` |

Why this is the cleaner answer, not a workaround:

- **The fact is a media-SSOT fact.** The count was bolted onto `AssetStore` in
  July 2026 (`PR-P2-DIAGNOSTICS-REALE`) purely to hang it off an existing port;
  a separate port stops forcing an operational-store type to implement a
  `media_assets` read.
- **`godlike/07` fail-closed is preserved and observable.** With no media handle
  the port is nil, the field is unavailable, and `/api/artlist/diagnostics`
  reports it as such — never fabricated, never read off the mirror. This is the
  same shape the same bundle already uses for `LocalSearcher` and
  `ClipResolver` in that mode, so no new deployment mode appears.
- **Zero fixture churn.** `AssetStore` keeps its seven operational methods, so
  `_ artlist.AssetStore = (*assets.ClipsRepository)(nil)` still holds and every
  existing test compiles unchanged.
- **The retired semantics are pinned on the new engine.**
  `pgmedia/media_statistics_test.go` certifies exact source match, cross-source
  exclusion, the deliberate *absence* of a soft-delete discount, and the
  empty-source sentinel — the last four run live against PostgreSQL
  (`TEST_POSTGRES_DSN`), not against a mock.

The alternative — making PostgreSQL mandatory for the whole Artlist asset store
— was rejected here because it changes a deployment contract (the bundle
currently supports an operational-store-only mode) rather than a read path. If
maintainers want that instead, this row is still the right place to start.

### Verification run in this pass

```text
# 2026-09-21 first pass (dead-method demolition)
go build ./...                                              OK
go test ./internal/platform/sqlite/...                      ok
go test ./cmd/archcheck/...                                 ok
go test ./internal/capabilities/assets/providers/artlist/... ok
go test ./internal/app/wiring/...                           ok
gofmt -l .                                                  CLEAN (repo-wide)
go vet ./...                                                PASS (repo-wide)
go run ./cmd/archcheck --strict                             PASS (has_hard_gate_hits: false)

# 2026-09-21 second pass (clips_statistics.go row closed)
go build ./...                                              OK
go vet ./...                                                PASS (repo-wide)
go test ./internal/platform/postgres/media/ -run TestMediaStatisticsReader  PASS
    # and the same live pin against the real engine:
    #   TEST_POSTGRES_DSN=postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media_test?sslmode=disable
    #   → 6/6 PASS, including "soft-deleted rows are still counted"
go test ./internal/capabilities/assets/providers/artlist/...  ok (incl. the two new SourceCounter pins)
go test ./internal/app/wiring/... ./internal/platform/sqlite/... ok
go run ./cmd/archcheck --strict                             PASS
```

`make verify-fast` was **not** run and is not claimed: its `tidy-check` runs
`go mod tidy` followed by `git diff`, and this checkout's `.git` is an empty
directory, so it would have mutated `go.mod`/`go.sum` with no way to revert. Its
read-only equivalents (`gofmt -l .`, `go vet ./...`, `go build ./...`) were run
directly and are recorded above.

## 2026-09-21 pass - `imagesregistry` sub-wave B: two rows closed

The two rows this pass was scoped to did **not** both want the verb the first
recon gave them. One was a DELETE hiding behind a MIGRATE label; the other was a
SPLIT whose read half moved off the mirror entirely. Both left the inventory in
the same change as the read they carried.

### Row 1 - `source_version.go`: DELETE (the recon's MIGRATE was wrong)

The row note said "MIGRATE TO PG: consumed through `detail.SourceVersionQuerier`".
Every link of that chain was already severed, so the honest verb is DELETE:

| Claim checked | Evidence |
| --- | --- |
| The supersede gate that consumed the port is gone | `jobs/outbox_registry.go` ("the SourceVersionQuerier field is REMOVED with the retired IndexingHandler family") and `app/wiring/build_outbox_handlers.go` ("the SourceVersionQuerier wiring is REMOVED"), both 2026-09-12. |
| The port has no consumer left | Repo-wide, `detail.SourceVersionQuerier` appeared in exactly one non-comment reference: the compile-time pin `var _ detail.SourceVersionQuerier = (*ClipsRepository)(nil)` - a port pinning itself. |
| The free function has no production caller | No call site outside the wrapper method; the cmd/admin producer computes the same projection inline (`backfill_missing.go:67`, its own COALESCE over `metadata_json.content_hash` -> `.file_hash` -> `legacy_file_md5`). |
| The media-SSOT counterpart exists | `pgmedia/index_event.go` resolves the fingerprint with `COALESCE(NULLIF(source_version,''), NULLIF(legacy_file_md5,''))` on the media SSOT. Nothing had to be written for this row. |
| What kept it alive | `source_version_test.go` (the 4-tier priority-chain pin) and two `assets.SourceVersionFor` oracle probes in `capabilities/assets/finalizer/asset_finalizer_tx_test.go`. |

Deleted in one change: the file, its test, `*ClipsRepository.SourceVersionFor`,
the `detail.SourceVersionQuerier` interface, the pin, the inventory entry. The
oracle probes were replaced by the assertions they duplicated - the test still
pins that `metadata_json.$.content_hash` is stamped by the same write boundary as
the outbox event (its final cross-check now compares the outbox payload against
that slot instead of against the deleted helper). Five prose references that
described the helper as live were corrected (`clips_crud.go`, `clips_core.go`,
`artifacts/finalizer.go`, `artifacts/finalizer_test.go`,
`cmd/admin/internal/outbox/adapter.go`, plus the `index_document.go` field note).

### Row 2 - `clips_index_state.go`: the read half migrated, the file stays

`DeleteClipByDriveLink` had already gone in the first pass; what remained was
`SoftDelete` + `SetIndexState` (writes) and `GetIndexState` (the file's only
media read).

| Question | Answer found |
| --- | --- |
| Does `GetIndexState` have a production call site? | **No.** Repo-wide the method appears only in its own definition, the port declaration and test fixtures: zero callers. |
| What does the production read path actually use? | `postgresCatalogRepository.GetIndexState` -> `pgmedia.MediaSearcher.GetAsset` on the media SSOT (`app/wiring/registry_helpers.go`). That path is already narrow and already PostgreSQL. |
| So what kept the SQLite implementation alive? | Two wiring sites that hand catalogsync a non-nil `AssetIndexer` for its boot validation (`newCatalogSyncRepository`'s no-media-handle branch, and the admin `sync-drive-folder` command), plus the pins naming `*assets.ClipsRepository` as `catalogsync.AssetIndexer`. |
| Is the degraded read honest? | **No, and that is the finding.** `mediasub.RequireMediaPostgres` documents that there is no SQLite fallback and that "the operational mirror holds no committed media rows", and `cmd/admin/internal/cli/media_database.go` repeats it ("callers MUST fail closed rather than degrading onto the operational SQLite store"). A read that can only ever return the migration DEFAULT sentinel for a row PostgreSQL owns is exactly the fail-open shape godlike/07 forbids. |

**The change applied:** the mirror read is deleted and the slot is answered by
an explicit fail-closed adapter, `noMediaPlaneIndexState`, which reports the
index state as unknown and names the missing plane. It cannot simply be nil -
`catalogsync.NewService` rejects a target whose `Indexer` is nil, and that
validation is deliberately fail-closed - so an explicit adapter is the honest
shape, not a fabricated availability. `asset.StateDiscovered` is still returned
alongside the error because the port's contract says a diagnostic index-state
read must never suppress a new index intent.

The admin command was migrated one step further than the minimum: it now takes
its repository + indexer pair from the *same* engine decision function as boot
(`wiring.CatalogSyncPorts`, a thin export of `newCatalogSyncRepository`) instead
of handing the mirror in for both slots. One decision function, so the two call
sites cannot drift. The admin command keeps its own SQLite outbox plane; only the
media reads follow the media engine.

`SoftDelete` and `SetIndexState` stay: both are the operational half of the
`jobs.AssetDeleter` port (`outboxDeps.Jobs.AssetDeleter = repos.ClipsRepo`), they
are WRITES, and the register this file left governs media reads. With
`GetIndexState` gone the file contains no `media_assets` read at all, so
`TestSQLiteMediaReaderRegisterHasNoStaleEntries` would have rejected a kept
entry - the same terminal-SPLIT shape as `folder_queries.go`.

**One observation recorded, not fixed (writer-plane scope):** the row note says
"`SetIndexState` must keep refusing `INDEXED` (owned by `PostgresIndexWorker`)",
but neither the SQLite method nor its helper has ever enforced that. On the SQLite
plane the only production caller is `IndexDeleteHandler`, which passes
`DELETE_PENDING` / `DELETED`. The guard belongs to the writer wave (the INDEXED
sole-owner gate), not to this read-plane pass; flagged here so it is not lost.

### Residual contract question for maintainers

`catalogsync.AssetIndexer` now has **no production caller of its single method**
anywhere in the tree - it survives as a non-nil boot marker, and the only
implementations are the PG router (wired, correct, unused) and the fail-closed
adapter. Two options, both outside a read-plane demolition:

1. keep it as a marker and treat `noMediaPlaneIndexState` as its declared
deployment contract (what this pass does), or
2. reduce the port to the marker it functionally is and delete the method from
`CatalogRepository`/`Target` plumbing in one app-layer change.

### Verification run in this pass

```text
go build ./...                                              OK
go vet ./...                                                PASS (repo-wide)
gofmt -l .                                                  CLEAN (repo-wide)
go test ./internal/platform/sqlite/...                      ok
go test ./internal/app/wiring/...                           ok
go test ./internal/capabilities/assets/catalogsync/...      ok
go test ./internal/capabilities/assets/finalizer/...        ok
go test ./internal/capabilities/assets/artifacts/...        ok
go test ./internal/kernel/asset/...                         ok
go test ./cmd/archcheck/...                                 ok (register no-ghosts + no-staleness)
go run ./cmd/archcheck --strict                             PASS (has_hard_gate_hits: false)
```

## 2026-09-21 pass - `imagesregistry` sub-wave B': `clip_list_queries.go` closed

This row was the media half of a SPLIT (`ListClips` had already died in the first
pass as a zero-call-site method). It leaves the inventory AND the tree in one
change because both remaining reads moved to the engine that owns the rows.

### What was live, and where it actually read

| Symbol | Live consumer | Plane it read before | Plane it reads now |
| --- | --- | --- | --- |
| `CountClips` | `artlist/diagnostics.go::GetStats` (`/api/artlist/stats`, 4 operator scripts) | operational SQLite mirror, through `artlist.AssetStore` | `pgmedia.MediaStatisticsReader.CountClips` via `artlist.MediaStats` |
| `LastUpdatedAtForTerm` | `artlist/diagnostics.go::Diagnostics` (`LastProcessedAt`) | operational SQLite mirror, through `artlist.AssetStore` | `pgmedia.MediaStatisticsReader.LastUpdatedAtForTerm` via `artlist.MediaStats` |
| `youtube/ports.ClipStorePort.CountClips` | **none** - zero call sites in the whole tree | - | deleted with the port method (chained dead liveness, like `MarkUsed` in the first pass) |

The finding that made this a real fix rather than a port swap: **in PostgreSQL
mode the two Artlist numbers were still read off the mirror.**
`artlistMediaSSOTAssetStore` took over only the search surface, so `CountClips`
and `LastUpdatedAtForTerm` delegated straight to the operational store - i.e.
`/api/artlist/stats` reported a catalogue total from a table the canonical
PostgreSQL committer does not write to, on a surface whose entire purpose is to
be believable.

### The port decision: the aggregates moved OFF `AssetStore`

The counts were on the wrong port, twice. The previous pass moved
`CountBySource` onto a dedicated optional port (`artlist.SourceCounter`) for
exactly this reason; this pass finished the job: `CountClips` and
`LastUpdatedAtForTerm` left `artlist.AssetStore`, and that port was
**widened and renamed to `artlist.MediaStats`** (one port, three media facts, one
concrete owner on the media SSOT). `AssetStore` is back to five purely
operational methods.

Why the rename rather than a second parallel port: three ports with one method
each would be three names for one question ("what does media_assets say?"), and
`SourceCounter` became a lie the moment it also answered a catalogue total and a
timestamp.

Two consequences worth recording:

- **Zero fixture churn.** Removing methods from an interface does not break its
  implementers, so the 35 Artlist fixtures that pass `*imagesregistry.ClipsRepository`
  as `AssetStore` compiled unchanged - the same property that made the
  `CountBySource` move free. The reflection pin
  `TestArtlistAssetStore_HasNoMediaAggregateSurface` now makes re-widening the
  operational port a test failure instead of a silent drift.
- **Fail-closed on the aggregate surface.** `/api/artlist/stats` has no per-field
  way to carry "unknown" (three int/bool fields), so with no media handle it now
  returns the typed `artlist.ErrMediaStatsUnavailable` instead of a fabricated 0.
  The *diagnostics* surface keeps its per-field behaviour (unavailable, never
  fabricated), because there an absent number is representable.

### Semantics preserved, and the one divergence forced by the engine

| Property | Decision | Pinned by |
| --- | --- | --- |
| `CountClips` soft-delete discount | **KEPT** (`lifecycle_state != 'DELETED'`). Deliberately the opposite of `CountBySource`, which counts indexed rows including deleted ones - two different questions, both documented. | live-PG subtest |
| `LastUpdatedAtForTerm` has NO lifecycle filter | **kept** (a soft-deleted artlist row still participates in the `MAX(created_at)`), recorded as a preserved quirk so a future change is deliberate | live-PG subtest |
| `created_at` is TEXT on both engines | kept: the PostgreSQL DDL mirrors SQLite (`created_at_ts` is the typed twin), so `MAX()` over RFC3339 text is the same lexicographic maximum and the value is returned verbatim | shared fixture |
| source pinned to `'artlist'` | kept | live-PG subtest (the newest row overall is a `stock` row with the same tag) |
| **`LIKE` -> `ILIKE`** | **FORCED DIVERGENCE.** SQLite's `LIKE` is case-insensitive for ASCII, PostgreSQL's is case-sensitive, so a verbatim port would have silently stopped matching differently-cased tags. Same divergence, same reasoning as the `operatorread` migration of 2026-09-20. | live-PG subtest: the term `beluga` matches tags stored as `Beluga`, which a case-sensitive `LIKE` cannot do |
| empty term | **HARDENED, not preserved**: the retired statement had no sentinel, so a missing term became `LIKE '%%'` and answered "the newest artlist row". Now `media.ErrEmptyTerm`. The only production caller already guarded the case, so no call in the tree changes behaviour. | DSN-free guard subtest |

### Files touched

Deleted: `imagesregistry/clip_list_queries.go`, its inventory entry, the
chained-dead `youtube/ports.ClipStorePort.CountClips` + `ClipStoreAdapter.CountClips`,
the superseded diagnostics pin file (replaced by `diagnostics_media_stats_test.go`).
Added to `pgmedia.MediaStatisticsReader`: `CountClips`, `LastUpdatedAtForTerm`,
`ErrEmptyTerm`. Widened: `artlist.MediaStats` (formerly `SourceCounter`), its
`ServicePorts`/`Service` field and the composition-root wiring. Corrected prose
in the register zone comment, `artlist_runs_adapter.go`, `artlist/diagnostics.go`,
`build_bundles_artlist_types.go` and `search_queries.go` (which still listed the
deleted file as a package sibling).

### Verification run in this pass

```text
go build ./...                                  OK
go vet ./...                                    PASS (repo-wide)
gofmt -l .                                      CLEAN (repo-wide)
go test ./internal/platform/sqlite/...          ok
go test ./internal/app/wiring/...               ok
go test ./internal/capabilities/youtube/...     ok
go test ./internal/capabilities/assets/providers/artlist/...  ok (incl. the new MediaStats pins + the port ratchet)
go test ./internal/platform/postgres/media/...  ok (hermetic guards)
go test ./cmd/archcheck/...                     ok (register no-ghosts + no-staleness)
go run ./cmd/archcheck --strict                 PASS (has_hard_gate_hits: false)

# live engine pin (the semantics above are the ENGINE's, so they are proven there):
TEST_POSTGRES_DSN=postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media_test?sslmode=disable \
  go test ./internal/platform/postgres/media/ -run TestMediaStatisticsReader -count=1 -v
  -> PASS: CountClips discount / ILIKE case-insensitivity / source filter / unmatched term is (nil, nil) / empty table = 0 and nil
```

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
| `clip_list_queries.go` | ~~MIGRATE TO PG~~ **DELETED (2026-09-21, sub-wave B')** | `ListClips`, `CountClips`, `LastUpdatedAtForTerm` | Executed. `ListClips` died in the first pass (zero call sites); the two survivors were the media half of the SPLIT and moved to `pgmedia.MediaStatisticsReader` behind the widened optional `artlist.MediaStats` port, so the file left the inventory and the tree. The chained-dead `youtube/ports.ClipStorePort.CountClips` went with them. One forced divergence (LIKE→ILIKE) and two preserved semantics are recorded in the sub-wave B' section. |
| `clips_enrich_state.go` | ~~MIGRATE TO PG~~ **DELETED (2026-09-20)** | `SetEnrichState`, `SetEnrichStateIfCurrent`, `GetEnrichState` | Executed. `pgmedia.MediaEnrichStateStore` already implemented the same three methods and is the only implementation wired (`build_bundles_domain_assets.go` resolves `EnrichRepositoryPort` from `enrichStateStoreFromCommitter`), so the SQLite methods had zero production and zero test callers. File removed, the two now-dead `UpdateMediaAssetEnrichState*` helpers removed with it, inventory entry removed. |
| `clips_index_state.go` | ~~MIGRATE TO PG~~ **SPLIT, read half closed (2026-09-21, sub-wave B)** | `SoftDelete`, `SetIndexState`, `GetIndexState`, `DeleteClipByDriveLink` | Executed. `DeleteClipByDriveLink` (dead stub) and then `GetIndexState` (zero production call sites) are gone; the index-state slot of the no-media-plane branch now fails closed (`noMediaPlaneIndexState`) instead of reading the mirror, so the file has no `media_assets` read left and left the inventory. **Survives:** `SoftDelete` + `SetIndexState`, the WRITE half of the `jobs.AssetDeleter` operational plane (the register governs reads). The row's "must keep refusing `INDEXED`" note is still unenforced on this plane - recorded in the sub-wave B section as writer-wave scope. |
| `clips_queries.go` | MIGRATE TO PG | `Count(filter)` | Single media aggregate. |
| `clips_repository_queries.go` | SPLIT | `CountAll`, `CountIndexed`, `CountIndexable`, `List`, `StreamAssetIDs` | **Survives:** `CountPendingOutbox`, `CountDeadLetter` (`outbox_events` is operational, not media). |
| `clips_resolution.go` | ~~MIGRATE TO PG~~ **DELETED (2026-09-20)** | `ResolveByMediaAssetID`, `ResolveByYouTubeVideoID`, `ResolveByDriveFileID`, `ResolveByExternalProviderID`, `GetClipFolderByVideoID`, `GetByDriveFileID` | Executed. `pgmedia.MediaSearcher` held two of the four resolver methods already (`media_clip_resolver.go`); `ResolveByYouTubeVideoID` + `ResolveByExternalProviderID` were added there with the retired SQLite semantics (`yt_<videoID>_%` fan-out; `google_drive` → `drive_file_id`, otherwise `source` + the metadata `external_id` projection). The three consumers now read PostgreSQL: the artlist `processor.NewClipResolver` and the SceneTextGenerator `ClipAssetResolver` take `pgmedia.NewMediaSearcher(mediaDB)`, and `ClipSourceBuilder` already preferred it (its SQLite fallback was removed). The two non-resolver wrappers went with it: `GetByDriveFileID` was inlined at its single caller (`artifacts/source_catalog.go` now calls the canonical `GetClipByDriveFileID`) and `GetClipFolderByVideoID` had no callers. |
| `clips_statistics.go` | ~~MIGRATE TO PG~~ **CLOSED (2026-09-21)** | `CountBySource`, `CountPersistedSince` | Executed: `CountPersistedSince` deleted (dead + its test file), `CountBySource` moved to `pgmedia.MediaStatisticsReader` behind the new `artlist.SourceCounter` port. File and inventory entry removed. |
| `dedup_queries.go` | ~~MIGRATE TO PG~~ **DELETED (2026-09-20)** | `FindByYouTubeVideoID`, `FindByLegacyFileMD5`, `FindBySourceURL`, `FindByName`, `FindDuplicatesByYouTubeVideoID` | Executed. The three live methods were reached only through `SourcingClipStoreAdapter`; the PostgreSQL equivalents already existed (`pgmedia.MediaSearcher.FindClipID{ByName,ByYouTubeVideoID,BySourceURL}`, wired before this pass as `SourcingClipStorePGAdapter`). `newSourcingClipStore` now returns nil — never the mirror — when the media handle is absent, `sourcing.youtube.Service.dedupCheck` fails closed on a nil port, and the SQLite adapter (`youtube_adapters_store.go`) + its constructor were deleted. `FindByLegacyFileMD5` and `FindDuplicatesByYouTubeID` already had zero callers (`DuplicateAssetIDsByYouTubeID` on `pgmedia.MediaDuplicateGroupReader` supersedes the latter). |
| `folder_queries.go` | SPLIT | `CountByFolderID` | **Survives:** `clip_folders` CRUD (`UpsertFolder`, `DeleteFolder`, `GetFolder`, `GetFolderByVideoID`, `ListByFolderID`, `ListByFolderPath`, `ListFolders`) — folders are not media rows. |
| `maintenance_repository.go` | SPLIT | `ScanLocalOrphans`, `ScanDriveOrphans` | **Survives:** `DeleteOldAPIRequests` (`api_requests`), `WALCheckpoint`, `IncrementalVacuum`, `FullVacuum` — SQLite operational maintenance. |
| `media_asset_mutations.go` | MIGRATE TO PG | `CheckAndIncrementMediaAssetVersion` read + `UPDATE media_assets` primitives | Media mutation primitives; the catalog records that only non-media mutation primitives were meant to survive, so this file must lose the media half. |
| `primitives.go` | SPLIT | `HardDeleteTx` parent read `SELECT 1 FROM media_assets` | **Survives:** the non-media child deletes and their `init()` allowlist (`asset_locations`, `asset_processing`, `asset_versions`). |
| `repo_queries.go` | MIGRATE TO PG | `assetRepositoryAdapter` (`detail.Repository`) + `listAssetsByFilter` | Media repository adapter. |
| `search_queries.go` | MIGRATE TO PG | `SearchClips`, `SearchClipsByKeywords`, `SearchClipsAdvanced`, `SearchStockByKeywords` | Media search. Note: `ScanClipsByKeywords` has **zero** production references — drop it in the same change. |
| `source_version.go` | ~~MIGRATE TO PG~~ **DELETED (2026-09-21, sub-wave B)** | `SourceVersionFor` (`media_assets.file_hash` / `source_version`) | Executed as a DELETE, not a migration: the `detail.SourceVersionQuerier` consumer (the IndexingHandler supersede gate) was retired on 2026-09-12, the port had no reference besides its own pin, no production path called the helper, and the media-SSOT counterpart already existed (`pgmedia/index_event.go`). File, test, method, port, pin and inventory entry removed together. |
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
| DELETED 2026-09-20 | 7 (`clips_enrich_state.go`, `dedup_queries.go`, `clips_resolution.go`, the three `operatorread/` rows, `adminmedia_source.go`) |
| DELETED 2026-09-21 | 6 (`asset_store_batch.go`; `folder_queries.go`, whose SPLIT reached its terminal state; `clips_statistics.go`; `source_version.go`, whose MIGRATE label turned out to be a DELETE; `clips_index_state.go`, whose SPLIT reached its terminal state for the read half; `clip_list_queries.go`, whose SPLIT reached its terminal state in sub-wave B') |
| MIGRATE TO PG remaining | 11 |
| SPLIT remaining | 9 − 1 (`clip_list_queries.go`) = 8 |
| **Remaining** | **19** |

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

**Status 2026-09-21 (after sub-wave B):** every row of this map was executed (see
the pass sections above). `asset_store_batch.go` and `source_version.go` left the
inventory AND the tree; `folder_queries.go` and `clips_index_state.go` left the
inventory while staying in the tree (their SPLIT reached its terminal state: no
`media_assets` read left in either file). The other files still carry their live
surface and are the remaining sub-wave B/C worklist.

| File | Zero-call-site methods |
| --- | --- |
| `asset_store_batch.go` | `BatchGetByIDs`, `SetBatchCache` (both: 0 prod, 0 test) |
| `clip_list_queries.go` | `ListClips` — job done: the row is fully closed, because `CountClips` + `LastUpdatedAtForTerm` followed it in sub-wave B' (see below) |
| `clips_index_state.go` | `DeleteClipByDriveLink`, `GetIndexState` (0 prod, 0 test call sites - the fixtures only satisfied the port) |
| `clips_repository_queries.go` | `StreamAssetIDs` |
| `clips_statistics.go` | `CountPersistedSince` (0 prod, 9 test — kept alive only by `clips_statistics_test.go`); its sibling `CountBySource` is **live** (`artlist/diagnostics.go` via the `artlist/ports.go` `CountBySource` port). **Both resolved 2026-09-21: the row is closed** — see the pass section. |
| `folder_queries.go` | `CountByFolderID`, `SearchFolders` (private helpers excluded from this scan) |
| `search_queries.go` | `SearchClipsByKeywords`, `SearchStockByKeywords` |
| `store_helpers.go` | `MarkClipsUsed` |

Two consequences. First, a row can often be split for free: delete the dead
methods, and what remains is the live surface that actually has to be moved.
Second, `clips_statistics.go` is the smallest complete unit in the slice —
delete `CountPersistedSince` (dead) + its dedicated test, move the live
`CountBySource` to a `pgmedia` implementation, rewire the artlist diagnostics,
and the file plus its inventory entry die in the same change.

**EXECUTED 2026-09-21**, with one amendment to the plan above: the count did not
move onto `artlist.AssetStore` but onto a new dedicated `artlist.SourceCounter`
port, because keeping it on `AssetStore` forced the operational store to
implement a media-SSOT method (35 test fixtures proved the mismatch by failing
to compile). See “The `CountBySource` decision” above.

2. **`imagesregistry` — sub-wave B (new ports):** ~~`source_version.go`~~ —
   CLOSED 2026-09-21 (DELETE: the consumer chain was already severed, so no port
   had to be written). ~~`clips_index_state.go`~~ — CLOSED 2026-09-21 (the media
   READ `GetIndexState` is deleted and the degraded slot fails closed; only the
   write half survives in the file). ~~`clip_list_queries.go`~~ — CLOSED
   2026-09-21 sub-wave B' (its two media aggregates moved to the media SSOT
   behind the widened `artlist.MediaStats` port; file deleted). Remaining in this
   wave: `clips_queries.go`, `media_asset_mutations.go`, `repo_queries.go`,
   `asset_store.go`. ~~`asset_store_batch.go`~~ — CLOSED
   2026-09-21 (the row was fully dead, so it left the inventory).
   ~~`clips_statistics.go`~~ — CLOSED 2026-09-21 (count moved to the media SSOT
   behind the new `artlist.SourceCounter` port).
3. **`imagesregistry` — sub-wave C (SPLIT, media half last):**
   `clips_repository_queries.go`, `maintenance_repository.go`,
   `primitives.go`, then `store_helpers.go`
   (it can only be emptied once every other file stopped importing its
   projection helpers). ~~`folder_queries.go`~~ — CLOSED 2026-09-21: its media
   half (`CountByFolderID`) was deleted, nothing in the file reads
   `media_assets` any more, and it left the inventory while staying in the
   tree.
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
