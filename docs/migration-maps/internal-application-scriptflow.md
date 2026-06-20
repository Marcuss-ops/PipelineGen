# `internal/application/scriptflow/` — Pending Migration

## Status

**done** — completed as a single flat-merge PR (commits `ce1f7189`, `43ec726d`
on origin/main). The legacy `internal/application/scriptflow/` tree was
absorbed entirely into the flat `internal/application/scripts/` package.
Sub-package layering (batch/curation/generate/scenes/documents/jobs)
was preserved only in spirit — at source level, the symbols now live
at the top level of the flat `scripts` package (e.g. `BatchService`,
`Curator`, `Engine`, `GeminiMemory`, etc.). The doc as it was originally
drafted (sub-package-preserving moves) is kept below for historical
context, but the planned cut-over recipe is **superseded** by the
flat-merge that actually shipped.

## What existed (pre-merge)

`internal/application/scriptflow/` contained 6 sub-packages:

| Sub-package | Public surface | Live importers |
|---|---|---|
| `scriptflow/batch/`     | `BatchService` (the script-generation batch coordinator) | ~6 |
| `scriptflow/curation/`  | `Curator`, `CurationService` (clip→media-curator pipeline; also `MediaCurator`) | ~3 |
| `scriptflow/generate/`  | `GenerationService` (per-clip generation job payload) | ~2 |
| `scriptflow/jobs/`      | internals — verify no public types | audit needed |
| `scriptflow/scenes/`    | `SceneBuilder`, scene-image generator | ~3 |
| `scriptflow/documents/` | Google Docs builder | ~2 |

## Original migration target (superseded)

| Legacy sub-package | Target (pre-merge plan) |
|---|---|
| `scriptflow/batch/`     | `internal/application/scripts/batch/` |
| `scriptflow/curation/`  | `internal/application/scripts/curation/` |
| `scriptflow/generate/`  | `internal/application/scripts/generate/` |
| `scriptflow/jobs/`      | delete if empty (audit needed) |
| `scriptflow/scenes/`    | `internal/application/scripts/scenes/` |
| `scriptflow/documents/` | merge into `internal/infrastructure/google/docs/` |

## Original cut-over recipe (superseded)

1. Move `scriptflow/batch/` first — it has the most importers and is the most
   cohesive. Use `git mv` (intra-non-legacy rename, so the CI guard
   doesn't fire) followed by import-path updates.
2. Move `scriptflow/curation/` — second-largest import footprint.
3. Move `scriptflow/generate/`, `scriptflow/scenes/`.
4. Move `scriptflow/documents/` last (it depends on the
   `internal/upload/drive` migration finishing — see
   [internal-upload.md](internal-upload.md)).
5. Audit `scriptflow/jobs/` — if empty, drop it; otherwise redistribute.
6. Drop `internal/application/scriptflow/`. Update
   `architecture/migration.yaml`.

## Actual outcome (June 2026)

The 6 sub-packages were flattened into the **single** `scripts/` package in
two PRs on origin/main:

1. `ce1f7189` — book/lessons consolidation into content layer.
2. `43ec726d` — flat-merge of `scriptflow/{batch,curation,generate,
   documents,jobs,scenes}/*` → `scripts/*`. After this:
   - `scripts/batch_types.go` owns `BatchTopic`, `GenerateBatchRequest`,
     `ValidateGenerateBatchRequest` (formerly in `scriptflow/batch`).
   - `scripts/batch_service.go` owns `BatchService` /
     `ExecuteBatchGeneration`.
   - `scripts/engine.go`, `scripts/source.go`, `scripts/normalize.go`,
     `scripts/clip_source_evidence.go`, `scripts/write_script.go` —
     top-level scriptflow functions now flat.
   - `scripts/curate.go`, `scripts/mediacurator.go`, `scripts/search.go`
     (formerly `scriptflow/curation/*`) live alongside `engine.go`.
   - Pre-flatten test files in the importing packages that referenced
     `scriptflow/batch.X` (e.g. `handler_batch_validation_test.go`,
     `handler_batch_test.go`) were updated to call
     `scripts.<X>` directly. The `batch` and `batchpkg` aliases were
     dropped because the flat package name is `scripts`.

## Subtlety: `internal/application/scripts/` was already a real package

`internal/application/scripts/` already contained `Engine`, `Repository`,
`gemmamemory/`, and the canonical `Repository` adapter. After the flat-merge
it also owns the scriptflow subset. `scripts/` is now the single coherent
home for **all** script generation logic, with a single `package scripts`
declaration and no nested sub-packages.

## Owner

Completed. Future cleanup, if any, lives in the burn-down backlog.
