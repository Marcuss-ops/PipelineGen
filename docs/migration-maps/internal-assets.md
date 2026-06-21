# `internal/assets/` — ✅ COMPLETED (Pre-wave-7)

**Status**: Directory eliminated. Content absorbed into `internal/domain/asset/`.

---

## Original plan

## Status

**pending** — deferred to Wave-14. Target: `internal/domain/asset/`.

## What exists

32 Go files in `internal/assets/` (roughly the canonical aggregate reader for
`media_assets` and its 3 satellite tables — `asset_locations`,
`asset_processing`, `asset_versions`). The package name is `assets`.

## Migration target

Move every file in `internal/assets/` to `internal/domain/asset/`. The package
name should become `asset` (no `s`) for consistency with the rest of
`internal/domain/`. Per the pre-Phase-2 notes:

> `internal/domain/asset/asset.go` is intentionally DISTINCT from
> `internal/domain/media/` (the legacy parcel). The two are converging
> toward a single MediaType abstraction.

## Cut-over recipe

1. Move files into `internal/domain/asset/` keeping package name `asset`.
2. Update importers with `rg -l 'pipelinegen/internal/assets' --type go | xargs sed -i 's|internal/assets|internal/domain/asset|g'`.
3. Verify `go build ./...` and `go test ./internal/domain/asset/...`.
4. Delete `internal/assets/` once the rebrand is complete.
5. Update `architecture/migration.yaml` (the existing entry for
   `internal/assets`).

## Owner

Wave-14. Estimated effort: single PR (mechanical rename), plus 1 deletion PR
when zero importers remain.

> **PR0 status footer (June 2026, fd8e3a43+/+2)** — L'intestazione
> `✅ COMPLETED` sopra è già coerente con il codice: `internal/assets/`
> non esiste più, tutti i consumer vivono in `internal/domain/asset/`
> (vedi Wave 4A status `done` nel `architecture/migration.yaml`).
