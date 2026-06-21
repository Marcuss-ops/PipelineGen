# `internal/core/` — ✅ COMPLETED (PR4)

**Status**: Directory eliminated June 2026. Contracts moved to `internal/domain/asset/`, services to `internal/application/assets/`.

---

## Original plan

## Status

**pending** — deferred to Wave-13 follow-up PR.

## What exists

The `internal/core/` directory hosts the canonical contract packages in the
pre-Phase-2 model. Today it still contains:

| Sub-package | Files | Public exports | Live importers (`rg -c`, approximate) |
|---|---|---|---|
| `core/maintenance/` | service, types | `Service`, `Handler`, `EnqueueRequest` | ~6 |
| `core/processor/`   | processor, types | `Processor`, `ProcessorConfig` | ~4 |
| `core/destination/` | destination, types | `Resolver`, `Adapter` | ~8 |
| `core/embedding/`   | embedder, types | `Embedder`, `Generator` | ~3 |
| `core/assetop/`     | assetop, types | `AssetOp`, `OpResult` | ~2 |
| `core/scoring/`     | scorer, types | `Scorer` | ~1 |
| `core/audio/`       | types | `AudioFeatures`, `AudioMeta` | ~2 |
| `core/workspace/`   | types | `Workspace` | unused (audit needed) |
| `core/lifecycle/`   | service, types | `Service`, `Store` | ~5 |

Plus `analysis.go` at the root (audit in progress).

## Migration target

Per the Phase-2 rebrand plan:

| Sub-package | Target |
|---|---|
| `core/maintenance/` | `internal/application/jobs/maintenance/` |
| `core/processor/`   | `internal/domain/asset/processor/` (interface) + `internal/infrastructure/media/processor/` (impl) |
| `core/destination/` | `internal/domain/asset/destination/` (interface) + `internal/infrastructure/drive/` (impl `Resolver`) |
| `core/embedding/`   | `internal/infrastructure/embeddings/` (already exists; this is a slim rename) |
| `core/assetop/`     | `internal/domain/asset/ops/` |
| `core/scoring/`     | `internal/domain/asset/scoring/` |
| `core/audio/`       | `internal/domain/asset/audio/` |
| `core/workspace/`   | delete (audit will show no callers) |
| `core/lifecycle/`   | `internal/domain/lifecycle/` |
| root `analysis.go`  | `internal/domain/asset/analysis.go` |

## Cut-over recipe (per sub-package)

1. Create the target package skeleton with the desired public types.
2. Add a `// Deprecated:` doc comment on the legacy package symbols pointing at
   the new path. **Do not** move logic yet.
3. Update each importer to use the new paths (one domain PR per consumer,
   usually grouped by `cmd/` or by API handler).
4. Once ALL importers in the repo have been moved AND zero new importers
   appear for 30 days, delete the legacy sub-package directory and update
   `architecture/migration.yaml`.
5. CI guard (Check 13) prevents new files from being added to `internal/core/`
   during the migration window.

## Why so many sub-packages?

Each `core/<X>/` was historically a "thing that the rest of `internal/` could
implement". After Phase-2, the same exports live in their final layered home
(`domain`, `application`, `infrastructure`), and the `core/` glue disappears.

## Owner

Wave-13 follow-up PR. Estimated effort: 9 small PRs, each touching fewer than
20 importers. Not safe to bundle.

> **PR0 status footer (June 2026, fd8e3a43+/+2)** — L'intestazione
> `✅ COMPLETED` sopra è già coerente con il codice: `internal/core/` non
> esiste più, tutto assorbito da `internal/domain/asset/` e
> `internal/infrastructure/{drive,media/processor}/` (vedi Wave 4C status
> `done` nel `architecture/migration.yaml`).
