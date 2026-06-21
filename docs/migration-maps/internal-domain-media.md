# `internal/domain/media/` — Completed Migration

## Status

**done** (June 2026) — directory eliminata. Tutti i tipi (SourceType, AssetStatus,
AssetNode, AssetExecutionResult, ClipFolder, ClipManifest, PipeelineStrategy,
NormalizeStrategy, ActiveKey, MonitoredSource, Subject, ImageAsset, ImageUsage,
ImageTag, CategoryChannel, SearchQuery, SearchQueryResult, GenerationStyle,
MediaType) erano già duplicati in `internal/domain/asset/`. Nessun importatore
residuo in codice Go. Target: `internal/domain/asset/`.

## What exists

Three files:

- `internal/domain/media/media.go` — 1 type (`Media`)
- `internal/domain/media/styles.go` — 1 type (`Style`) + lookup helpers
- `internal/domain/media/image.go` — 1 type (`ImageAsset`)

These are the legacy `domain/media` shapes that existed from the Phase-1
domain split. They are being **merged** with `internal/domain/asset/` (the
target).

## Migration target

Per the Phase-2 rebrand notes:

> `internal/domain/asset/asset.go` is intentionally DISTINCT from
> `internal/domain/media/`. The two are converging toward a SINGLE
> MediaType abstraction.

After this migration:

- `Media`     → `domain/asset.MediaType` (or absorb into `Asset.Type`)
- `Style`     → `domain/asset.Style` (or absorb into `Asset.Styles`)
- `ImageAsset` → `domain/asset/Image` (or absorb into `Asset` type discriminator)

Recommended approach: don't move the files file-by-file — re-author them at
the target and add typed conversion shims in `domain/asset/` so importers
can be migrated in bulk.

## Audit results

Audit method: `rg 'internal/domain/media' --type go | wc -l`.

| Probe | Result |
|---|---|
| Total importers                              | 46 (across roughly: api/, application/, media/, sources/, artifacts/, infrastructure/, cmd/) |
| Direct type references on production paths   | ~12 unique types |
| Tests that depend on the legacy shape names  | ~5 |

Largest importers (rough counts from `rg -c 'internal/domain/media' --type go`):

1. `internal/application/images/` — 14 imports (the heaviest user; biggest
   cut-over risk). Mitigation: cutovers in `images/` go one method at a time
   during this wave.
2. `internal/sources/youtube/` — 5 imports (extractor + intelligence files).
3. `internal/sources/artlist/` — 1 import (run_helpers).
4. `internal/api/{script,sources,channels}/` — 7 imports combined.
5. `internal/infrastructure/database/sqlite/` — 4 imports (repos should
   switch to domain/asset types first).

## Cut-over recipe

1. Define the canonical target types in `internal/domain/asset/` (or fold
   into the existing `asset.go` aggregate).
2. Add type-conversion helpers (e.g. `domainmedia.Media → asset.MediaType`)
   in either `domain/media/` (during transition) or `domain/asset/` (after).
3. For each heavy importer, perform an import-by-import rewrite. Group
   imports by directory so a single PR can migrate one application layer
   end-to-end.
4. Drop `internal/domain/media/` only when 0 importers remain for 30 days.

## Owner

Wave-14. Estimated effort: 6–10 PRs spread across iimages/,
sources/youtube/, sources/artlist/, api/(script|sources|channels)/,
infrastructure/database/sqlite/.

## Why so gnarly?

Because the legacy `domain/media.Media` type is used as a discriminator
across many layers — image, audio, clip, voiceover, etc. — the cut-over
must preserve the discriminator semantics exactly. This is the wave with
the highest risk of silent regressions. Plan that PRs include a test round
trip for each importer (encode → decode → equality on every media type).
