# Data and Config Ownership

This document defines canonical ownership for data, configuration, and state in PipelineGen.

## Durable authority

SQLite is the durable authority for all canonical state. Any other store is a projection and must be rebuildable from SQLite.

## Data store inventory

Concrete owner-per-fact map. Canonical business state is SQLite; every other store is either a rebuildable projection, a side-effect surface, or append-only telemetry. Pointers cite the canonical code owner, not a line number.

### Stores and their role

| Store | Path (canonical resolver) | Role | Rebuildable from |
|---|---|---|---|
| Primary SQLite | `cfg.Storage.PrimaryDBFullPath()` → `media/media.db.sqlite` | **SSOT** — canonical business state (jobs, assets, scripts, voiceovers, outbox) | — |
| acquisition.SourceStager | `internal/application/acquisition/port.go` | **SSOT** — canonical source staging port (Prepare/Release lifecycle). All consumers (YouTube, Artlist, Stock, Images, Jobs/assets) use this port. The legacy `assets.SourceStager` has been removed (CONTRACT completed 2026-08-22). | — |
| Observability SQLite | `cfg.Storage.ObservabilityDBFullPath()` → `observability/api_requests.db.sqlite` | **SSOT for the observability axis** (run/attempt/stage/operation timing + API audit) | distinct concern; derived from job execution, not from business tables |
| Qdrant | runtime alias per `ProjectionContract` | **Projection** — semantic/lexical retrieval | primary SQLite |
| Google Drive | remote | **Side-effect surface** — delivery location for bytes | SQLite metadata is authoritative; Drive is reconciled against it |
| Local filesystem | `MediaDir`/`CacheDir`/`StagingDir`/`WorkspaceDir` | **Side-effect surface** — staging/cache blobs | redownloadable / regenerable |
| Legacy catalog DBs | `data/stock/stock.db.sqlite`, `data/artlist/artlist.db.sqlite`, `data/artlist_videos.db`, `data/clips.db.sqlite` | **ELIMINATED** — merged into primary via `unify-catalogs` (run the tool, reconcile rows/hashes/locations, backup, then rm) | primary `media_assets` (source='stock'/'artlist') |

### Primary SQLite — fact families (SSOT)

The primary DB owns the following facts (one canonical owner each; tables cited, not exhaustive):

| Fact | Canonical tables | Notes |
|---|---|---|
| Job lifecycle + execution | `jobs`, `job_events` | immutable logical request; retry/lease/CAS state lives here |
| Media asset record | `media_assets` | one canonical SQLite row per asset; deterministic asset ID |
| Asset indexes + links | `asset_index`, `asset_links` | content-addressed search indexes |
| Clip folders | `clip_folders` | Drive folder registry |
| Voiceovers | `voiceovers` | per-item row (id, text_hash, language, drive ids) |
| Scripts | `scripts`, `script_sections`, `script_stock_matches` | script surface |
| Durable post-commit work | `outbox_events` | versioned events; idempotent `event_key` |
| Media memory | `media_bindings`, `media_concepts` | phrase→entity bindings; canonical fingerprint |
| Registry / taxonomy | `media_registry_ledger`, `content_objects`, `asset_content_link`, `control_plane_meta`, `job_registry`, `performance_registry` | capability registry + lineage |
| Migration ledger | `schema_migrations` | SHA-256 checksummed migration history |

### Observability SQLite — fact families (SSOT for the observability axis)

`internal/kernel/observability` owns the observability vocabulary and report shape; SQLite here is its durable projection surface, distinct from business state (see `docs/architecture/job-attempt-run-observability-contract.md`).

| Fact | Canonical tables | Relationship |
|---|---|---|
| Attempt → run identity | `job_attempts` | 1 attempt = 1 run; `AttemptID`/`RunID` are independent durable IDs |
| Run lifecycle + timing | `run_observability` | RUNNING→terminal; wall/queue/blocked ms |
| Stage / operation / artifact / child observations | `run_stage_observations`, `run_operation_observations`, `run_artifact_observations`, `run_child_observations` | idempotent writes on observation identity |
| Capability workflow payload | `run_workflow_payload` | script request/result envelope (not a second run ledger) |
| API audit log | `api_requests` | append-only; retention-rotated via `admin db rotate` |

### Qdrant projections (rebuildable)

The three projections are closed over by `internal/platform/qdrant/schema/projection_contract.go` and must never share a point ID, alias, or retention scope (`ValidateProjectionSeparation`).

| Projection | Schema | Point ID | Canonical SQLite source | Rebuild path |
|---|---|---|---|---|
| `media_assets` | `DefaultV3Schema()` | bare asset ID (UUID v8) | `media_assets` | `ProjectionManager.RebuildV4` (blue-green, signed, golden-query certified) |
| `media_frames` | `FrameIndexSchema()` | `frame-` + UUID | keyframes `(video_id, ts_ms)` | `ProjectionManager.RebuildProjection` |
| `media_concepts` | `ConceptIndexSchema()` | `concept-` + ID | `media_concepts` / `media_bindings` | `ProjectionManager.RebuildProjection` |

Rebuild discipline: writes flow only through the generic `ProjectionWriter` capability; a rebuild never mutates SQLite, and a failed/retired generation is retired by alias switch (`ReconcileProjection`).

### Caches (derived projections)

Derived caches are projections keyed by their canonical SQLite record and are safe to drop/rebuild on demand (never business truth):

| Cache | Canonical source | Invalidation |
|---|---|---|
| `research_cache` / `vidrush_provider_cache` | search/research results | TTL + provenance columns |
| `artifact_cache` | published artifact receipts | content-addressed |
| Subtitle cache (`SubtitlesPath`) | per-video `.vtt` from the fetcher | re-downloaded on miss |
| Artlist/stock search caches | provider responses | warm-on-miss from canonical query |

### Per-fact matrix

| Fact | SSOT | Projection / derived copy | Reconstruction |
|---|---|---|---|
| "this asset exists, its metadata, its state" | `media_assets` (primary) | Qdrant `media_assets` point | reindex from `media_assets` |
| "this keyframe exists at (video_id, ts_ms)" | keyframe source (primary/asset) | Qdrant `media_frames` point | reindex keyframes |
| "this phrase binds to this entity" | `media_concepts`/`media_bindings` (primary) | Qdrant `media_concepts` point | reindex bindings |
| "this job is RUNNING/SUCCEEDED/FAILED" | `jobs` (primary) | `run_observability`/`job_attempts` (observability) | replay from job lifecycle |
| "this file is on Drive at X" | `media_assets.drive_file_id` etc. (primary) | Drive folder structure | Drive reconcile (`/api/drive/reconcile`) |
| "this source is staged for processing" | `acquisition.SourceStager.Prepare` (canonical port) | Local staged file + CleanupToken | Re-Prepare same SourceRef within TTL |

## One owner per fact

Every fact in the system has one canonical owner. No two packages may independently compute or store the same fact.

## Database rules

- Apply migrations only to the database that owns the affected tables.
- Prefer expand, backfill, cutover, contract for compatibility changes.
- Preserve deterministic asset IDs and idempotent job/outbox keys.

## Qdrant projection

Qdrant is a derived projection and must be completely rebuildable from SQLite. It is never the source of truth.

## Drive and filesystem

Google Drive and local filesystem are side-effect surfaces. Writes must be durable in SQLite before being emitted through the transactional outbox.

## Configuration

Configuration is owned by the composition root (`internal/app`). Runtime configuration is loaded once at startup and treated as read-only.

## Future storage changes

Any new storage backend must be introduced behind a port, wired only at the composition root, and documented in this file before use.
