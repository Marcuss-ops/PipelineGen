# `internal/application/scriptflow/` — Pending Migration

## Status

**pending** — in-progress. Target: `internal/application/scripts/{batch,curation,generate,...}/`.

## What exists

`internal/application/scriptflow/` contains 6 sub-packages:

| Sub-package | Public surface | Live importers |
|---|---|---|
| `scriptflow/batch/`     | `BatchService` (the script-generation batch coordinator) | ~6 |
| `scriptflow/curation/`  | `Curator`, `CurationService` (clip→media-curator pipeline; also `MediaCurator`) | ~3 |
| `scriptflow/generate/`  | `GenerationService` (per-clip generation job payload) | ~2 |
| `scriptflow/jobs/`      | internals — verify no public types | audit needed |
| `scriptflow/scenes/`    | `SceneBuilder`, scene-image generator | ~3 |
| `scriptflow/documents/` | Google Docs builder | ~2 |

## Migration target

| Legacy sub-package | Target |
|---|---|
| `scriptflow/batch/`     | `internal/application/scripts/batch/` |
| `scriptflow/curation/`  | `internal/application/scripts/curation/` |
| `scriptflow/generate/`  | `internal/application/scripts/generate/` |
| `scriptflow/jobs/`      | delete if empty (audit needed) |
| `scriptflow/scenes/`    | `internal/application/scripts/scenes/` |
| `scriptflow/documents/` | merge into `internal/infrastructure/google/docs/` |

## Cut-over recipe

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

## Subtlety: the target directory `internal/application/scripts/` already exists

`internal/application/scripts/` already contains `Engine`, `Repository`,
`gemmamemory/`, and the canonical `Repository` adapter. After this migration
it will also own `batch/`, `curation/`, `generate/`, `scenes/`. That's the
direction — `scripts/` becomes the single coherent home for ALL script
generation logic.

## Owner

Wave-15 follow-up. Estimated effort: 5 PRs in dependency order.
