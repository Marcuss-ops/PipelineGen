# `internal/upload/drive/` — Pending Migration

## Status

**pending** — deferred to Wave-15. Target: `internal/infrastructure/drive/`.

## What exists

`internal/upload/drive/` is a directory containing all Google-Drive-specific
upload, store, and resolver logic. Public surface includes:

- `drive.Uploader` (POST/multipart file upload)
- `drive.DocClient` (Google Docs API wrapper)
- `drive.Store` (asset storage layer that talks to Drive + the local mirror)
- `drive.Resolver` (path resolver for Drive roots; CONFLICTS with the
  `core/destination.Resolver` interface)
- `drive.NewDriveServiceFromFiles` (auth + SDK adapter)

## Migration target

Move every file into `internal/infrastructure/drive/`. The package name stays
`drive`. The pre-Phase-2 plan also calls for the
`drive.NewDestinationResolver` factory to be renamed to
`infrastructure/drive.NewResolver` (with the same constructor signature) so
that `mediaStore` can be typed against the canonical
`domain/asset/destination.Resolver` interface.

## Subtlety: dual `Resolver` interfaces

Both `internal/core/destination/` and `internal/upload/drive/` define a
`Resolver` interface. They are intentionally the SAME interface (both expose
`(Resolve(ctx, q) (Path, error)`), but live in different packages. After
the migration:

1. `core/destination.Resolver` becomes `domain/asset/destination.Resolver`
   (the canonical interface — see [internal-core.md](internal-core.md)).
2. `upload/drive.Resolver` is eliminated — the only caller of it is
   `mediaStore`, which should type itself against
   `domain/asset/destination.Resolver` and have the infrastructure-layer
   `drive.Resolver` (concrete impl) be injected via DI.

## Cut-over recipe

1. Add `destination.Resolver` interface in `internal/domain/asset/` (this
   should be done as part of the [internal-core.md](internal-core.md) wave).
2. Move every file from `internal/upload/drive/` to
   `internal/infrastructure/drive/`. Update the package import paths.
3. Update `mediaStore`'s field type from `*mediastorage.Store` to
   `*drive.Store`. Update `drive.NewResolver` returns
   `destination.Resolver`.
4. Eliminate `internal/upload/`. Update `architecture/migration.yaml`.

## Owner

Wave-15. Estimated effort: 1 mechanical PR + 1 small wiring PR +
1 deletion PR.
