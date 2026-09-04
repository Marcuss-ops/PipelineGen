# PipelineGen Architecture

PipelineGen is a headless Go backend for discovering, processing, indexing, and delivering media.

## System model

| Concern | Owner |
|---|---|
| Media canonical business state | PostgreSQL + pgvector |
| Non-media canonical business state | SQLite where that domain has not migrated |
| Long-running execution | SQLite-backed jobs and workers |
| Durable post-commit work | Transactional outbox owned by each canonical store |
| Media semantic and lexical retrieval | pgvector inside the media PostgreSQL SSOT |
| Non-media vector use | Qdrant only where an explicit non-media owner requires it |
| Remote files | Google Drive |
| Dependency construction and lifecycle | `internal/app` |
| Shared semantic contracts | `internal/kernel` |
| Business capabilities and use cases | `internal/capabilities` |
| Technical adapters and transport platform | `internal/platform` |

`internal/app`, `internal/kernel`, `internal/capabilities`, and
`internal/platform` are the only target roots. The existing
`internal/application`, `internal/api`, `internal/infrastructure`, and
`internal/domain` roots are migration-only zones: **no new capabilities, no
new public contracts, no new providers, no new routes, and no new files or
packages** may be introduced there. Changes in those zones are limited to
migration work, correctness/security fixes required to keep the system
running, or removal of legacy code, and must have a registered migration owner
and deadline in `architecture/package_hotspots.json`.

PostgreSQL + pgvector is authoritative for the media domain. SQLite remains
authoritative only for non-media domains that have not migrated. Qdrant is not
a media authority and may not be used as a media fallback search, hydration,
indexing, or write path. Drive and local storage remain external locations and
must not become hidden sources of business truth.

### Media data-layer unification (September 2026)

The canonical media flow is **PostgreSQL only**:

```text
producer
  -> PostgresMediaCommitter
  -> media_assets + asset_locations + PostgreSQL outbox
  -> PostgresIndexWorker
  -> media_embeddings (pgvector)

search request
  -> MediaSearcher pgvector retrieval
  -> MediaSearcher media_assets hydration
  -> Candidate
  -> authorized delivery URL
```

| Invariant | Rule |
|---|---|
| **Media SSOT** | PostgreSQL `media_assets`, `asset_locations`, features, text tracks, outbox, and `media_embeddings` |
| **Write gate** | `PostgresMediaCommitter` is the canonical production media writer |
| **Index projection** | `PostgresIndexWorker` writes embeddings into PostgreSQL `media_embeddings`; Qdrant is not a media projection |
| **Search** | `MediaSearcher` owns both `VectorStorePort` and `MediaReadRepository`, so retrieval and hydration cannot split across databases |
| **Workspace/lifecycle scope** | Hard predicates are enforced in PostgreSQL retrieval; hydration repeats applicable guards and never broadens the selected ID set |
| **Configuration** | `media_postgresql.enabled=true` requires a valid DSN and fails closed on invalid/unreachable PostgreSQL |
| **Qdrant** | Forbidden for media reads/writes; permitted only for explicitly owned non-media capabilities |
| **SQLite** | No media runtime fallback; legacy SQLite data is migration/backfill input only |
| **Emergency tools** | Historical migration/recovery tools are administrative only and never part of normal runtime routing |

## Process entry points

- `cmd/server`: HTTP server and selected background runtime.
- `cmd/worker`: worker process consuming the shared job broker.
- `cmd/admin`: migrations, backfills, reconciliation, diagnostics, and generated documentation.

Entry points remain thin and delegate construction to `internal/app`.

## Dependency zones

```text
cmd
  -> internal/app
       -> internal/capabilities
       -> internal/kernel
       -> internal/platform

internal/app is the composition root. Capabilities depend on kernel contracts
and typed ports; platform supplies concrete adapters and transport mechanics.
The former application/api/infrastructure/domain roots remain only as
migration-only zones and are not valid homes for new architecture.

pkg is leaf-only and must not import internal packages.
```

`internal/app` may compose all target roots. `internal/capabilities` owns
business behavior, `internal/kernel` owns genuinely shared semantic contracts,
and `internal/platform` owns concrete I/O adapters and HTTP/server mechanics.
Legacy-root code must move toward these owners rather than creating another
parallel architecture.

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

Job policy, handler registration, retries, concurrency, leases, cancellation,
progress, and deduplication are centralized in the job system.

## Job capability layers

The root `internal/capabilities/jobs` package remains the compatibility facade
and orchestration boundary. Reusable policy slices are owned by focused
subpackages:

- `internal/capabilities/jobs/queue` owns enqueue validation and identity, claim
  capability/wait policy, and retry-budget/due decisions.
- `internal/capabilities/jobs/scheduling` owns polling state transitions,
  polling and persisted-retry backoff, preparation planning/registry, and
  resource-aware speculation. It contains policy only; workers and stores retain
  I/O, lifecycle effects, persistence, and telemetry.
- `internal/capabilities/jobs/finalize` owns artifact-manifest conversion and
  SQLite contention classification shared by finalization consumers.

`kernel/job.Store` remains the canonical persistence contract for enqueue,
claim, retry, and schedule-retry transitions; the extracted capability layers
must not duplicate SQLite state transitions or import the root `jobs` package.

## Transactional outbox

Every domain emits durable post-commit work from its own canonical store. For
the media domain that store is PostgreSQL; non-media domains may still use
SQLite.

```text
BEGIN
  write canonical row
  write versioned outbox event
COMMIT

outbox worker
  -> claim
  -> execute adapter side effect
  -> complete, retry, supersede, or dead-letter
```

## Media indexing and search

A media asset is authoritative in PostgreSQL. Named embedding families such as
text, transcript, visual, and audio are stored in `media_embeddings` using
pgvector with explicit model/type identity. Lexical retrieval uses the same
PostgreSQL media rows.

The canonical retrieval flow is:

```text
query normalization
  -> hard PostgreSQL metadata filters
  -> pgvector dense / PostgreSQL lexical retrieval
  -> fusion
  -> PostgreSQL media_assets hydration through the same MediaSearcher
  -> optional lightweight reranking
  -> deduplication and diversification
  -> authorized delivery URLs
```

Search profiles and source-specific behavior belong in shared registries or
resolvers, not duplicated handlers. The semantic backend composition gate
requires one adapter to implement both vector retrieval and media hydration;
a Qdrant-only adapter therefore cannot become a media search backend.

## Drive and files

Local files are staging/cache data. Google Drive is a delivery location.
Application workflows publish through the canonical delivery publisher; raw
Drive SDK clients stay in platform or admin-only tooling.

For stock timestamp extraction, one parent timestamp maps to one Drive folder
containing all child clips and one aggregated metadata file.

Re-delivery on a PUBLISHED-state `artifact_stages` row is a typed no-op
(`artifact.ErrTerminalStateRejection`) instead of silently overwriting
`published_location` and `published_at`: `(*artifactstages.Repository).MarkPublished`
gates on `state NOT IN ('PUBLISHED','SUCCEEDED','FAILED_PERMANENT')`, so a
duplicate Drive upload is structurally impossible.

## Configuration and ownership

- `architecture/policy.yaml` contains machine-enforced structural policy and
  declares the four target roots plus the migration-only legacy-root inventory.
- `architecture/package_hotspots.json` is the migration registry for legacy
  roots; every entry carries an owner, deadline, target, and explicit
  migration-only policy.
- `architecture/ownership.generated.yaml` is the generated capability ownership view.
- `architecture/current.yaml` contains active exceptions only.
- `architecture/issues.yaml` contains unresolved cross-package issues.
- `architecture/deprecations/` contains live compatibility removals only.
- `docs/api/ACTIVE_API_GENERATED.md` is the generated route surface.
- `docs/architecture/godlike/INDEX.md` is the central navigation map for the governance docs.

Completed work, historical decisions, plans, evidence, and snapshots are not
part of the working tree. Git history is the archive.
