# Architecture CI Gates

The architecture CI gates enforce structural rules via `cmd/archcheck` and `scripts/ci-architectural-checks.sh`.

## Purpose

Keep the codebase aligned with the canonical architecture by catching dependency and structural regressions at CI time.

## Mandatory checks

- `cmd/archcheck` must pass.
- `scripts/ci-architectural-checks.sh` must pass.
- `make verify-main` must pass before pushing.

## Boundary checks

- API transport layer must not import infrastructure packages.
- Application layer must not depend directly on infrastructure implementations.
- `internal/app` is the only composition root.

## Registry checks

- New routing, provider selection, source policy, sampling, or resolution logic must enter a shared registry, resolver, or sampler.
- Do not duplicate decision logic across handlers.

## Legacy checks

- Stale prose paths and deprecated references are flagged.
- Legacy compatibility entries must have an owner and deadline.
- `internal/app`, `internal/kernel`, `internal/capabilities`, and
  `internal/platform` are the only target roots.
- `internal/application`, `internal/api`, `internal/infrastructure`, and
  `internal/domain` are migration-only roots. New capabilities, public
  contracts, providers, routes, files, and packages are prohibited there;
  migration records in `architecture/package_hotspots.json` provide ownership
  and deadlines but do not authorize extending a legacy root.

## Contract checks

- Generated API documentation must match registered routes.
- Typed ports must be satisfied structurally by infrastructure adapters.

## Data checks

- SQLite is the canonical state store.
- Qdrant projections must be rebuildable from SQLite.

## Complexity budgets

- Package size and file length limits are enforced.
- Constructor and struct dependency limits are enforced.

## Generated output

- Generated code must be checked in and match the source of generation.
- Golden files must be updated explicitly when contracts change.

## Zero-baseline rule

New violations are not grandfathered. Fix the architecture, do not add exceptions.
