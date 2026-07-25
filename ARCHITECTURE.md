# PipelineGen Architecture

PipelineGen is a headless Go backend for discovering, processing, indexing, and delivering media.

## System model

| Concern | Owner |
|---|---|
| Canonical business state | SQLite |
| Long-running execution | SQLite-backed jobs and workers |
| Durable post-commit work | Transactional outbox |
| Semantic and lexical retrieval | Qdrant projection |
| Remote files | Google Drive |
| Dependency construction | `internal/app` |
| Business orchestration | `internal/application` |
| Technical adapters | `internal/infrastructure` |
| HTTP transport | `internal/api` |

SQLite is authoritative. Qdrant and Drive are external projections or locations and must not become hidden sources of business truth.

## Process entry points

- `cmd/server`: HTTP server and selected background runtime.
- `cmd/worker`: worker process consuming the shared job broker.
- `cmd/admin`: migrations, backfills, reconciliation, diagnostics, and generated documentation.

Entry points remain thin and delegate construction to `internal/app`.

## Dependency zones

```text
cmd
  -> internal/app
       -> internal/api
       -> internal/application
       -> internal/domain
       -> internal/infrastructure

pkg is leaf-only and must not import internal packages.
```

`internal/app` may import every zone because it owns composition. Other zones follow inward-facing ports and domain contracts.

## Request and job flow

```text
HTTP request
  -> API validation
  -> application use case
  -> synchronous result
     or enqueue durable job
  -> worker lease
  -> registered job handler
  -> result, retry, or dead letter
```

Job policy, handler registration, retries, concurrency, leases, cancellation, progress, and deduplication are centralized in the job system.

## Transactional outbox

```text
BEGIN
  write canonical SQLite row
  write versioned outbox event
COMMIT

outbox worker
  -> claim
  -> execute adapter side effect
  -> complete, retry, supersede, or dead-letter
```

Indexing, deletion, cleanup, and other non-atomic external effects use this pattern.

## Media indexing and search

Each media asset has one canonical SQLite record. Qdrant stores a deterministic point with named channels such as text, transcript, visual, and sparse lexical search when available.

The preferred retrieval flow is:

```text
query normalization
  -> hard metadata filters
  -> dense and sparse retrieval
  -> fusion
  -> optional lightweight reranking
  -> deduplication and diversification
  -> hydrate canonical SQLite records
  -> authorized delivery URLs
```

Search profiles and source-specific behavior belong in shared registries or resolvers, not duplicated handlers.

## Drive and files

Local files are staging/cache data. Google Drive is a delivery location. Application workflows publish through the canonical delivery publisher; raw Drive SDK clients stay in infrastructure or admin-only tooling.

For stock timestamp extraction, one parent timestamp maps to one Drive folder containing all child clips and one aggregated metadata file.

Re-delivery on a PUBLISHED-state `artifact_stages` row is a typed no-op (`artifact.ErrTerminalStateRejection`) instead of silently overwriting `published_location` and `published_at`: `(*artifactstages.Repository).MarkPublished` gates on `state NOT IN ('PUBLISHED','SUCCEEDED','FAILED_PERMANENT')`, so a duplicate Drive upload is structurally impossible. Dashboards that counted publish-ops by overwrite volume (or by `MarkPublished` UPDATE RowsAffected) under-count — the per-row outcome is now `affected = 0`.

## Configuration and ownership

- `architecture/policy.yaml` contains machine-enforced structural policy.
- `architecture/ownership.generated.yaml` is the generated capability ownership view.
- `architecture/current.yaml` contains active exceptions only.
- `architecture/issues.yaml` contains unresolved cross-package issues.
- `architecture/deprecations.yaml` contains live compatibility removals only.
- `docs/api/ACTIVE_API_GENERATED.md` is the generated route surface.
- `docs/architecture/godlike/INDEX.md` is the central navigation map for the 5 godlike governance docs (ownership, zero-legacy, CI gates, agent playbook, feature removal).

Completed work, historical decisions, plans, evidence, and snapshots are not part of the working tree.
