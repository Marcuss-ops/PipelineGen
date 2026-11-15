# P4 Qdrant/SQLite retirement boundary (2026-11-15)

## Decision

Qdrant and SQLite are **not** deleted by P4. `AGENTS.md` makes Qdrant valid for
non-media consumers and SQLite authoritative for non-media domains. Only the
media-legacy projections are already forbidden. The remaining work is a
non-media consumer inventory and a written migration boundary, not a deletion.

## Current footprint

| Store | Go files referencing it | Top consumer areas |
|---|---:|---|
| Qdrant | 131 | `internal/platform` 79, `internal/app` 21, `cmd/archcheck` 14, `internal/capabilities` 13, `cmd/admin` 4 |
| SQLite | 391 | `internal/capabilities` 121, `internal/platform` 101, `internal/app` 91, `cmd/admin` 36, `cmd/archcheck` 24 |

The counts include tests and governance scanners; they are inventory signals,
not proof of a runtime dependency.

## Valid non-media consumers

These remain valid and are not migration targets:

- Semantic/text/embedding indexing and search through `platform/qdrant`.
- Jobs, outbox, leases, scheduling, and idempotency through `platform/sqlite`.
- Admin reconciliation, audit, backup, and migration tooling through the
  canonical platform adapters.
- Architecture scanners that read store schemas/imports as governance data.

## Forbidden surface

- Any media-legacy projection that bypasses the canonical media committer.
- Direct SQL writes to `media_assets` outside the canonical writer.
- New Qdrant reads/writes from media code paths that already have a canonical
  media store.
- A second owner for the same non-media fact (for example, a new direct SQLite
  writer beside the existing capability port).

## Boundary rules for the next migration

1. Every non-media consumer must enter through `platform/qdrant` or
   `platform/sqlite`; capability packages may define ports but must not open
   their own clients.
2. A non-media domain is a migration candidate only when its facts are already
   owned by a capability and the store is an implementation detail.
3. Media-legacy projections are removed first; they are already forbidden and
   do not need a new storage decision.
4. Admin and archcheck are control-plane exceptions; they may inspect stores
   but must not become runtime writers.
5. No Qdrant/SQLite dependency is removed from `go.mod` until the consumer
   inventory for both stores is at zero for the retired surface.

## Acceptance before any deletion

- A per-domain consumer table with owner, port, and store call path.
- A written statement that the domain's canonical owner is not the store.
- Zero media-legacy projection callers.
- Build, strict archcheck, and the domain tests pass after the consumer moves.

Until those conditions hold, this document is the boundary; deletion is out of
scope.
