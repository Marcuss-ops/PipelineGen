# PipelineGen — Architecture

> **Status**: canonical; replaces the scattered map previously in
> `AGENTS.md` (old sections), `docs/architecture/MODULE_MAP.md`,
> `docs/architecture/MODULE_OWNERSHIP.md`, and the README.
>
> **Authority carve-out**: this doc covers structure and data flow. For
> **rules** (DB driver, FTS5 ban, schema boundaries, AI generation policy,
> admin token, agent instructions) `AGENTS.md` wins. If they disagree, fix the code.
>
> Both MODULE_MAP.md and MODULE_OWNERSHIP.md have been **deleted**.
> All their content is now in §4 (Module ownership) or AGENTS.md.

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
│  │ cmd/server │─▶│ internal/api   │─▶│ internal/module          │    │
│  │ main.go    │  │ server/routes  │  │ registry.RegisterAll...  │    │
│  └────────────┘  └────────────────┘  └────────────┬─────────────┘    │
│                                                   │                   │
│                                                   ▼                   │
│                   ┌─────────────────────────────────────────────┐    │
│                   │  internal/app (composition)                  │    │
│                   │  WireRegistry, WireServices                 │    │
│                   │  builds CoreDeps once, hands to modules     │    │
│                   └─────────┬─────────────────────┬─────────────┘    │
│                             │                     │                  │
│                             ▼                     ▼                  │
│       ┌───────────────────────────┐  ┌──────────────────────────┐    │
│       │ internal/jobs (queue)     │  │ internal/service/*       │    │
│       │ lease, events, corr-id    │  │ artlist, images, voice-  │    │
│       │ ActiveKey dedup           │  │ over, scriptcore, ...    │    │
│       └──────────┬────────────────┘  └──────────┬───────────────┘    │
│                  │                              │                    │
│                  ▼                              ▼                    │
│       ┌──────────────────────────────────────────────────────┐      │
│       │ Removed: * (scripts, jobs, clips, ...)    │      │
│       └──────────┬────────────────────────────┬──────────────┘      │
│                  │                            │                      │
│                  ▼                            ▼                      │
│         ┌────────────────┐          ┌──────────────────┐             │
│         │ data/media.db.sqlite  │          │ data/media.db.sqlite    │             │
│         │ WAL · 5-10     │          │ WAL · 5-10       │             │
│         └────────────────┘          └──────────────────┘             │
│                                                                       │
│  ┌─ internal/media ─────────────────────────────────────────────┐    │
│  │ monitor/ images/ voiceover/ stockpipeline/ semantic/         │    │
│  │ association/ clipindexer/ catalogsync/ books/ lessons/ ...   │    │
│  └───────────────────────────────────────────────────────────────┘    │
│  ┌─ internal/sources ───────────────────────────────────────────┐    │
│  │ artlist/  youtube/   (scraper client, search, extraction)   │    │
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
| Composition | `internal/app` | Build `CoreDeps` once. Wire DB pools, run migrations, instantiate services. | owns pools + migrations at boot | no |
| Delivery | `internal/module` | Register routes on the shared Gin engine, start/stop background tasks via `StartAll`/`StopAll`. | uses injected handles | yes |

Handlers live in `internal/api/handlers/<domain>/`. Business logic lives in
`internal/service` or `internal/media/<domain>`. A module is a thin adapter
by default; a handful (`google_accounting`) own their sidecar lifecycle and
run a watchdog goroutine.

## 4. Module ownership

`internal/app/registry.go::WireRegistry` wires these **9 modules**. Each owns
its routes, its background tasks, and the slice of `CoreDeps` it needs.
Reading from other modules' DB tables is OK; importing another module's
`internal/api/handlers` package is forbidden — go through the service layer.

| Module | File | Mounts | Touches |
|--------|------|--------|---------|
| `ScriptFlow` | `internal/module/scriptflow.go` | `/api/script/*` | `scriptcore`, `gemmamemory`, `translations`, `clipindexer`, `images`, `voiceover` |
| `Realtime` | `internal/module/realtime.go` | `/api/realtime/*` | `media/realtime` (vector + reranker) |
| `Books` | `internal/module/books.go` | `/api/books/*` | `media/books`, Python `book_summarizer.py` |
| `Lessons` | `internal/module/lessons.go` | `/api/lessons/*` | `media/lessons` |
| `Comics` | `internal/module/comics.go` | `/api/comics/*` | `media/comics` |
| `Channels` | `internal/module/channels.go` | `/api/channels/*` | `media/monitor`, `repository/channels` |
| `SearchQueries` | `internal/module/search_queries.go` | `/api/search-queries/*` | `repository/searchqueries` |
| `ScriptHistory` | `internal/module/scripthistory.go` | `/api/script-history/*` | `repository/scripts` |
| `Utility` | `internal/module/core_modules.go` | health, jobs, system routes | `internal/jobs`, `logger` |

[^handler-only]: Handler folders not in the registry (routes wired manually or via the system module): `sources/{artlist,youtube,stock,voiceover,images,clip_crud,drive}`, `mediaingest`, `fullimages`, `scraper`, `google_accounting`. Promote into the registry if you add significant new behaviour.

System routes (always present): `/health`, `/metrics`, `/assets/*`, `/media/google-accounting/*`.

## 5. Data flow — three canonical journeys

### 5a. Artlist search term → Drive asset
```
HTTP POST /api/artlist/run {term}
  └─▶ handler (registered by Router, not via module registry)
        └─▶ internal/service/artlist.runOrchestrator
              ├─▶ internal/sources/artlist.search
              │     └─▶ Node scraper :9123 (L1 in-memory + L2 SQLite cache)
              └─▶ enqueue jobs.Service {type: media.artlist}
                    └─▶ workers.Worker → artlist.jobHandler
                          ├─▶ download via provider
                          ├─▶ internal/media/clipindexer.IndexClip
                          │     └─▶ scripts/index_clips.py
                          └─▶ internal/upload/drive.Uploader
                                └─▶ Google Drive (cfg.Drive.ArtlistFolder())
```

### 5b. Script generation (unified pipeline)

> **Gennaio 2026**: il flow precedente `GenerateFromSource` (con 11 fasi
> separate, agent Python in-loop, payload dedicato) è stato **rimosso**;
> `/api/script/generate-from-clips` è ora il canonical async endpoint.
> `/api/script/generate-with-images` rimane come endpoint **dedicato e
> separato**, NON è un alias di `/generate-from-clips` — vedi la nota
> immediatamente sopra al flow ASCII sotto per il confronto tra i due
> preset di payload (vedi `docs/CHANGELOG_2026-06-03.md` sezione 0 per la
> nota BREAKING sulla rimozione del flow legacy). Per i pattern di
> parallelismo della fase immagini vedi `docs/PARALLEL_IMAGE_GENERATION.md`.

```
HTTP POST /api/script/generate-from-clips   ─ canonical, preset dal body
                                          (handler: GenerateFromClips in handler_clip_source.go)
HTTP POST /api/script/generate-with-images  ─ preset forzato: scene images ON,
                                          entities/metadata OFF
                                          (handler: GenerateWithImages in handler_generate_with_images.go)
                                          Differenza: solo preset del payload.
                                          Entrambi → enqueue type="script.generate_from_clips".
  └─▶ ScriptFlow handler.GenerateFromClips  (per /generate-from-clips;
      oppure ScriptFlow handler.GenerateWithImages per /generate-with-images)
        └─▶ enqueue jobs.Service {type: script.generate_from_clips}
              └─▶ workers.Worker → HandleClipScriptGenerateJob (job_handler_clip_source.go)
                    Paths (branch on payload):
                      1. clip_ids present   → ClipSourceBuilder.BuildClipContext → engine.WriteScript
                      2. num_clips > 0      → mediaCurator.Curate (auto-search) → engine.WriteScript
                      3. otherwise          → text-only engine.WriteScript (con plan)
                    Optional post-processing (parallel via concurrent.Group):
                      4. entity extraction + insights    (if extract_entities)
                      5. YouTube metadata per language  (if generate_metadata)
                      6. Google Doc creation (always)
                    Phases 6 use a save ctx independent of the request ctx — see §7.
```

### 5c. Channel monitor (background)
```
Ticker (cfg.Jobs.CatalogSyncInterval, default 6h)
  └─▶ internal/media/monitor/channel_monitor.StartScheduled
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
| Add an HTTP endpoint | handler in `internal/api/handlers/<domain>/`, register in `routes.go` or via `internal/module/<domain>.go` |
| Add a background job | `internal/jobs` (register handler) + `internal/api/handlers/<domain>/job_handler.go` (entry point) |
| Add a DB table | `migrations/sqlite/0xx_*.sql` + `Removed: <domain>/` |
| Add a CLI admin command | `cmd/admin/<command>.go` (shim) + `internal/admin/<subpkg>/` (body) |
| Change a Drive folder | `internal/config/drive.go` (struct + resolver methods) |
| Tune concurrency | `cfg.Scripts.*`, `cfg.Jobs.MaxParallelPerProject`, `cfg.Jobs.LeaseTTLSeconds` |
| Debug a stuck job | `GET /api/jobs/:id` + `journalctl -u pipelinegen -f` |
| Add a LLM prompt version | bump in `internal/ml/ollama/prompts/`, record in `cfg.Scripts.PromptVersion` |

## 12. Pointers to deeper docs

| Doc | Covers |
|-----|--------|
| `AGENTS.md` | **Critical rules** (DB driver, FTS5 ban, schema boundaries, AI gen policy, agent instructions). Wins on rule conflicts. |
| `docs/architecture/job_lifecycle.md` | Job states, lease, retry, dead-letter |
| `docs/architecture/job_lifecycle.md` | Job states, lease, retry, dead-letter |
| `docs/PARALLELIZATION.md` | Parallel execution tuning, warm pool |
| `docs/INTELLIGENCE_ROADMAP.md` | Hybrid search, vector store, reranker roadmap |
| `docs/AI_GENERATION.md` | NVIDIA + Flux, semantic tagger |
| `docs/SCRIPT_PIPELINE.md` | Script generation phases (supplements §5b) |
| `docs/youtube_clip_service.md` | YouTube extraction + intelligence |
| `docs/CHANGELOG_2026-06-03.md` | Most recent architectural changes |
| `docs/ops-audit.md` | Operational concerns (systemd, log rotation, GPU) |


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
  repos); operational details in `docs/ops-audit.md`.

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

### Stato post-cascade (June 2026 — vedi anche `docs/POST_CASCADE_OPERATIONAL_READINESS.md`)

- Settore verde sul cascade package scope (5 packages + cmd/server + cmd/worker).
- `go test ./...` ha 7 packages falliti FUORI dal cascade scope — investigazione separata.
- `internal/application/youtube/` è ancora un mega-package di 43 file (target 5-8) — split pianificato.
- 3 latent-risk fissano l'agenda post-cascade (Thumbnails:nil, searchRunnerStub silent-empty, typed-nil panic).

---

*If you change the architecture, update this file in the same commit. The
diagram is the contract.*
