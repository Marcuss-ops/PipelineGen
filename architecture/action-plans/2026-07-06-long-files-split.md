# Action Plan: Long Files Split — July 2026

**Created:** 2026-07-06
**Wave-tracker ID:** `LONG-FILES-SPLIT-2026-07-06`
**Status:** pending (documentation-only anchor)

## §1 — Context

On 2026-07-06, a project-wide LOC audit surfaced **239 files ≥ 300 LOC**, **62 files ≥ 500 LOC**, **12 files ≥ 700 LOC**, and **4 files ≥ 1000 LOC**. The AGENTS.md Pattern 5 cap is ≤250 LoC per file and ≤40 files per package. This action plan maps the top 20 offenders to per-file split strategies and provides clickable suggested follow-ups for each.

## §2 — Priority Bands

### Band A — P0 absolute (>700 LOC, deadline 2026-07-20)

| # | LOC | File | Split strategy |
|---|-----|------|----------------|
| A1 | 1082 | `scripts/archcheck/main.go` | Separate CLI dispatcher (main.go thin), runner, checker registry, report formatter |
| A2 | 954 | `internal/application/youtube/metadata/service.go` | Split by operation: download, metadata extraction, enrichment, quality scoring |
| A3 | 810 | `internal/application/scripts/usecase/flow_helpers.go` | Extract per-capability helpers: clip source, document builder, specscene, translation |
| A4 | 738 | `internal/infrastructure/database/sqlite/assets/images_repository.go` | Split by CRUD: insert/update, search queries, generated-images, aggregates |
| A5 | 731 | `internal/application/jobs/registry.go` | Split by job family: voiceover jobs, script jobs, extraction jobs, stock jobs, media jobs |
| A6 | 730 | `internal/infrastructure/qdrant/indexing/payload_mapper.go` | Separate per-channel mappers: text payload, visual payload, audio payload, transcript payload |
| A7 | 710 | `internal/application/images/storage_search.go` | Split storage vs search: storage_ops.go + search_queries.go |
| A8 | 707 | `internal/application/scripts/usecase/generate_one_usecase.go` | Extract per-phase helpers: plan resolution, engine invoke, postprocessing, persistence |
| A9 | 706 | `scripts/archcheck/symbol_refs.go` | Split by scan type: import scanner, type scanner, symbol reference scanner |
| A10 | 699 | `internal/application/scripts/adapters/postprocessor_registry.go` | Extract per-postprocessor: voiceover, image, document, composite |

### Band B — P1 (600-700 LOC, deadline 2026-08-03)

| # | LOC | File | Split strategy |
|---|-----|------|----------------|
| B1 | 698 | `internal/application/assets/providers/stock/stockpipeline/orchestrator.go` | Extract per-step: resolve sources, plan clips, stage sources, build manifest, validate, emit, project |
| B2 | 692 | `internal/application/jobs/completion/complete_job_service.go` | Split by phase: validation, idempotency, in-tx persistence, outbox dispatch |
| B3 | 674 | `internal/domain/finalization/types.go` | Split by concern: artifact types, publisher types, upload intents, finalizer gates |
| B4 | 668 | `internal/application/jobs/worker/runner.go` | Extract: job dispatch, progress tracking, lease management, cancellation |
| B5 | 667 | `internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go` | Split by CRUD: insert/reserve, query, marks (enqueued/rejected), watermark |
| B6 | 662 | `internal/application/qdrant/legacyaudit/legacyaudit.go` | Audit file — may NOT need splitting (report/snapshot, not service) |
| B7 | 658 | `internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go` | Split by transition: complete, fail, retry, cancel, lease, aggregate |

### Band C — P2 (500-650 LOC, deadline 2026-08-17)

| # | LOC | File | Split strategy |
|---|-----|------|----------------|
| C1 | 646 | `internal/application/assets/deletion/deletion.go` | Split by phase: request validation, drive delete, index delete, state machine |
| C2 | 644 | `internal/application/voiceover/types.go` | Domain types — harder to split. Extract: request/response DTOs, pipeline types, port types |
| C3 | 639 | `internal/application/jobs/completion/complete_with_artifacts_service.go` | Split: manifest validation, artifact upload orchestration, atomic completion |
| C4 | 631 | `internal/app/adapters_infra.go` | Split by domain: drive adapters, qdrant adapters, search adapters, embedding adapters |

## §3 — Per-file execution checklist

Each PR lands **directly on `main`** per AGENTS.md Git-Lesson-2 (NO branches, NO `--no-ff`, NO `--force`).

Per-file verification gates:
- `gofmt -l` clean on touched files
- `go vet ./<package>/...` exit 0
- `go build ./<package>/...` exit 0
- `go test -short ./<package>/...` exit 0
- Zero new symbols exported (pure mechanical split)

## §4 — Files ALREADY covered by existing waves (SKIP)

These files are intentionally EXCLUDED from this action plan because they are already tracked by prior waves:

| File | Existing wave | Status |
|------|--------------|--------|
| `internal/app/composition.go` (661 LoC) | `CODE-QUALITY-AUDIT-2026-07-05` P0-1 | 📋 In piano |
| `internal/app/build_bundles_domain.go` (682 LoC) | `CODE-QUALITY-CLEANUP-2026-07-04` | 📋 In piano |
| Voiceover files (multiple) | `VO-DECOMPOSITION-2026-07-04` | ⚙️ In corso |
| God-object files (multiple) | `GODOBJ-2026-07-03` | ⚙️ In corso |

## §5 — Forward-pointers

- `PR-LONG-FILES-HOTSPOT-CROSSREF` (deadline 2026-08-22): post-wave git-log frequency cross-validation
- `PR-LONG-FILES-WAVE-CLOSURE` (deadline 2026-08-31): flip wave status to `done` when all Band A+B+C files ≤250 LoC

## §6 — Honest scope-lock (godlike/07)

- This is a STATIC-priority plan (by LOC). The final canonical ranking should cross-validate against git-log frequency.
- Pre-existing 6-item build issue carry-forward (`PRE-EXISTING-BUILD-ISSUES-2026-07-04`) is unchanged — NOT regressions of any split PR.
- `internal/domain/finalization/types.go` (B3) and `internal/application/voiceover/types.go` (C2) are domain-type files — splitting them is harder than splitting service/repository files. If splitting proves too invasive, they may be deferred to a later wave.
- `internal/application/qdrant/legacyaudit/legacyaudit.go` (B6) may be an audit/report file rather than a service — verify before splitting; if it's a snapshot, skip.

## §7 — Cross-references

- `architecture/current.yaml#LONG-FILES-SPLIT-2026-07-06` (wave-tracker anchor)
- `CHANGELOG.md ## Unreleased → ### Documentation` (lockstep mirror)
- `AGENTS.md ## Recent cross-cutting closures` (mirror entry)
- AGENTS.md Pattern 5 (≤250 LoC per file cap)
- AGENTS.md Git-Lesson-2 (direct-to-main, no branches)

## §8 — Signature

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
