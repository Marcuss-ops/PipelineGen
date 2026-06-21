# `internal/application/assets/artifacts/` — Wave 7 ✅ COMPLETED

## Status

**completed** — wave-7. The package was lifted-and-shifted from `internal/artifacts/`
to `internal/application/assets/artifacts/`. Package name preserved as `artifacts`.

**Future work**: per the original plan below, the package should be eliminated by
splitting its components across domain/application/infrastructure layers. This
lift-and-shift is an intermediate step.

---

## Original plan (pre-wave-7)

## What exists

12 files in `internal/artifacts/` + `artifacts/resolvers/`:

| File | Public surface |
|---|---|
| `service.go` | `Service` (high-level facade) |
| `repository.go` | `Repository` (legacy in-mem blob store) |
| `registry.go` | `Registry` (asset-id registry; the actual useful one) |
| `local_blob.go` | `LocalBlob` (data struct) |
| `finalizer.go` + `finalizer_test.go` | `Finalizer` |
| `converters.go` | `Converter` helpers |
| `clips_adapter.go` | `ClipsAdapter` |
| `source_resolver.go` | `SourceResolver` |
| `types.go` + `types_test.go` | domain types |
| `verifier.go` | drive-link verifier |

## Migration target

| Sub-symbol | Target |
|---|---|
| `Registry`           | `internal/domain/asset/registry.go` (the asset-id registry is a domain concept) |
| `Finalizer`          | `internal/application/asset/finalizer.go` |
| `Service`            | eliminate — caller becomes a use case in `internal/application/asset/` |
| `Repository`         | delete — superseded by `assets/SQLite` repo + `domain/asset/` aggregate |
| `LocalBlob`          | inline at call site (used in <3 places; can become a local struct) |
| `ClipsAdapter`       | `internal/application/asset/clips_adapter.go` |
| `SourceResolver`     | `internal/infrastructure/source/resolver.go` |
| `verifier.go`        | `internal/infrastructure/drive/verifier.go` |
| `DriveVerifier`      | already wrapped in `artifacts.NewAPIDriveVerifier(...)`; collapse into `internal/infrastructure/drive/` |
| `converters.go`      | delete if unused after the move (audit required) |
| `types.go`           | split per-target |

## Cut-over recipe (elimination, not relocation)

1. Migrate `Registry` first — it has the most importers and is the only
   non-trivial surface. Move it to `domain/asset/registry.go`.
2. Migrate `Finalizer` to `application/asset/finalizer.go`.
3. Migrate `ClipsAdapter` + `SourceResolver` + `verifier.go` to their
   application/infrastructure homes.
4. Eliminate `Service` — replace call sites with the use case that was
   previously delegated to it.
5. Delete `Repository` + `LocalBlob` + `converters.go` + the rest.
6. Drop the entire `internal/artifacts/` directory. Update
   `architecture/migration.yaml`.

## Why eliminate rather than move?

`artifacts.Service` is a *facade over a facade*: it composes
`Registry`, `Finalizer`, and `DriveVerifier` for callers, but the callers can
just inject those primitives (and the lifecycle.go factory
`NewLifecycleFromDeps` already does most of this). The package as a
whole adds ceremony without adding abstraction.

## Owner

Wave-15. Estimated effort: 4–6 PRs. Highest payoff of any wave.
