# Phase 2 Residual Importer Tracker

**Captured after Phase 2 PR-4 noise-trim (`02601f8a`) + FRAGMENTO (c) deferred
parity test (`85b7e2ec`).** This is the residual importer list for Phase 2 PR-5
plus the small multi-PR cluster that overlaps with FRAGMENTO (c).

## Summary

After Phase 2 PR-1..PR-4 (which migrated `internal/api/sources/`,
`internal/application/`, `internal/media/`, and `internal/infrastructure/` to
consume from `internal/domain/asset` in place of `internal/assets`), exactly
**27 files** remain that still import `internal/assets` directly. These are
tracked here for Phase 2 PR-5 (the next migration wave).

Phase 2's terminal goal is: **zero** `import
"github.com/Marcuss-ops/PipelineGen/internal/assets"` lines across non-
`internal/assets/*` files. PR-5 closes that gap by folder.

## Counts

| Wave | Folder | Files | Notes |
| ---: | :--- | ---: | :--- |
| 1 | `internal/app/*` (middleware / composition root) | **3** | Smallest PR-5 wave |
| 2 | generic handlers | **0** | Deferred (no files at present) |
| 3 | `cmd/*` (admin/server binaries) | **0** | Clean (zero importers) |
| 4 | `internal/sources/{artlist,youtube}/*` (source root) | **20** | Largest PR-5 wave |
| 5 | Special / multi-PR overlap with FRAGMENTO (c) | **4** | Separate path: PR-c-1..PR-c-3 |
| **Total** |   | **27** |   |

Wave 2 (generic handlers) and Wave 3 (`cmd/`) are **zero-file waves** at the
time of capture. They are kept in the wave plan so a future tornata that adds
new files in those folders has a clear migration pattern to follow.

---

## Wave 1 — middleware (3 files)

| Path | Refs | Distinct symbols |
| :--- | ---: | :--- |
| `internal/app/bootstrap.go` | 1 | `Service` |
| `internal/app/dependencies.go` | 12 | `LocationRepository`, `NewAssetStoreSQLite`, `NewService`, `ProcessingRepository`, `Repository`, `Service`, `VersionRepository` |
| `internal/app/registry.go` | 1 | `LocationRepository`, `ProcessingRepository`, `Repository`, `Service` |

### Deferred rationale

`internal/app/*` is the composition root; it constructs `assets.Service` and
wires the repositories. Per PR-2, the prototype of these repositories is
owned by `internal/assets` and the canonical import path is
`internal/domain/asset` (where the alias re-exports live). Migrating these
3 files is mechanical:

- Replace `assets.X` with `asset.X` for symbols already aliased
  (`Repository`, `Service`, `LocationRepository`, `ProcessingRepository`).
- Add YAGNI aliases for the missing one (`VersionRepository`).
- `NewService` is `*assets.Service`'s constructor — if `Service` continues
  to be the canonical façade, it stays as is.

Gate: `go vet ./internal/app/... ./internal/domain/asset/... && go test
-count=0 ./internal/app/... && go build ./internal/app/...`.

---

## Wave 2 — generic handlers

**No files at the time of capture.** If a future tornata adds generic handler
files that depend on `internal/assets`, they should migrate as part of
Wave 3 (cmd) — they share the bootstrap lifecycle — rather than being
deferred to a separate wave.

---

## Wave 3 — `cmd/*`

**No files at the time of capture.** The admin and server binaries under
`cmd/server/` and `cmd/admin/` have **zero importers** of `internal/assets`,
so Wave 3 is a no-op and remains "done" indefinitely.

---

## Wave 4 — sources root (20 files)

### `internal/sources/artlist/*` (9 files)

| Path | Refs | Distinct symbols |
| :--- | ---: | :--- |
| `internal/sources/artlist/assetrepo_integration_test.go` | 12 | `Asset, LifecycleState, MediaType, NewAssetStoreSQLite, Repository, Source, Upsert` |
| `internal/sources/artlist/convert.go` | 8 | `Asset` |
| `internal/sources/artlist/dispatch_bridge.go` | 1 | `Asset` |
| `internal/sources/artlist/dto_search.go` | 1 | `Asset` |
| `internal/sources/artlist/run_orchestrator_stages.go` | 3 | `Asset, StateReady, Version` |
| `internal/sources/artlist/search_service.go` | 9 | `Asset, MediaType, Repository, Source` |
| `internal/sources/artlist/semantic_enricher.go` | 3 | `Asset` |
| `internal/sources/artlist/service.go` | 7 | `Asset, LocationRepository, ProcessingRepository, VersionRepository` |
| `internal/sources/artlist/service_test.go` | 13 | `Asset, StateReady` |

### `internal/sources/youtube/*` (11 files)

| Path | Refs | Distinct symbols |
| :--- | ---: | :--- |
| `internal/sources/youtube/assetrepo_integration_test.go` | 10 | `Asset, Metadata, NewAssetStoreSQLite, Repository, StateReady, Upsert` |
| `internal/sources/youtube/convert.go` | 5 | `Asset` |
| `internal/sources/youtube/enrichment_fallback.go` | 1 | `Asset` |
| `internal/sources/youtube/extractor_process.go` | 1 | `Asset` |
| `internal/sources/youtube/metadata_enrich.go` | 1 | `Asset` |
| `internal/sources/youtube/metadata_persist.go` | 6 | `Asset` |
| `internal/sources/youtube/searcher_cache.go` | 3 | `Asset` |
| `internal/sources/youtube/searcher.go` | 4 | `Asset` |
| `internal/sources/youtube/search_topic.go` | 4 | `Asset` |
| `internal/sources/youtube/segment.go` | 1 | `Version` |
| `internal/sources/youtube/service.go` | 9 | `Asset, ProcessingRepository, Repository, VersionRepository` |

### Deferred rationale

`internal/sources/*` is the SOURCE-package root. These files consume
`assets.X` directly via a literal-import (e.g. `*assets.Asset` literals in
`convert.go`). Migration to the `internal/domain/asset` alias layer is
mechanical:

- Replace `assets.X` with `asset.X` for symbols already aliased (`Asset`,
  `MediaType`, `Source`, `Repository`, `Filter`, `Details`, `Service`,
  `LocationKind{,Drive,Local}`, `Location{,Repository}`,
  `ProcessingRepository`, `Stage{,Upload,Download}`,
  `Status{,Running,Completed,Failed}`, `ErrNotFound`,
  `State{,Staging,Processing,Active,Deleted,Ready,Pending}`).
- Add YAGNI aliases for the missing ones (`Version`, `VersionRepository`,
  `Locations`, `Metadata`, `Upsert`, `LifecycleState`, `SoftDelete`,
  `ProcessingStage`, `ProcessingStatus`).
  Note: `AssetStoreSQLite` and `NewAssetStoreSQLite` are already
  aliased as of PR-4 — see `internal/domain/asset/asset.go` preamble;
  do NOT re-add them.

### FRAGNAMENTO (b) cross-link

Wave 4 is structurally adjacent to FRAGNAMENTO (b) Phase 1 (copy-and-invert
`providers/{artlist,youtube}/impl/`). If FRAGNAMENTO (b) ships first, its
canonical `providers/*` package becomes the preferred home for
`SearchService` and `Service.SearchByTopicWithFilter` logic; Wave 4 can
adopt that as the canonical type for the source-side proxies (each
source-package method becomes a thin proxy to the providers impl,
preserving the public method signatures).

**Recommended order**: FRAGNAMENTO (b) Phase 1 → FRAGNAMENTO-wave-4 (sources
root) — both touch `internal/sources/{artlist,youtube}/*` and overlap on
symbol surfaces.

Gate: `go vet ./internal/sources/... ./internal/domain/asset/... && go test
-count=0 ./internal/sources/... && go build ./internal/sources/... && go test
-v -count=1 -run Test.*Match ./internal/domain/asset/...`.

---

## Wave 5 — special / multi-PR (FRAGMENTO-c overlap, 4 files)

These four files are not part of PR-5 because they overlap with the
multi-PR FRAGMENTO (c) path (artifact-status fusion) or are the alias
layer itself.

| Path | Refs | Distinct symbols (selected) |
| :--- | ---: | :--- |
| `internal/artifacts/clips_adapter.go` | 28 | `Asset, Details, ErrNotFound, Location{,KindLocal,KindDrive}, LocationRepository, MediaType, ProcessingRepository, Repository, Service, Source, Stage{,Upload,Download}, State{,Ready,Deleted}, Status{,Running,Completed,Failed}, {Upsert,SoftDelete}` |
| `internal/artifacts/converters.go` | 6 | `Asset` (struct + `Set*` methods) |
| `internal/domain/asset/asset.go` | 48 | `Asset, MediaType, Source, Repository, Filter, Details, Service, LocationKind{,Drive,Local}, Location{,Repository}, ProcessingRepository, Locations, Stage{,Upload,Download}, Status{,Running,Completed,Failed}, ErrNotFound, State{,*}, Artifact, ArtifactStoreSQLite, ClipFolder, AdvancedSearch{Request,Result}, NewAssetStoreSQLite, SegmentEmbeddingRecord, ScanCanonicalAssetRowsPublic` |
| `internal/domain/asset/asset_test.go` | 32 | alias parity tests (TestState, TestLocationKind, TestProcessing, TestFunctionRebindings, TestAssetIsHardAlias, TestStatusConstantValues) — references the canonical types via their underlying `assets.X` values |

### Deferred rationale

This cluster overlaps with **FRAGMENTO (c)** (artifact-status fusion) and
the alias-layer expansion. The two `internal/artifacts/*` files
(`clips_adapter.go`, `converters.go`) are part of FRAGMENTO-c's cycle-break
(extract to `internal/artifacts/clipsadapter/` per PR-c-1), then migrate their
20+ assets references per PR-c-2, then the canonical `internal/artifacts`
package becomes self-contained per PR-c-3 after `internal/assets/artifact.go`
is deleted.

The two `internal/domain/asset/*` files (`asset.go`, `asset_test.go`) are the
alias layer itself. They cannot be migrated further under PR-5 because
Phase 2's terminal goal is for these aliases to point at non-`internal/assets/`
types; that's PR-c-2 / PR-c-3 territory.

### Why this stays out of PR-5

* These files form a **separate architectural cluster** (canonical vs.
  legacy story, rather than the bounded-context sweep that PR-5 plans).
* FRAGMENTO (c) is a **multi-PR effort** with its own commit-message
  contract — it must rewrite `ArtifactStore` interfaces in `internal/assets`,
  break the cycle, and only then delete artifact.go.
* Co-mingling FRAGMENTO (c)'s surface with PR-5 would dilute the diff's
  narrative and risk a regression in either path.

---

## Acceptance criteria

PR-5 is **complete** when the following hold:

1. **Zero residual importers** of `internal/assets` outside the Wave-5
   cluster:
   `grep -rl 'github.com/Marcuss-ops/PipelineGen/internal/assets"'
   --include='*.go' . | grep -v '^./internal/assets/'` returns only
   the four Wave-5 files (`clips_adapter.go`, `converters.go`,
   `asset.go`, `asset_test.go`).
2. **Strict gate green on PR-5 surface**:
   `go vet ./internal/app/... ./internal/sources/... ./internal/domain/asset/...
   && go test -count=0 ./internal/app/... ./internal/sources/...
   ./internal/domain/asset/... && go build ./internal/app/...
   ./internal/sources/...`.
3. **All alias re-exports stay parity-tested**: the seven parity tests in
   `internal/domain/asset/asset_test.go` (`TestStateConstantsMatchAssets`,
   `TestLocationKindConstantsMatchAssets`, `TestLocationKindIsHardAlias`,
   `TestProcessingConstantsMatchAssets`,
   `TestFunctionRebindingsMatchAssets`, `TestAssetIsHardAlias`,
   `TestStatusConstantValues`) all pass — `go test -v -count=1
   ./internal/domain/asset/...`.
   Sub-criterion: any PR-5-added typed-const or var alias (e.g.
   `Locations`, `VersionRepository`, `ProcessingStage`) must be covered
   by a parity test extending the existing `TestXxxConstantsMatchAssets`
   pattern. No silently-aliased consts/vars without a regression
   guard.
4. **Branch off chosen base, rebased onto main** before FF-merge — main is
   several commits ahead of any older base (e.g. `ccad98ea`) because of
   PR-1..PR-4 + FRAGMENTO (c) follow-ups.
5. **PR-5 does NOT** touch the Wave-5 cluster. PR-5's diff must be
   scope-bounded to Waves 1+4 (the 23 non-multi-PR files).

---

## Cross-refs / migration scripts

* Repro the strict gate locally: `bash scripts/ci-architectural-checks.sh`
  (when adding to the gate) and the manual triple
  `go vet ./... && go test -count=0 ./... && go build ./...` survived all
  of PR-1..PR-4.
* Alias-layer expansion precedents live in
  `internal/domain/asset/asset.go` (see preamble "Migration progress"
  block); each PR added YAGNI aliases one wave at a time.
* Delegation patterns / utility selection: `AGENTS.md` §"Modular edit
  patterns" + §"Pattern 7 — Reusing existing services" — explicit the
  rules for which utility to prefer (`pkg/...`) before custom code,
  which canonicalization-test pattern to mirror, and how to gate sed
  migrations with shadow + receiver-field-overmatch scans.
* Module ownership and registry topology (which Phase-2 modules can
  swap at the `WireRegistry` level without breaking wiring): see
  `ARCHITECTURE.md` §3 (data flows) + §10 (day-1 commands, including
  the strict-gate triplet). PR-5 should consult this when deciding
  whether to migrate per-wave or per-package.
* The "branch off base → push → rebase onto main → FF-merge → push"
  workflow is validated by the FRAGMENTO (c) DEFERRED tornata; PR-5 should
  follow the same pattern.

---

## Owner-accountability hooks

Wave-by-wave recommended agent seeding (carried over from prior tornatas'
`feat/*` branch-naming convention):

| Wave | Suggested branch name | Workflow base | Rebase target |
| :--- | :--- | :--- | :--- |
| 1 | `feat/wave-12-phase-2-pr5-middleware` | main HEAD | main HEAD |
| 4 | `feat/wave-12-phase-2-pr5-sources-root` | main HEAD | main HEAD |
| 5 | _N/A — multi-PR FRAGMENTO (c) cluster_ | ccad98ea (PR-3 base) | main HEAD |

The two PR-5 waves can be merged as a single PR if they're small (total 23
files × ~10-migration per file ≈ manageable); otherwise split Wave 4 into
PR-5a (artlist, 9 files) + PR-5b (youtube, 11 files).
