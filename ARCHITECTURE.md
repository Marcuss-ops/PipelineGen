# PipelineGen Architecture

PipelineGen is a headless Go backend for discovering, processing, indexing, and delivering media.

## System model

| Concern | Owner |
|---|---|
| Canonical business state | PostgreSQL + pgvector (media domain); SQLite remains canonical for non-media domains during staged migration |
| Long-running execution | SQLite-backed jobs and workers |
| Durable post-commit work | Transactional outbox |
| Semantic and lexical retrieval | Qdrant projection |
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

PostgreSQL + pgvector is authoritative for the media domain. SQLite remains authoritative for non-media domains during staged migration. Qdrant is no longer a media authority and may not be used as a fallback search or write path. Drive and local storage remain external locations and must not become hidden sources of business truth.

### Data-layer unification (August 2026)

The sync direction is **always and only** `SQLite → Outbox → Qdrant`:

| Invariant | Rule |
|---|---|
| **SSOT** | SQLite `media_assets` is the single source of truth |
| **Write gate** | `persistence.AssetCommitter` is the sole canonical writer — no direct SQL to `media_assets` outside it (enforced by `percheck_media_assets_writer_canonical` CI gate) |
| **Projection** | Qdrant `media_assets` is a derived projection, fully rebuildable from SQLite (`reindex-qdrant --apply --in-place`) |
| **Runtime collection** | Only `media_assets` (alias `media_assets_current`); recovery/test/synthetic collections are forbidden at runtime (`schema.IsRuntimeCollection` gate) |
| **Empty projection** | `INDEX_UNAVAILABLE / REBUILD_REQUIRED` — never a fallback to a recovery collection |
| **Search** | Qdrant returns candidate asset IDs + score; SQLite returns canonical content (name, drive_link, lifecycle, metadata) |
| **Reconciler** | `ReconcileProjection` is the sole SQLite→Qdrant repair gate (missing→index, stale→reindex, orphan→delete, hash_mismatch→reindex) |
| **Emergency recovery** | `recover-registry-from-qdrant` is EMERGENCY ONLY (disaster recovery / forensics), lives in `cmd/admin/emergency/` — never a normal sync path |

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

Job policy, handler registration, retries, concurrency, leases, cancellation, progress, and deduplication are centralized in the job system.

## Job capability layers

The root `internal/capabilities/jobs` package remains the compatibility facade and
orchestration boundary. Reusable policy slices are owned by focused subpackages:

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
- `docs/architecture/godlike/INDEX.md` is the central navigation map for the 5 godlike governance docs (ownership, zero-legacy, CI gates, agent playbook, feature removal).

Completed work, historical decisions, plans, evidence, and snapshots are not part of the working tree.
