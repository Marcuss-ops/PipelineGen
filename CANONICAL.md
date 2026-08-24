# PipelineGen — Canonical Sources

Only current, executable information belongs in the working tree. Historical plans and closure evidence belong in Git history.

| Concern | Canonical source |
|---|---|
| Engineering and Git rules | `AGENTS.md` |
| Current system architecture and target root decision | `ARCHITECTURE.md` |
| Build and startup | `README.md` |
| Live HTTP routes | `docs/api/ACTIVE_API_GENERATED.md` |
| Architecture policy, target roots, and legacy-root restrictions | `architecture/policy.yaml` |
| Legacy-root migration ownership and deadlines | `architecture/package_hotspots.json` |
| Architecture policy navigation | `docs/architecture/godlike/INDEX.md` |
| Capability ownership | `architecture/ownership.generated.yaml` |
| Active exceptions only | `architecture/current.yaml` and `architecture/issues.yaml` |
| Compatibility removals | `architecture/deprecations/` |
| Qdrant schema | current adapter under migration-only `internal/platform/qdrant/schema`; target platform ownership is `internal/platform/qdrant`; machine-readable files under `architecture/qdrant` |
| Operational procedures | current files under `docs/operations` |
| Clip pre-planner pipeline (input → planner → search → sampler → view redaction → generator → binding) | `docs/operations/clip-pre-planner.md` |
| CI exceptions | allowlists under `docs/migrations` |

## Conflict rule

When documentation conflicts with code, generated routes, tests, or machine-readable policy, executable sources win. Correct or delete the stale prose immediately.

For the internal tree, the binding decision is singular: `app`, `kernel`,
`capabilities`, and `platform` are the only target roots. `application`, `api`,
`infrastructure`, and `domain` are migration-only zones. They must not receive
new capabilities, public contracts, providers, routes, files, or packages;
legacy-zone changes require an existing migration record in
`architecture/package_hotspots.json`.

## Documentation policy

Do not add action-plan diaries, completed wave reports, snapshots, evidence dumps, future-dated completion reports, or duplicate architecture explanations. Use issues for active work and Git history for completed work.
