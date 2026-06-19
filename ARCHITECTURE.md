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

| Database | Path | Holds | Migrations |
|----------|------|-------|------------|
| `media.db.sqlite` | `data/media/` | **Unico database** — everything: scripts, jobs, media_assets, clip_folders, voiceovers, youtube_cache, gemma_memory, search_queries, sketchfab, pipeline_runs, etc. | `migrations/sqlite/` |

Both: WAL, `busy_timeout=5000`, `synchronous=NORMAL`, 5-10 open / 2-5 idle per
pool. Migrations run centrally at boot (`internal/app/migrations.go::runAllMigrations`);
no per-DB ad-hoc migration; see AGENTS.md "schema boundaries".

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
./admin migrate-status
./admin benchmark
./admin gen-api-docs

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

*If you change the architecture, update this file in the same commit. The
diagram is the contract.*
