# `internal/sources/` — Pending Migration

## Status

**pending** — deferred to Wave-15. Target: promoted into
`internal/application/assets/providers/`.

## What exists

Two sub-packages:

- `internal/sources/youtube/` — the YouTube fetch + clip extraction pipeline.
- `internal/sources/artlist/` — the Artlist fetch + scrape pipeline.

These packages were the original home of the source-specific business logic
before the `providers.Registry` was introduced in `registry.go::WireRegistry`.

## Migration target

| Legacy sub-package | Target |
|---|---|
| `internal/sources/youtube/<X>.go` (provider surface only — search + fetch via the registry) | `internal/application/assets/providers/youtube/` |
| `internal/sources/youtube/<X>.go` (extractor surface — separate concerns: clip extraction, intelligence, scripts) | split into `internal/application/assets/providers/youtube/` (provider interface) + `internal/infrastructure/youtube/` (extractor infra) |
| `internal/sources/artlist/<X>.go` (provider surface) | `internal/application/assets/providers/artlist/` |
| `internal/sources/artlist/<X>.go` (scraper infra) | `internal/infrastructure/artlist/scraper/` |

## Subtlety: don't move the scraper along with the provider

`internal/sources/artlist/` contains both the `Service` (provider surface,
used by the registry adapter) AND the long-running Node scraper daemon
(infrastructure — not a provider). The two should NOT collapse into
`internal/application/assets/providers/artlist/`. Move the provider surface
to that target; move the scraper binary + integration to
`internal/infrastructure/artlist/`.

## Cut-over recipe

1. **First** finish the provider-registry migration in
   `internal/app/registry.go::WireRegistry`. The adapter wiring already
   exists (`artlistadapter.NewAdapter`, `youtubeadapter.NewAdapter`). Once
   zero direct callers of `internal/sources/artlist.Service` and
   `internal/sources/youtube.Service` remain outside the registry, the
   move is purely mechanical.
2. For YouTube: separate the `internal/sources/youtube/extractor*.go` files
   into `internal/infrastructure/youtube/`. These are not "providers" —
   they are FFmpeg/youtube-dl subprocess helpers.
3. Move the provider surface to `internal/application/assets/providers/{youtube,artlist}/`.
4. Update `artlistadapter.NewAdapter` and `youtubeadapter.NewAdapter`
   imports.
5. Move the scraper files for Artlist to `internal/infrastructure/artlist/`.
6. Drop `internal/sources/{youtube,artlist}/`. Update
   `architecture/migration.yaml`.

## Owner

Wave-15. Estimated effort: 3–5 PRs.
