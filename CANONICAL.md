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
| Qdrant schema | Go adapter under `internal/platform/qdrant/schema` (owned by `internal/platform/qdrant`). NOT a media source of truth: the media Qdrant projection is retired, the machine-readable `architecture/qdrant/v3-schema.json` was deleted with it, and only the non-media consumers (mediamemory frames/concepts, maintenance DR, admin audit) read this schema |
| Operational procedures | current files under `docs/operations` |
| Clip pre-planner pipeline (input → planner → search → sampler → view redaction → generator → binding) | `docs/operations/clip-pre-planner.md` |
| CI exceptions | allowlists under `docs/migrations` |

## Conflict rule

When documentation conflicts with code, generated routes, tests, or machine-readable policy, executable sources win. Correct or delete the stale prose immediately.For the internal tree, the binding decision is singular: `app`,
`kernel`, `capabilities`, and `platform` are the only roots — and the only ones
on disk. The former `application`, `api`, `infrastructure`, and `domain` roots
are DELETED (verified absent 2026-09-13); `architecture/policy.yaml` declares
`legacy_internal_roots` intentionally absent. Re-creating one of them is a
violation enforced forward by `percheck_legacy_root_new_code`,
`percheck_legacy_root_ban`, and `percheck_api_infrastructure_imports`.

## Documentation policy

Do not add action-plan diaries, completed wave reports, snapshots, evidence dumps, future-dated completion reports, or duplicate architecture explanations. Use issues for active work and Git history for completed work.
