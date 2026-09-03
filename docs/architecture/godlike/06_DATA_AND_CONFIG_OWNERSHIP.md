# Data and Config Ownership

This document defines canonical ownership for data, configuration, and state in PipelineGen.

## Durable authority

PostgreSQL + pgvector is the durable authority for the media domain. SQLite remains the durable authority for non-media domains during staged migration. Any derived media projection must be rebuildable from PostgreSQL; Qdrant is not a media fallback.

## Data store inventory

Concrete owner-per-fact map. Canonical business state is SQLite; every other store is either a rebuildable projection, a side-effect surface, or append-only telemetry. Pointers cite the canonical code owner, not a line number.

### Stores and their role

| Store | Path (canonical resolver) | Role | Rebuildable from |
|---|---|---|---|
| Primary SQLite | `cfg.Storage.PrimaryDBFullPath()` → `media/media.db.sqlite` | **SSOT for non-media domains during staged migration** (jobs, scripts, voiceovers, outbox) | — |
| Media PostgreSQL | canonical composition-root PostgreSQL DSN | **SSOT** — `media_assets`, `asset_locations`, `media_asset_features`, `media_embeddings` | — |
| acquisition.SourceStager | `internal/application/acquisition/port.go` | **SSOT** — canonical source staging port (Prepare/Release lifecycle). All consumers (YouTube, Artlist, Stock, Images, Jobs/assets) use this port. The legacy `assets.SourceStager` has been removed (CONTRACT completed 2026-08-22). | — |
| Observability SQLite | `cfg.Storage.ObservabilityDBFullPath()` → `observability/api_requests.db.sqlite` | **SSOT for the observability axis** (run/attempt/stage/operation timing + API audit) | distinct concern; derived from job execution, not from business tables |
| Qdrant | runtime alias per `ProjectionContract` | **Projection** — semantic/lexical retrieval | primary SQLite |
| Google Drive | remote | **Side-effect surface** — delivery location for bytes | SQLite metadata is authoritative; Drive is reconciled against it |
| Local filesystem | `MediaDir`/`CacheDir`/`StagingDir`/`WorkspaceDir` | **Side-effect surface** — staging/cache blobs | redownloadable / regenerable |
| Legacy catalog DBs | `data/stock/stock.db.sqlite`, `data/artlist/artlist.db.sqlite`, `data/artlist_videos.db`, `data/clips.db.sqlite` | **ELIMINATED** — merged into primary via `unify-catalogs` (run the tool, reconcile rows/hashes/locations, backup, then rm) | primary `media_assets` (source='stock'/'artlist') |

### Primary SQLite — non-media fact families (SSOT during staged migration)

The primary DB owns the following facts (one canonical owner each; tables cited, not exhaustive):

| Fact | Canonical tables | Notes |
|---|---|---|
| Job lifecycle + execution | `jobs`, `job_events` | immutable logical request; retry/lease/CAS state lives here |
| Media asset record | PostgreSQL `media_assets` | one canonical PostgreSQL row per asset; deterministic asset ID. All durable writes route through `persistence.AssetCommitter` |
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

### Media PostgreSQL schema (SSOT)

The media database owns four surfaces: `media_assets`, `asset_locations`, `media_asset_features`, and `media_embeddings`. The embeddings table uses pgvector and keeps model/type identity in the primary key so incompatible vector families cannot share an index. Hard scalar filters are indexed before vector indexes; HNSW is used for vector search where the selected embedding family has a fixed dimension.

During migration, PostgreSQL is first deployed and backfilled, then reads and writes are cut over behind the existing typed ports. No direct dual-write is permitted, and missing PostgreSQL configuration must fail closed rather than fall back to SQLite or Qdrant.

### Qdrant projections (retained only for non-cutover compatibility)

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

## Canonical media_assets writer family

`media_assets` writes have exactly one owner: the `persistence.AssetCommitter`
port (implemented by `PostgresMediaCommitter` in media-SSOT mode;
`SQLiteAssetCommitter` is migration-only) and its sibling
`persistence.CanonicalAssetWriter` surface (`SQLiteMediaCommitter`). Every
asset commit (YouTube, Artlist, local, voiceover, images, recovery) MUST
route through `AssetCommitter.CommitAndIndex` / `CommitTx`. The canonical
media search store is pgvector inside the same PostgreSQL SSOT
(`internal/platform/postgres/media.MediaSearcher` implements the canonical
`search.VectorStorePort`); in media-SSOT mode the composition root resolves
the pgvector plane fail-closed and no Qdrant media collection is consulted.
Direct SQL writes to `media_assets` outside this family
are banned and enforced by the `percheck_media_assets_writer_canonical` CI
gate (see godlike/08). Cutover certification:
`make certify-media-cutover` (POSTGRES_MEDIA_SSOT gate).

The canonical SQL-owning files (the SSOT family, 5 files as of the
asset-persistence unification cutover, August 2026):

1. `internal/platform/sqlite/assets/imagesregistry/asset_committer.go`
2. `internal/platform/sqlite/assets/imagesregistry/asset_committer_mutations.go`
3. `internal/platform/sqlite/assets/imagesregistry/asset_committer_projection_mutations.go`
4. `internal/platform/sqlite/assets/imagesregistry/canonical_clip_mutations.go`
5. `internal/platform/sqlite/assets/imagesregistry/media_committer.go`

The gate's allowlist (`mediaAssetsWriterCanonicalOwners` in
`cmd/archcheck/scan/boundaries/percheck_media_assets_writer_canonical.go`)
holds exactly these five files; any other file that writes `media_assets`
SQL must delegate to this family or be migrated before it can be committed.

## Database rules

- Apply migrations only to the database that owns the affected tables.
- Prefer expand, backfill, cutover, contract for compatibility changes.
- Preserve deterministic asset IDs and idempotent job/outbox keys.

## Qdrant projection

Qdrant is not part of the PostgreSQL media SSOT path. Media Qdrant reads and writes must remain disabled during and after cutover; PostgreSQL + pgvector is the canonical media search store. Any remaining Qdrant usage must be explicitly outside the media domain.

### Media projection demolition status (September 2026)

REMOVED (cutover demolition):

- `SelectMediaAssetCommitter` (composition root + media subpackage): the
  caller-supplied adapter-pair decision was never invoked by production
  wiring and conflicted with the canonical single decision point. The
  media engine selection lives exclusively in
  `canonical_media_committer.go` (`newCanonicalAssetCommitterCfg` /
  `canonicalCommitterForRoot`), fail-closed on
  `cfg.MediaPostgreSQL.Enabled` + the open `root.MediaPostgres` handle.

REPLACED (structural, gated by `make certify-media-cutover`):

- The SQLite → outbox → Qdrant media projection chain is replaced by
  `pgmedia.PostgresIndexWorker`: claims `asset.index.requested` from the
  PG outbox, embeds the asset search_text, upserts the pgvector, flips
  `index_state=INDEXED` in the same transaction, and completes the event
  with lease fencing + retry/dead-letter semantics. In PG mode the
  composition root registers NO Qdrant media indexing handler
  (`QDRANT_MEDIA_WRITES=0`, `QDRANT_MEDIA_READS=0` — structural gates).

RETAINED (legitimate non-media Qdrant usage — demolition debt owner:
non-media Qdrant retirement, NOT the media cutover):

- `internal/platform/qdrant/indexing/mediamemory` (frame-concept
  projections), `internal/capabilities/maintenance` DR adapter,
  `cmd/admin/internal/audit` + `cmd/admin/reconcile` tooling, and the
  SQLite media committer family (staged-migration adapter until
  non-media SQLite retirement).

## Drive and filesystem

Google Drive and local filesystem are side-effect surfaces. Writes must be durable in SQLite before being emitted through the transactional outbox.

## Configuration

Configuration is owned by the composition root (`internal/app`). Runtime configuration is loaded once at startup and treated as read-only.

## Future storage changes

Any new storage backend must be introduced behind a port, wired only at the composition root, and documented in this file before use.
