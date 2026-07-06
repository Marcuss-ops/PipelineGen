# LONG-FILES-DECOMPOSITION-2026-07-06 — Action Plan

> **Source**: `wc -l` analysis of all Go production files on `origin/main` (2026-07-06).
> **Authoring context**: post-`GODOBJ-2026-07-03` (16 god-object files, 4 bands) +
> post-`CODE-QUALITY-AUDIT-2026-07-05` (4 P0 + 3 P1 anti-patterns).
> This plan captures the **8 longest files NOT already tracked** by any
> existing wave — the gap between the god-object waves (which prioritized
> semantic-smell + fake-availability) and the raw LOC reality on disk.

> **Rule of thumb (AGENTS.md Pattern 5)**:
> - A file over ~300-400 LoC with >2-3 distinct responsibilities should be split.
> - A file over 700 LoC is a **critical** violation.
> - Each split file owns exactly ONE capability concern (godlike/06 SSOT).

> **godlike/06 3-surface lockstep (per CANONICAL.md §1)**:
> 1. `architecture/action-plans/2026-07-06-long-files-decomposition.md` (this file — narrative)
> 2. `architecture/current.yaml#LONG-FILES-DECOMPOSITION-2026-07-06` (wave-tracker entry)
> 3. `CHANGELOG.md` `## Unreleased → ### Documentation` (closure meta-entry)
> 4. `AGENTS.md` §Recent cross-cutting closures (mirror entry)

---

## 1. Honest disclosure (godlike/07 no-fake-availability)

The 8 files below were selected by **static LOC count** — the raw `wc -l`
output on `origin/main` as of 2026-07-06. Files already covered by
existing wave-tracker entries (`GODOBJ-2026-07-03`,
`CODE-QUALITY-AUDIT-2026-07-05`, `VO-DECOMPOSITION-2026-07-04`,
`CODE-QUALITY-CLEANUP-2026-07-04`) are **EXCLUDED** from this plan
(e.g. `jobs/registry.go` 731 LoC → GODOBJ P0 band;
`app/composition.go` 661 LoC → CODE-QUALITY P0-1).

**Excluded from this plan (already tracked)**:
| File | LOC | Existing wave |
|------|-----|---------------|
| `internal/application/jobs/registry.go` | 731 | GODOBJ-2026-07-03 P0 band |
| `internal/application/assets/providers/stock/stockpipeline/orchestrator.go` | 698 | GODOBJ-2026-07-03 Mechanical band |
| `internal/application/jobs/completion/complete_job_service.go` | 692 | GODOBJ-2026-07-03 P0 band |
| `internal/app/composition.go` | 661 | CODE-QUALITY-AUDIT-2026-07-05 P0-1 |
| `internal/application/jobs/worker/runner.go` | 668 | GODOBJ-2026-07-03 P0 band |

**Carry-forward preservation**: the 6 pre-existing build issues from
`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` are NOT
regressions of any LONG-FILES-DECOMPOSITION PR.

---

## 2. The 8 target files (sorted by LOC, descending)

### 🔴 CRITICA (3 files, >800 LOC)

#### 1. `scripts/archcheck/main.go` — 1082 LOC

- **Violation**: 4.3× the Pattern 5 cap (~250 LoC).
- **Current content**: CLI dispatcher (`main()` switch), runner loop,
  report formatter, `--strict` gate logic, all in one file.
- **Split topology** (3 files):
  - `main.go` (slim dispatcher: `main()` + `switch` + flag parsing, ~80 LoC)
  - `runner.go` (check execution loop + `DefaultChecks` registry + per-check timeout, ~400 LoC)
  - `report.go` (output formatting: `--strict` exit-code logic + report table + JSON output, ~300 LoC)
  - *(remaining ~300 LoC: check-specific dispatch helpers stay in `scan/*.go` per Phase 1)*
- **godlike/06 SSOT**: each of the 3 split files owns exactly one capability concern (CLI entrypoint / execution / reporting).
- **godlike/07 minimum-blast-radius**: zero surface-contract changes; `main()` signature preserved; `go run ./cmd/archcheck` output identical.
- **Effort**: ~1000 LoC moved (pure code-motion), ~50 LoC new tests.

#### 2. `internal/application/youtube/metadata/service.go` — 954 LOC

- **Violation**: 3.8× the Pattern 5 cap.
- **Current content**: YouTube metadata service monolith — download, enrich,
  caching, retry, transcript extraction all in one file.
- **Split topology** (5 files):
  - `service.go` (slim orchestrator: `MetadataService` struct + `NewMetadataService` + public surface, ~120 LoC)
  - `service_download.go` (download + retry + yt-dlp invocation, ~200 LoC)
  - `service_enrich.go` (metadata enrichment: tags, topics, entities, ~200 LoC)
  - `service_cache.go` (cache layer: read-through + write-back + TTL, ~150 LoC)
  - `service_transcript.go` (transcript extraction: Whisper + fallback, ~200 LoC)
- **godlike/06 SSOT**: each file owns exactly one capability concern.
- **godlike/07 minimum-blast-radius**: zero surface-contract changes;
  `MetadataService` public API preserved.
- **Effort**: ~900 LoC moved (pure code-motion), ~80 LoC new tests.

#### 3. `internal/application/scripts/usecase/flow_helpers.go` — 810 LOC

- **Violation**: 3.2× the Pattern 5 cap.
- **Current content**: "Helpers" file with 800+ LOC — an anti-pattern
  signaling multiple capability concerns hiding under a generic name.
- **Split topology** (4 files):
  - `flow_helpers.go` (slim: shared utility functions used by ≥2 use cases, ~150 LoC)
  - `flow_helpers_clips.go` (clip-specific helpers: resolution, filtering, sorting, ~220 LoC)
  - `flow_helpers_script.go` (script-specific helpers: generation, postprocessing, ~220 LoC)
  - `flow_helpers_voiceover.go` (voiceover-specific helpers: TTS, Drive, ~220 LoC)
- **godlike/06 SSOT**: each helper file is co-located with its capability domain.
- **godlike/07 minimum-blast-radius**: zero new exported symbols; pure reorg.
- **Effort**: ~800 LoC moved (pure code-motion), ~40 LoC new tests.

---

### 🟠 ALTA (5 files, 700-740 LOC)

#### 4. `internal/infrastructure/database/sqlite/assets/images_repository.go` — 738 LOC

- **Violation**: 2.9× the Pattern 5 cap.
- **Current content**: SQLite repository monolith for images — CRUD, search,
  pagination, aggregation all in one file.
- **Split topology** (4 files):
  - `images_repository.go` (slim: `ImagesRepository` struct + constructor + compile-time pin, ~80 LoC)
  - `images_repository_crud.go` (Insert/Update/Get/Delete + scan helpers, ~250 LoC)
  - `images_repository_search.go` (List/Search/Filter + pagination + limit constants, ~220 LoC)
  - `images_repository_aggregate.go` (Aggregate/Count/GroupBy + territory queries, ~190 LoC)
- **godlike/06 SSOT**: each file owns exactly one operation family.
- **godlike/07 minimum-blast-radius**: zero SQL query changes; pure file split.
- **Effort**: ~730 LoC moved (pure code-motion), ~50 LoC new tests.

#### 5. `internal/infrastructure/qdrant/indexing/payload_mapper.go` — 730 LOC

- **Violation**: 2.9× the Pattern 5 cap.
- **Current content**: Qdrant payload mapping monolith — per-asset-type
  mappers, field serialization, sparse vector construction, all in one file.
- **Split topology** (4 files):
  - `payload_mapper.go` (slim: `PayloadMapper` struct + `Map` dispatcher + compile-time pin, ~100 LoC)
  - `payload_mapper_youtube.go` (YouTube clip → Qdrant payload mapping, ~220 LoC)
  - `payload_mapper_artlist.go` (Artlist asset → Qdrant payload mapping, ~200 LoC)
  - `payload_mapper_voiceover.go` (Voiceover → Qdrant payload mapping + sparse bm25, ~210 LoC)
- **godlike/06 SSOT**: each mapper file owns exactly one asset type.
- **godlike/07 minimum-blast-radius**: zero wire-format changes; Qdrant payload identical.
- **Effort**: ~720 LoC moved (pure code-motion), ~60 LoC new tests.

#### 6. `internal/application/images/storage_search.go` — 710 LOC

- **Violation**: 2.8× the Pattern 5 cap.
- **Current content**: Storage + search conflated in a single file.
  Two distinct capabilities: (a) image storage/retrieval, (b) image search.
- **Split topology** (2 files):
  - `storage.go` (image storage: upload, download, local cache, Drive sync, ~400 LoC)
  - `search.go` (image search: query construction, Qdrant search, rerank, result mapping, ~310 LoC)
- **godlike/06 SSOT**: storage and search are separate capability concerns.
- **godlike/07 minimum-blast-radius**: zero surface-contract changes.
- **Effort**: ~700 LoC moved (pure code-motion), ~30 LoC new tests.

#### 7. `internal/application/scripts/usecase/generate_one_usecase.go` — 707 LOC

- **Violation**: 2.8× the Pattern 5 cap.
- **Current content**: Single use case with 700+ LOC — likely a multi-phase
  orchestrator (plan → generate → postprocess → persist).
- **Split topology** (4 files):
  - `generate_one_usecase.go` (slim orchestrator: `GenerateOneUseCase` struct + `Execute` entry point, ~120 LoC)
  - `generate_one_plan.go` (planning phase: LLM plan generation + validation, ~200 LoC)
  - `generate_one_execute.go` (execution phase: script generation + postprocessing, ~200 LoC)
  - `generate_one_persist.go` (persistence phase: DB write + cache bump + outbox, ~190 LoC)
- **godlike/06 SSOT**: each phase file owns exactly one pipeline stage.
- **godlike/07 minimum-blast-radius**: zero behavior change; pipeline phases preserved.
- **Effort**: ~700 LoC moved (pure code-motion), ~50 LoC new tests.

#### 8. `scripts/archcheck/symbol_refs.go` — 706 LOC

- **Violation**: 2.8× the Pattern 5 cap.
- **Current content**: Symbol reference extraction — AST parsing, import
  graph building, cross-file symbol lookup. Companion to `main.go`.
- **Split topology** (3 files):
  - `symbol_refs.go` (slim: public surface + orchestrator, ~150 LoC)
  - `symbol_refs_parse.go` (AST parsing: `go/parser` + `go/ast` walk, ~300 LoC)
  - `symbol_refs_graph.go` (import graph: cross-file resolution + cycle detection, ~260 LoC)
- **godlike/06 SSOT**: each file owns exactly one concern (public API / parsing / graph).
- **godlike/07 minimum-blast-radius**: zero exported symbol changes; pure file split.
- **Effort**: ~700 LoC moved (pure code-motion), ~40 LoC new tests.

---

## 3. Priority bands (3)

| Band | Items | Deadline | Pattern |
|------|-------|----------|---------|
| **P0 CRITICA** (>800 LOC) | 3 (archcheck/main.go + youtube/metadata/service.go + scripts/usecase/flow_helpers.go) | 2026-07-15 | Mechanical split per AGENTS.md Pattern 5 |
| **P1 ALTA** (700-740 LOC) | 3 (images_repository.go + payload_mapper.go + storage_search.go) | 2026-07-25 | Mechanical split per AGENTS.md Pattern 5 |
| **P1 ALTA** (700-710 LOC) | 2 (generate_one_usecase.go + symbol_refs.go) | 2026-07-25 | Mechanical split per AGENTS.md Pattern 5 |

---

## 4. Execution order & locks

### Wave 1 — P0 CRITICA (deadline 2026-07-15, 3 SHAs)

1. **PR-ARCHCHECK-MAIN-SPLIT** — `scripts/archcheck/main.go` → 3 files
2. **PR-YOUTUBE-METADATA-SPLIT** — `internal/application/youtube/metadata/service.go` → 5 files
3. **PR-FLOW-HELPERS-SPLIT** — `internal/application/scripts/usecase/flow_helpers.go` → 4 files

Locks: all 3 are independent (different packages, different concerns).
Can land in parallel per godlike/07 EXPAND-phase discipline.

### Wave 2 — P1 ALTA (deadline 2026-07-25, 5 SHAs)

1. **PR-IMAGES-REPO-SPLIT** — `images_repository.go` → 4 files
2. **PR-PAYLOAD-MAPPER-SPLIT** — `payload_mapper.go` → 4 files
3. **PR-STORAGE-SEARCH-SPLIT** — `storage_search.go` → 2 files
4. **PR-GENERATE-ONE-SPLIT** — `generate_one_usecase.go` → 4 files
5. **PR-SYMBOL-REFS-SPLIT** — `symbol_refs.go` → 3 files

Locks: all 5 are independent. Can land in parallel.
PR-SYMBOL-REFS-SPLIT is independent of PR-ARCHCHECK-MAIN-SPLIT
(different files in the same package — they split different god objects;
the files don't import each other).

---

## 5. Per-PR verification gates

Every PR must pass on its **targeted subtree** before push:

```bash
gofmt -l <touched_files>          # must be clean
go vet ./<targeted_package>/...   # must exit 0
go build ./<targeted_package>/... # must exit 0
go test -short -count=1 ./<targeted_package>/...  # must PASS
```

Pre-existing build issues from `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`
are **out of scope** — each PR only needs to pass on its own subtree
(per the GODOBJ-2026-07-03 precedent: "each per-file PR lands in isolation
on its own subtree").

---

## 6. Migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

For these mechanical splits, the sequence is simplified:

1. **EXPAND** — create the new split files with the extracted code.
   The old file stays at full size. Both surfaces coexist.
2. **CUTOVER** — remove the extracted code from the old file, leaving
   only the slim orchestrator. Tests verify byte-equivalent behavior.
3. **CONTRACT** — (optional) deprecation record for the old surface.
   For pure code-motion splits, CONTRACT is a no-op (the old file
   still exists as the slim orchestrator).

No BACKFILL is needed because no call site changes — the new files
are in the same package, so imports don't change.

---

## 7. Honest limitations (godlike/07)

1. **Static prioritization**: this audit prioritized by raw LOC count
   only. Some files may be naturally long (e.g., a registry with 50+
   entries) and splitting them would hurt readability. The per-PR
   agent must validate that the split actually improves cohesion
   before landing.

2. **The 6 pre-existing build issues** carry forward unchanged —
   NOT regressions of any LONG-FILES-DECOMPOSITION PR.

3. **`jobs/registry.go` (731 LOC)** is EXCLUDED because it's already
   in GODOBJ-2026-07-03 P0 band. The GODOBJ tracker owns that split.

4. **No new feature work**: this wave is purely structural. New features
   are forward-pointed to their own waves.

5. **This plan does NOT cover test files** — `youtube_discoveries_test.go`
   (1554 LOC) and other large test files are separate concerns.

---

## 8. Wave-tracker entry pointer (canonical anchor)

`architecture/current.yaml#LONG-FILES-DECOMPOSITION-2026-07-06`:

- 8 net-new slim-shape `linked_issues` (1 per PR above).
- 2 per-band deadline anchors (2026-07-15 / 2026-07-25).
- 1 forward-pointer cross-ref `PR-LONG-FILES-HOTSPOT-CROSSREF` (deadline 2026-08-01).

**Status at creation**: `pending` (no PR shipped yet).

---

## 9. Cross-references

- `architecture/current.yaml#GODOBJ-2026-07-03` — god-object decomposition (16 files, 4 bands)
- `architecture/current.yaml#CODE-QUALITY-AUDIT-2026-07-05` — anti-patterns audit (4 P0 + 3 P1)
- `architecture/current.yaml#CODE-QUALITY-CLEANUP-2026-07-04` — cleanup priorities (12 dirty areas)
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — carry-forward list
- AGENTS.md Pattern 5 — god-object split discipline
- AGENTS.md Git-Lesson-2 — direct-to-main workflow
- AGENTS.md Git-Lesson-3 — Co-authored-by trailer discipline
- godlike/06 SSOT (one canonical owner per fact)
- godlike/07 no-fake-availability, typed-error contract, minimum-blast-radius

---

## 10. Lifecycle (audit trail)

- **2026-07-06**: this plan created. Status: `pending`.
- **2026-07-15**: target close for Wave 1 (3 CRITICA PRs).
- **2026-07-25**: target close for Wave 2 (5 ALTA PRs).
- **2026-08-01**: forward-pointer `PR-LONG-FILES-HOTSPOT-CROSSREF` runs.

**Co-authored-by**: PipelineGen Agent <agent@pipelinegen.local>
(per AGENTS.md Git-Lesson-3 auditability convention)
