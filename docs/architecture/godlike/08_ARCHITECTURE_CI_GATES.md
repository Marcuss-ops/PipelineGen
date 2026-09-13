# Architecture CI Gates

The architecture CI gates enforce structural rules via `cmd/archcheck` (run by
`.github/workflows/architecture.yml` and by `make verify-main` / `make
archcheck-strict`). `scripts/ci-architectural-checks.sh` no longer exists — it
was deleted by commit `7e6965aab`; `cmd/archcheck` is the single owner.

## Purpose

Keep the codebase aligned with the canonical architecture by catching dependency and structural regressions at CI time.

## Mandatory checks

- `go run ./cmd/archcheck --strict` must pass (this is the CI form; a non-zero
  exit also covers the fail-closed debt-budget check).
- `make verify-main` must pass before pushing (its pre-push hook runs it).
- `python3 scripts/regen_hotspots.py --check` must pass — the hotspot registry
  `current` counts are regenerated from the tree and go stale as packages move.

## Boundary checks

- Only `internal/app`, `internal/kernel`, `internal/capabilities`, and
  `internal/platform` are target roots; the legacy roots are gone (see Legacy
  checks). Enforced by `percheck_legacy_root_ban` and
  `percheck_legacy_root_new_code`.
- `percheck_brain_infra_ban` and `percheck_brain_single_impl` keep the brain
  capability free of infrastructure imports and of a second implementation.
- Beyond those, layering is a REVIEW rule, not a machine gate: there is no
  generic "capabilities must not import platform" check in the hard-gate set.
  Treat "`internal/app` is the only composition root; dependency construction
  belongs there" as a design contract enforced by review, so do not cite this
  document as evidence that a machine enforces it.

## Registry checks

- New routing, provider selection, source policy, sampling, or resolution logic must enter a shared registry, resolver, or sampler.
- Do not duplicate decision logic across handlers.

## Legacy checks

- Stale prose paths and deprecated references are flagged.
- Legacy compatibility entries must have an owner and deadline.
- `internal/app`, `internal/kernel`, `internal/capabilities`, and
  `internal/platform` are the only target roots.
- `internal/application`, `internal/api`, `internal/infrastructure`, and
  `internal/domain` no longer exist: the WAVE-24 migration completed and the
  four roots were deleted, so `percheck_legacy_root_ban` enforces the absence
  rather than a migration-only allowance. `architecture/package_hotspots.json`
  records hot packages (with owner + deadline), not legacy roots.

## Contract checks

- Generated API documentation must match registered routes.
- Typed ports must be satisfied structurally by infrastructure adapters.

## Data checks

- **PostgreSQL + pgvector is the durable authority for the media domain**
  (`media_assets`, `asset_locations`, `media_asset_features`,
  `media_embeddings`, `asset_text_tracks`, `asset_renditions`,
  `media_asset_sources`, `registry_events`, media outbox). SQLite is the
  durable authority only for the enumerated non-media operational domains
  (jobs, delivery_log, scripts, cache, idempotency, artifacts/staging,
  observability).
- **Qdrant is not a media store.** It may not be used as a media read, write,
  or projection; the only surviving consumers are explicitly-justified
  non-media ones (mediamemory, maintenance DR, admin audit tooling). Media
  vector search is owned by `internal/platform/postgres/media.MediaSearcher`
  (the canonical `search.VectorStorePort`).
- `percheck_media_assets_writer_canonical` bans direct SQL writes to
  `media_assets` and `asset_locations` from every package except the canonical
  owner (PostgreSQL + pgvector media SSOT, September 2026).
  - **Scan scope**: `internal/` **and** `cmd/`. `cmd/` is included because the
    documented claim is repo-wide; leaving it out made the claim false for the
    admin CLI. Test files and SQL migrations are out of scope.
  - **Covered verbs**: `INSERT OR REPLACE` / `INSERT` / `REPLACE` / `UPDATE` /
    `DELETE FROM` on `media_assets`.
  - **Ownership is by PACKAGE, not filename**: resolved through
    `policy.IsCanonicalMediaWriter` (`cmd/archcheck/policy/exempt.go`). The whole
    `internal/platform/postgres/media/` package is the SSOT, plus a closed list
    of narrow non-package writers. A hand-maintained filename list drifted the
    moment a new canonical file landed (`delete_saga.go`), which is why the
    package prefix replaced it.
  - **Test-only support** is an EXACT-file exemption
    (`policy.TestOnlySupportFiles`), not a directory prefix, so a new writer
    materialised under a `testsupport/` directory is still a violation.
  - The finalizer fence (no direct writes to `asset_locations` / `outbox_events`
    from the finalizer scope) is owned by this same gate via
    `mediaAssetsWriterScopedRules`; it was merged out of the retired
    `percheck_finalizer_no_direct_sql`.

## Complexity budgets

- Package size and file length limits are enforced.
- Constructor and struct dependency limits are enforced.

## Generated output

- Generated code must be checked in and match the source of generation.
- Golden files must be updated explicitly when contracts change.

## Zero-baseline rule

New violations are not grandfathered. Fix the architecture, do not add exceptions.
