# PipelineGen — Architecture

> **Status**: canonical; replaces the scattered map previously in
> `AGENTS.md` (old sections) and the README.
>
> **Authority carve-out**: this doc covers structure and data flow. For
> **rules** (DB driver, FTS5 ban, schema boundaries, AI generation policy,
> admin token, agent instructions) `AGENTS.md` wins. If they disagree, fix the code.

## 1. What it is

PipelineGen is a Go backend that automates media pipelines: scrape
(Artlist, YouTube), enrich with AI (Ollama, NVIDIA, vector search), render
(video/image/voiceover), sync to Drive. Long-running work runs through a
unified job queue; HTTP traffic is served by Gin.

**Three binaries in one monorepo:**

| Binary | Source | Purpose |
|--------|--------|---------|
| `pipelinegen` | `cmd/server/main.go` | HTTP server + background workers. Default `--mode all`. |
| `pipelinegen-worker` | `cmd/worker/main.go` | Standalone worker (connects to broker via HTTP). |
| `admin` | `cmd/admin/main.go` | One-shot CLI: backfills, resets, migrations, benchmarks, API docs. |

## 2. System at a glance

```
┌───────────────────────────────────────────────────────────────────────┐
│                        pipelinegen (Go, Gin)                          │
│                                                                       │
│  ┌────────────┐  ┌────────────────┐  ┌──────────────────────────┐    │
│  │ cmd/server │─▶│ internal/api   │─▶│ internal/app/registry.go │    │
│  │ main.go    │  │ server/routes  │  │ WireRegistry(root)       │    │
│  └────────────┘  └────────────────┘  └────────────┬─────────────┘    │
│                                                   │                   │
│                                                   ▼                   │
│                   ┌─────────────────────────────────────────────┐    │
│                   │  internal/app (composition)                  │    │
│                   │  NewComposition → ComposeRoot (12 bundles)  │    │
│                   │  builds capability bundles, no CoreDeps     │    │
│                   └─────────┬─────────────────────┬─────────────┘    │
│                             │                     │                  │
│                             ▼                     ▼                  │
│       ┌───────────────────────────┐  ┌──────────────────────────┐    │
│       │ internal/application/jobs │  │ internal/application/*    │    │
│       │ broker, runner, outbox    │  │ assets, scripts, images,  │    │
│       │ ActiveKey dedup           │  │ content, voiceover        │    │
│       │ events, delivery          │  │ (use-case orchestration)  │    │
│       └──────────┬────────────────┘  └──────────┬───────────────┘    │
│                  │                              │                    │
│                  ▼                              ▼                    │
│       ┌──────────────────────────────────────────────────────┐      │
│       │ internal/infrastructure (adapters)                    │      │
│       │ database/, drive/, media/processor/, ai/, qdrant/,   │      │
│       │ health/, indexing/, files/, remote/, youtube/        │      │
│       └──────────┬────────────────────────────┬──────────────┘      │
│                  │                            │                      │
│                  ▼                            ▼                      │
│         ┌───────────────────────┐   ┌───────────────────────────────────┐    │
│         │ data/media/           │   │ data/observability/               │    │
│         │ media.db.sqlite       │   │ api_requests.db.sqlite            │    │
│         │ (Primary)             │   │ (Observability)                   │    │
│         └───────────────────────┘   └───────────────────────────────────┘    │
│                                                                       │
│  ┌─ internal/application ────────────────────────────────────────┐    │
│  │ assets/{providers,association,realtime,catalogsync,...}       │    │
│  │ monitor/ images/ voiceover/ content/ ingest/ scripts/         │    │
│  │ system/health/  (PR1 Health boundary, June 2026)              │    │
│  └───────────────────────────────────────────────────────────────┘    │
└───────────────────────────────────────────────────────────────────────┘
              │                  │                │
              ▼                  ▼                ▼
   ┌──────────────────┐ ┌────────────────┐ ┌────────────────────┐
   │ Node scraper     │ │ Google Drive   │ │ External AI        │
   │ :9123 systemd    │ │ OAuth2 + API   │ │ Ollama :11434      │
   │ artlist-scraper  │ │                │ │ NVIDIA NIM         │
   └──────────────────┘ └────────────────┘ │ Qdrant :6333       │
                                           │ CrossEncoder :8091 │
                                           │ FastAPI :8000      │
                                           │ yt-dlp + ffmpeg    │
                                           └────────────────────┘
```

## 3. The two-phases model

| Phase | Package | Job | DB | HTTP |
|-------|---------|-----|----|----|
| Composition | `internal/app` | Build **Capability Bundles** once via `ComposeRoot` + `Build*Bundle()` (`Drive`, `Repo`, `Search`, `Process`, `AI`, `Domain`, `Jobs`, `Outbox`, `Sync`, `Maintenance`, `Utility`). Wire DB pools, run migrations, instantiate concrete adapters. | owns pools + migrations at boot | no |
| Delivery | `internal/api/<feature>/` | Register routes on the shared Gin engine via thin-transport handlers that delegate to use cases in `internal/application/<feature>/`. Background tasks start via `lifecycle.go::startBackgroundJobs(ctx, cfg, root, log)`. | uses injected handles via use cases | yes |

Handlers live in `internal/api/<feature>/<handler>.go` and **must not**
contain business logic (Pattern 8 in AGENTS.md). Business logic lives
in `internal/application/<feature>/` (use cases + orchestration) and
`internal/infrastructure/<X>/` (concrete adapters: DB, Drive, exec).
Each `internal/api/<feature>/` exposes at most 1 `Handler` + 1
`RegisterRoutes`. The legacy `CoreDeps` mega-struct was removed in
PR4d-final (June 2026) — bundles are the only valid wiring primitive.

## 4. Module ownership

`internal/app/registry.go::WireRegistry` wires capability modules. Each owns
its routes, its background tasks, and the ComposeRoot bundle slice it needs.
Reading from other modules' DB tables is OK; importing another module's
`internal/api/` package is forbidden — go through the service layer.

| Module | Registry file | Mounts | Verdict |
|--------|-------------|--------|---------|
| `ScriptFlow` | `module_scripts.go` → `WireScriptFlow` | `/api/script/*` | active (post PR4d-final) |
| `Assets` | `module_assets.go` → `WireAssets` | `/api/media/*`, `/api/assets/*` | active |
| `Artlist` | `module_artlist.go` → `WireArtlist` | `/api/artlist/*` | active |
| `Images` | `module_fullimages.go` → `WireImages` | `/api/images/*`, `/api/fullimages/*` | active |
| `Jobs` | `module_jobs.go` → `WireJobs` | `/api/jobs/*`, `/api/internal/*` | active |
| `YouTube` | `module_youtube.go` → `WireYouTubeClip` | `/api/clips/*` | active |
| `Channels` | `module_ingest.go` | `/api/channels/*` | active |
| `Stock` | `module_stock.go` → `WireStockPipeline` | stock pipeline routes | active |
| `Content` | books + lessons merged → `internal/api/content/` | `/api/books/*`, `/api/lessons/*` | active |

System routes (always present): `/health`, `/ready`, `/metrics`, `/assets/*`, `/media/google-accounting/*`.

**PR1 Health boundary (June 2026)**: the health handler is thin transport only;
infrastructure checks (DB, Drive, Qdrant, Jobs) live in
`internal/infrastructure/health/` and are orchestrated by
`internal/application/system/health/Service`. The handler delegates —
zero `database/sql`, Google Drive SDK, or Qdrant HTTP in `internal/api/`.

## 5. Data flow — three canonical journeys

### 5a. Artlist search term → Drive asset
```
HTTP POST /api/artlist/run {term}
  └─▶ handler (registered by WireArtlist module)
        └─▶ internal/application/assets/providers/artlist.runOrchestrator
              ├─▶ internal/application/assets/providers/artlist.search
              │     └─▶ Node scraper :9123 (L1 in-memory + L2 SQLite cache)
              └─▶ enqueue jobs.Service {type: media.artlist}
                    └─▶ workers.Worker → artlist.jobHandler
                          ├─▶ download via provider
                          ├─▶ internal/infrastructure/indexing/clipindexer.IndexClip
                          │     └─▶ scripts/index_clips.py
                          └─▶ internal/infrastructure/drive.Uploader
                                └─▶ Google Drive (cfg.Drive.ArtlistFolder())
```

### 5b. Script generation (unified pipeline)

> **Giugno 2026**: `/api/script/generate-from-clips` è il canonical async endpoint.
> `/api/script/generate-with-images` rimane come endpoint **dedicato e
> separato**, NON è un alias di `/generate-from-clips`.

```
HTTP POST /api/script/generate-from-clips   ─ canonical, preset dal body
HTTP POST /api/script/generate-with-images  ─ preset forzato: scene images ON
  └─▶ ScriptFlowHandler → enqueue type="script.generate_from_clips"
        └─▶ HandleClipScriptGenerateJob (handler_jobs.go)
              Paths (branch on payload):
                1. clip_ids present   → ClipSourceBuilder.BuildClipContext → engine.WriteScript
                2. num_clips > 0      → mediaCurator.Curate (auto-search) → engine.WriteScript
                3. otherwise          → text-only engine.WriteScript
              Optional post-processing (parallel via concurrent.Group):
                4. entity extraction + insights    (if extract_entities)
                5. YouTube metadata per language  (if generate_metadata)
                6. Google Doc creation (always)
```

### 5c. Channel monitor (background)
```
Ticker (cfg.Jobs.CatalogSyncInterval, default 6h)
  └─▶ internal/application/monitor/channel_monitor.StartScheduled
        └─▶ per enabled channel:
              ├─▶ YouTube search (yt-dlp)
              ├─▶ transcript extract (VTT)
              ├─▶ semantic match against watch terms
              └─▶ enqueue media.youtube_clip on match
```

## 6. Persistence

**Pattern (codex/db-set-and-paths, June 2026)**: every sqlite database is
opened through `internal/infrastructure/database.DatabaseSet` (`OpenSet`,
`Migrate`, `Health`, `Close`). `internal/app/composition.go` calls
`OpenSet(...)` exactly once at boot. No `sql.Open` lives outside
`internal/infrastructure/database/**`.

| Database | Path | Holds | Migrations |
|----------|------|-------|------------|
| **Primary** | `<DataDir>/media/media.db.sqlite` (compat default) | **Unico database** — scripts, jobs, media_assets, clip_folders, voiceovers, youtube_cache, gemma_memory, search_queries, sketchfab, pipeline_runs, worker_nodes, etc. | `migrations/sqlite/*.sql` |
| **Observability** | `<DataDir>/observability/api_requests.db.sqlite` | API request log table + indexes (single purpose: HTTP traffic telemetry). Distinct from Primary so log retention doesn't churn the schema-versioned Primary DB. | `migrations/sqlite/*.sql` |

**Configurable via `cfg.Storage`** (defaults preserve legacy single-file layout):

| Field | YAML | Env | Default |
|-------|------|-----|---------|
| `data_dir` | `storage.data_dir` | `VELOX_DATA_DIR` | `./data` |
| `primary_db_path` | `storage.primary_db_path` | `VELOX_PRIMARY_DB_PATH` | `<DataDir>/media/media.db.sqlite` |
| `observability_db_path` | `storage.observability_db_path` | `VELOX_OBSERVABILITY_DB_PATH` | `<DataDir>/observability/api_requests.db.sqlite` |
| `workspace_dir` | `storage.workspace_dir` | `VELOX_WORKSPACE_DIR` | `<DataDir>/workspace` |
| `cache_dir` | `storage.cache_dir` | `VELOX_CACHE_DIR` | `<DataDir>/cache` |
| `export_dir` | `storage.export_dir` | `VELOX_EXPORT_DIR` | `<DataDir>/export` |

Pragmas: WAL, `busy_timeout=5000`, `synchronous=NORMAL`, 5-10 open / 2-5
idle per pool. `DatabaseSet.Migrate(log)` runs the canonical migration
ledger against BOTH Primary + Observability (errors on either roll
forward as-is — no distributed transaction across DBs).

**Path migration**: the path-migration tool (`cmd/admin/path_migrate.go`,
future PR) performs backup + SHA256 checksum + PRAGMA integrity_check +
rollback when operators opt in to relocate the Primary DB from the
legacy `<DataDir>/media.db.sqlite` path to the canonical
`<DataDir>/media/media.db.sqlite`. Until that runs, the PrimaryDBPath
default matches today's on-disk file so existing deployments keep
working without a migration. Default resolution in
`internal/infrastructure/database/storage.ResolveStorageConfig`.

**On disk**: `cfg.Storage.DataDir` (default `./data`); subdirs `voiceovers/`,
`images/`, `youtube/`, `artlist/`, `assets/`, `downloads/`, `animations/`,
`backups/`. **On Drive**: `cfg.Drive.MediaRootFolder` is the single root;
per-domain folders are fallbacks. The `DriveConfig` resolvers
(`ArtlistFolder()`, `ClipsFolder()`) implement this priority.

## 7. Concurrency and context

- **Server lifecycle**: `signal.NotifyContext(ctx, SIGINT, SIGTERM)` derived
  once in `internal/api/server.go`. All goroutines and module lifecycles
  derive from it.
- **Request ctx**: propagates from Gin handlers through services to the DB.
  `requestID` middleware attaches a correlation ID.
- **Save ctx** (intentional exception): post-generation side-effects use
  `context.Background()` + 30s timeout so the save survives the HTTP client
  disconnecting. Pattern in `withPostWriteContext`,
  `gemmamemory.SaveAfterGeneration`, `scriptcore.Engine.WriteScript`.
- **concurrent.Group** (`pkg/concurrent`): replaces `WaitGroup` + N `Mutex`.
  Cancels siblings on first error, recovers panics, one goroutine per slot.
- **Job claims**: lease (`cfg.Jobs.LeaseTTLSeconds = 300`); dead workers
  auto-reclaimed by `RequeueExpiredLeases`. `ActiveKey` dedupes enqueues.

### Extract facade contract — `monitor → orchestrator → ytextraction`

Background monitors invoke the synchronous
`*youtube.Service.Extract(ctx, req)` facade (entry point at
`internal/application/youtube/service_orchestrator.go::Extract`).
This is the **canonical** clip-extraction entry for the monitor path
(POST `/api/script/*` routes run through the async job pipeline; they
ignore this facade). The contract:

1. **Thin facade, no orchestration**: the orchestrator method only
   nil-guards `s.extraction` and forwards to `s.extraction.Extract`.
   New capability wiring must NOT be added to the facade — every monitor
   change should be a no-op on the orchestrator surface.
2. **Lazy enricher / Indexer / Drive reconstruction**: the orchestrator
   wires the `ytextraction.Service` with the root `*youtube.Service` as its
   `ExtractionCallbacks` adapter (`NewService` →
   `ytextraction.NewService(..., svc)`). The capability delegates every
   external operation back through the adapter; the canonical callback
   signature lives at `internal/application/youtube/extraction/` (the
   `ExtractionCallbacks` interface) and is implemented wholesale on
   `*youtube.Service`, so capability-side internals never reach into
   orchestrator state directly. **To add a new capability-side external op,
   extend the `ExtractionCallbacks` interface in one place and implement
   the forwarder on `*Service` — never inject state into ytextraction.**
   Per-invocation lazy `RebuildDeps` reconstruction for the
   `rebuild_search_text` job type lives on `s.HandleRebuildSearchTextJob`
   (closes over `s.clips.DB()`, `s.indexer`, `s.metadata.EnrichClip`).
   **Callers below the orchestrator must NOT try to reconstruct any of
   these deps themselves.**
3. **Capability-not-wired is fatal at the facade level**: `s.extraction
   == nil` returns an explicit `"youtube: extraction capability not
   wired (...)"` error. The monitor path logs at `Error` and skips the
   clip; a `defer recover()` at the top of `downloadClip` ensures a
   panic from one mis-wired port doesn't tear down the ticker
   goroutine. The composition root must wire `Cfg, Log, VideoPipeline,
   Clips, Monitors, AssetDestResolver, FolderMemory, SegmentsSvc` in
   `ServiceDeps` for `NewService` to wire the capability.
4. **Response shape & counter discipline**: `*youtubetypes.ExtractResponse`
   with `OK`, `Items []ExtractItem` (each `ExtractItem.Status` is one
   of `processed`/`skipped`/`failed`), `Stats` (`Requested` / `Processed` /
   `Skipped` / `Failed`), `Folder`, `Error`. The monitor path treats
   `err != nil` as hard failure (Error log), `resp == nil && err == nil`
   as a defensive hard failure (treats as misconfigured; Error log),
   `resp.OK == false` as business-level failure (Warn log + skip),
   and on success logs `resp.Stats.Processed/Skipped/Failed` as
   separate `zap.Int` fields — never `len(resp.Items)`, which would
   over-report by including failed items.

Replacing the previous `channel-monitor` "WARN-skip placeholder":
that placeholder documented `*youtube.Service.Extract` was removed
during the ytextraction extraction; the facade above restores it
without leaking capability internal state.

`context.Background()` in non-test code: currently **~9 sites** (refactored from ~20). The remaining sites are either intentional post-write save contexts or top-level composition roots where no parent context exists. Lint gate: `bash scripts/ci-architectural-checks.sh`.

## 8. External services

| Service | Port | Purpose | Started by |
|---------|------|---------|------------|
| Ollama | 11434 | Text/metadata (Gemma, Llama) | external |
| NVIDIA NIM | 8000 | Image generation (FLUX, SDXL) | external |
| Qdrant | 6333/6334 | Vector store (hybrid search) | external |
| CrossEncoder | 8091 | Post-Qdrant reranking | external |
| FastAPI Google Vids | 8000 | Playwright automation | `google_accounting` watchdog |
| Node scraper | 9123 | Persistent headless browser | `artlist-scraper.service` |
| `yt-dlp` | — | YouTube search/download | CLI |
| `ffmpeg` | — | Video/audio encoding | CLI |
| Google Drive | — | Asset storage | OAuth2 |

The Go server starts only the two sidecars it owns (FastAPI Vids, Node
scraper). Everything else is external.

## 9. Configuration

`config.yaml` at the project root is loaded once by `internal/config.Get()`.
Order: struct defaults → YAML → env vars. The 19 sub-structs of `Config`
live in `internal/config/*.go` (one per file; `types.go` is the index).

Security-sensitive: `VELOX_ADMIN_TOKEN` must be supplied at runtime;
production token MUST NOT be checked in.

## 10. Day-1 commands

The runtime opens the `DatabaseSet` once via `storage.OpenSet(cfg.Storage, log)`
in `internal/app/bootstrap.go::initDatabases`; no `sql.Open` lives outside
`internal/infrastructure/database/**`. Override the canonical DB paths via
the `storage.primary_db_path` and `storage.observability_db_path` config
fields (defaults preserve legacy single-file layout; see §6).

**Script-flow use cases (June 2026)**: the three HTTP endpoints that used
to embed orchestration in `ScriptFlowHandler` now delegate to typed use
cases in `internal/application/scripts/`:
- `scripts.GenerateBatchUseCase` — backs `POST /api/script/generate-batch`.
- `scripts.SectionRegenerator` — backs `POST /api/script/:id/sections/:section_id/regenerate`.
- `scripts.CacheEvictionUseCase` — backs `POST /api/script/cache/evict`
  (LLM circuit-breaker reset + memory-cache eviction).

The handler is a thin transport; the registry wires each use case via the
`ScriptFlowDeps` literal in `internal/app/registry.go`.


```bash
# Build
go build -o pipelinegen ./cmd/server/
go build -o pipelinegen-worker ./cmd/worker/
go build -o admin ./cmd/admin/

# Run
./pipelinegen --mode all         # HTTP + workers
./pipelinegen --mode http        # HTTP only
./pipelinegen --mode workers     # workers only

# Admin CLI
./admin seed-channels
./admin db migrations  # codex/db-doctor-restore (W2, June 2026) — was ./admin migrate-status
./admin benchmark
./admin gen-api-docs
./admin path-migrate   # codex/db-path-migration-tool (followup)

# Tests
go test ./...                                    # full suite
go test ./internal/jobs/...                      # job lifecycle
go test -run TestDriveConfig ./internal/config/  # single test

# Lint
bash scripts/ci-architectural-checks.sh

# Docs
./admin gen-api-docs
```

OAuth/Drive token: if Drive auth fails, run `python3 scripts/generate_drive_token.py`
and follow the OAuth flow. CI: `.github/workflows/`.

## 11. What lives where

| You want to… | Go look in |
|--------------|------------|
| Add an HTTP endpoint | handler in `internal/api/<feature>/`, delegate to use case in `internal/application/<feature>/` |
| Add a background job | `internal/application/jobs/` (register handler) + job handler in `internal/application/<feature>/` |
| Add a DB table | `migrations/sqlite/0xx_*.sql` + repository in `internal/infrastructure/database/sqlite/` |
| Add a CLI admin command | `cmd/admin/<command>.go` (shim) |
| Change a Drive folder | `internal/infrastructure/config/drive.go` (struct + resolver methods) |
| Tune concurrency | `cfg.Concurrency.*`, `cfg.Jobs.MaxParallelPerProject`, `cfg.Jobs.LeaseTTLSeconds` |
| Debug a stuck job | `GET /api/jobs/:id` + `journalctl -u pipelinegen -f` |
| Add a LLM prompt version | bump in `internal/infrastructure/ai/ollama/prompts/`, record in `cfg.Scripts.PromptVersion` |

## 12. Pointers to deeper docs

All detailed documentation previously under `docs/` has been consolidated and removed.
For all critical rules and operational guidelines, please refer to:
- `AGENTS.md`: Critical rules (DB driver, FTS5 ban, schema boundaries, AI gen policy, agent instructions).
- `PROJECT_GUIDE.md`: Quick start guide.


## 12b. Observability DB retention policy (June 2026)

Wahl: **disposable + cron retention** (chosen over inclusion-in-backup for
this codebase; rationale below). The observability DB
(`data/observability/api_requests.db.sqlite`) holds only one purpose:
HTTP request telemetry replayability for post-incident forensics.

**Policy:**
- The observability DB is NOT included in the canonical primary backup
  manifest (`admin db backup` covers the primary file only). Including
  it would bloat RPO snapshots with telemetry that has no domain value
  beyond ~7 days of history.
- Retention is driven by `admin db rotate` (cron-friendly):
  - Cuts off rows with `ts < now - cfg.Storage.ObservabilityMaxAgeDays`
    (default 7 days).
  - Offloads the cutoff rows to a self-contained SQLite archive at
    `<DataDir>/backups/observability-YYYYMMDD.db.sqlite` via ATTACH +
    `INSERT INTO offload.api_requests SELECT * FROM main.api_requests`.
  - Purges the offloaded rows from the live DB.
  - Runs `VACUUM main.api_requests` to reclaim page slots.
  - Emits a JSON line with `cutoff / offloaded_to / offloaded_rows /
    purged_rows / bytes_reclaimed / duration_ms`.
- Operators schedule `admin db rotate` on a daily cron at 02:00 server
  time. Suggested crontab:
  ```
  0 2 * * * cd /opt/pipelinegen && go run ./cmd/admin db rotate
  ```
- `admin db status` reports `observability` size + the most recent
  backup timestamp so operators can spot drift.

**Why not include-in-backup (the other option)?**
- Backup manifest becomes large (week-old telemetry adds ~200 MB for
  typical weeks; rotation keeps it bounded).
- Restores would rehydrate rows that get immediately purged, wasting
  disk + bandwidth.
- The forensics need is bounded by incident review windows (~7 days);
  older rows are not actionable.
- Disposable + cron also keeps the `data/observability/...` directory
  out of the registered list (Check 16 still passes: only
  `data/media/media.db.sqlite` is the registered primary).

**Implementation pointers:**
- `internal/infrastructure/database/rotation.go::RotateObservability` —
  the ATTACH + INSERT + DELETE + VACUUM sequence.
- `cmd/admin/db_rotate.go` — the admin CLI subcommand.
- `internal/infrastructure/config/types.go::StorageConfig` —
  ObservabilityMaxAgeDays (default 7), ObservabilityMaxSizeMB
  (default 1024).

## 13. Conventions and out-of-scope

- **1 struct per file** in `internal/config/` (`types.go` is the index).
- **`pkg/`** is for leaf utilities only: `retry`, `hashutil`, `concurrent`,
  `sliceutil`, `textutil`, `fileutil`, `urlutil`, `timeutil`.
- **No** `context.Background()` outside the allowlist (lint-gated).
- **No** `any` casts except for genuinely polymorphic values.
- **No** generic `interface{}` returns — small interface near the consumer.
- **Async by default** for LLM calls, Drive uploads, scrapers; sync only
  for health checks and diagnostics.
- **Italian** in code comments is fine (the codebase uses it widely);
  commit messages and this doc stay in English.
- **Out of scope**: Android/iOS clients, Artlist Chrome extension (separate
  repos); operational details are documented in `AGENTS.md`.

---

## 14. Port abstraction layer (June 2026, PR1.7 cascade)

Per PR1.7 (June 2026) `internal/application/youtube/` adotta
**port abstraction** come pattern canonico per le dipendenze esterne
di `Service`. In sintesi:

- **Port interface** dichiarato in `internal/application/youtube/ports.go`
  (application layer; **mai** nell'infrastruttura).
- **Concrete adapter** in `internal/infrastructure/youtube/` (o
  `internal/infrastructure/<bounded-context>/`).
- **Compile-time assertion** nel file adapter:
  `var _ youtubedto.VideoMetadataFetcherPort = (*MetadataFetcherAdapter)(nil)`.
- **Composition-side wiring** in `internal/app/youtube_adapters.go`
  (`newClipStoreAdapter`, `newDriveFolderMgrAdapter`, ecc) —
  invocato da `internal/app/composition.go::BuildDomainBundle`.
- **Constructor injection** via `NewService(ServiceDeps{...})`.

### Ports esistenti (12 strutturali — June 2026)

| Port | Adapter canonical | Model |
|------|-------------------|-------|
| `ClipStorePort` | `internal/app/youtube_adapters.go::clipStoreAdapter` | wraps `*assets.ClipsRepository` |
| `MonitorsStorePort` | `internal/app/youtube_adapters.go::monitorsStoreAdapter` | wraps `*assets.MonitorsRepository` |
| `VideoMetadataFetcherPort` | `internal/infrastructure/youtube/metadata.go::MetadataFetcherAdapter` | shells yt-dlp `--dump-json` |
| `DriveFolderManagerPort` | `internal/app/youtube_adapters.go::driveFolderMgrAdapter` | wraps `*drive.Uploader` |
| `FolderMemoryPort` | passes-through `*foldermemory.Service` directly | canonical impl lives at `internal/media/foldermemory/` |
| `OllamaClientPort` | passes-through `*client.Client` directly | canonical impl lives at `internal/ml/ollama/client/` |
| `SearchRunnerPort` | `internal/app/youtube_adapters.go::searchRunnerStub` (stub; real implementation deferred) | returns empty + warn-log |
| `ClipIndexerPort` | `internal/app/youtube_adapters.go::clipIndexerAdapter` | wraps `*clipindexer.Service` |
| `WhisperTranscriberPort` | reserved; nil-by-default; segment.go nil-guards before call | — |
| `ClipFilesPort` | reserved; nil-by-default; segment_cache.go nil-guards before call | — |
| `HashServicePort` | reserved; nil-by-default; service.go::md5String/md5File fallback chain | leaf via `pkg/hashutil` |
| `SubtitleFetcherPort` | reserved; nil-by-default; subtitles.go nil-guards before call | — |

Empty-marker (opaque injection tokens, no method signature): 
`TempFileManagerPort`, `YouTubeCacheStorePort`.

### Canonical DTO

Un solo DTO per i metadata video: `*youtubedto.DownloaderMetadata`
(con 14 fields + `CachedAt`). Back-compat per i nomi legacy:
`type VideoMetadata = DownloaderMetadata`,
`type YouTubeMetadataPort = DownloaderMetadata`.

### Constructor collapse

La precedente setter cascade (`Service.SetSearchRunner(...)`,
`SetClipFiles(...)`, ...) è collassata in
`NewService(ServiceDeps{...})` con 21 campi (13 wired esplicitamente,
8 nil-tolerant per port opzionali).

### Stato post-cascade (June 2026 — vedi anche `AGENTS.md`)

- Settore verde sul cascade package scope (5 packages + cmd/server + cmd/worker).
- `go test ./...` ha 7 packages falliti FUORI dal cascade scope — investigazione separata.
- `internal/application/youtube/` è ancora un mega-package di 43 file (target 5-8) — split pianificato.
- 3 latent-risk fissano l'agenda post-cascade (Thumbnails:nil, searchRunnerStub silent-empty, typed-nil panic).

---

*If you change the architecture, update this file in the same commit. The
diagram is the contract.*
