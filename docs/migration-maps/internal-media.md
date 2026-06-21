# `internal/media/` — ✅ COMPLETED (Waves 10-12)

**Status**: Legacy subdirectories migrated:
- `monitor/` → `internal/application/monitor/` (Wave 10)
- `ingest/` → `internal/application/ingest/` (Wave 11)
- `mediaasset/` → `internal/infrastructure/media/processor/` (Wave 12)

Non-legacy subdirectories (`stockpipeline/`, `vectorstore/`, etc.) remain active.

---

## Original plan

## Status

**pending** — largest migration by file count; deferred to Wave-14.
The legacy-directory CI guard (Check 13) prevents new files from being
added while the migration is in progress.

## Sub-package inventory

`internal/media/<X>/` maps to roughly the layered split below. The exact
mapping is decided per sub-package in a follow-up PR — this map is just the
inventory + the canonical decision rule.

| Legacy sub-package | Target layer |
|---|---|
| `assetindex/`      | `domain/asset/index/` (data) + `application/asset/` (operations) |
| `assettree/`       | `domain/asset/tree/` (data) + `application/asset/tree/` (operations) |
| `autotag/`         | `application/asset/autotag/` |
| `books/`           | `application/books/` |
| `catalogsync/`     | `application/asset/catalogsync/` |
| `classifier/`      | `application/asset/classifier/` |
| `clipindexer/`     | `application/asset/clipindexer/` |
| `clipresolver/`    | `application/asset/clipresolver/` |
| `clipcatalog/`     | `domain/asset/clipcatalog/` |
| `fullimages/`      | `application/images/fullimages/` |
| `generation/`      | `application/images/generation/` (style registry lives here) |
| `ingest/`          | `application/asset/ingest/` |
| `lessons/`         | `application/lessons/` |
| `mediaasset/`      | `domain/asset/mediaasset/` (the processor lives in `core/processor/`, see [internal-core.md](internal-core.md)) |
| `monitor/`         | `application/asset/monitor/` |
| `ontology/`        | `application/asset/ontology/` |
| `scoring/`         | `domain/asset/scoring/` |
| `semantic/`        | `application/asset/semantic/` |
| `stockpipeline/`   | `application/asset/stock/` |
| `vectorstore/`     | `infrastructure/vectorstore/` + `domain/asset/vectors/` |
| `videomuscles/`    | `application/media/videomuscles/` |
| `voiceoversync/`   | `application/voiceover/sync/` |
| `foldermemory/`    | `domain/asset/foldermemory/` |
| (root `deletion.go`) | `application/asset/deletion/` |

## Cut-over recipe (per sub-package)

1. Create the target package skeleton.
2. Migrate consumers one capability at a time (usually 1 API handler at a
   time — see [Pattern 1 in AGENTS.md](../../AGENTS.md)).
3. Add `// Deprecated:` doc comments on the legacy symbols during the
   transition; once zero importers remain for 30 days, delete the legacy
   sub-package and update `architecture/migration.yaml`.
4. Each sub-package has an estimated 5–20 importers. Run one small PR per
   sub-package for safety.

## Estimated effort

- 23 sub-packages × ~1 PR per import front + 1 deletion PR each ≈ 25–40 PRs
  total over multiple waves. The CI guard keeps new code out of `internal/media/`
  during this period so the count cannot silently grow.
- Use the `migration.yaml` file at `architecture/migration.yaml` to track
  per-sub-package status.

## Owner

Wave-14+ (multi-quarter). Not blocking for Phase A or Phase-B work.
