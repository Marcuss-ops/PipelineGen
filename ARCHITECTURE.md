# PipelineGen — Architecture

> **Status**: canonical; replaces the scattered map previously in
> `AGENTS.md` (old sections) and the README.
>
> **Authority carve-out**: this doc covers structure and data flow.
>
> - For **agent-facing rules** (AI generation policy, admin token, agent
>   instructions) `AGENTS.md` wins.
> - For **data and configuration ownership** (DB driver lock, FTS5 ban,
>   schema boundaries, table capability ownership, Qdrant projection
>   sequence, Drive authority, configuration boot pipeline,
>   EXPAND/BACKFILL/CUTOVER/CONTRACT) the canonical source is
>   [`docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md`](docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md).
> - For the **target-tree axis** (Phase 0 governance, package-size caps,
>   per-capability directory rules) see `architecture/policy.yaml` and
>   §11.5 below.
>
> If sources disagree, fix the code; the loader will tell you which
> rule was violated.

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
`RegisterRoutes`. The old `CoreDeps` mega-struct was removed in
PR4d-final (June 2026) — bundles are the only valid wiring primitive.

## 4. Module ownership

`internal/app/registry.go::WireRegistry` wires capability modules. Each owns
its routes, its background tasks, and the ComposeRoot bundle slice it needs.
Reading from other modules' DB tables is OK; importing another module's
`internal/api/` package is forbidden — go through the service layer.

| Module | Registry file | Mounts | Verdict |
|--------|-------------|--------|---------|
| `ScriptFlow` | `module_scripts.go` → `WireScriptFlow` | `/api/script/*` | active (post PR4d-final) |
| `Assets` | `module_media.go` → `WireAssets` | `/api/media/*`, `/api/assets/*` | active |
| `Artlist` | `module_sources.go` → `WireArtlist` | `/api/artlist/*` | active |
| `Images` | `composition.go` (consolidated; `WireImages`) | `/api/images/*`, `/api/fullimages/*` | active |
| `Jobs` | `composition.go::BuildJobsBundle` | `/api/jobs/*`, `/api/internal/*` | active |
| `YouTube` | `composition.go::BuildDomainBundle` | `/api/clips/*` | active |
| `Channels` | `composition.go` (consolidated; `WireChannels`) | `/api/channels/*` | active |
| `Stock` | `composition.go` (consolidated; `WireStockPipeline`) | stock pipeline routes | active |
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

> **Authority**: Database ownership, migrations discipline (one ledger
> + one runner; capability-owned tables; no `sql.Open` outside
> `internal/infrastructure/database/**`; canonical PRAGMAs
> `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`), the
> Qdrant projection carve-out, and future storage-engine migration
> (EXPAND/BACKFILL/CUTOVER/CONTRACT) live canonically in
> [`docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md`](docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md).
>
> **Package-level enforcement** (which `internal/infrastructure/database/**`
> package owns which table family, and per-`internal/application/*`
> capability-side repository hand-off) is registered in the per-section
> ownership files: `architecture/ownership/infrastructure.yaml` (adapters,
> drivers, table families) and `architecture/ownership/application.yaml`
> (capability-side repository contracts). The generated canonical view
> `architecture/ownership.generated.yaml` is rebuilt byte-deterministically
> by `cmd/architecture-aggregate` (stdlib-only concatenation; no YAML
> re-parsing round-trip to preserve comments) and consumed by
> `scripts/archcheck/main.go`. Re-verification path:
> `go run ./cmd/architecture-aggregate; diff <generated> architecture/ownership.generated.yaml`
> (canonical form registered in
> `architecture/migrations/baseline-inventory-2026-06-29.yaml::capability_owner_authority.verification_command`).
> The physical layout below is the operational view.

**Pattern (codex/db-set-and-paths, June 2026)**: every sqlite database is
opened through `internal/infrastructure/database.DatabaseSet` (`OpenSet`,
`Migrate`, `Health`, `Close`). `internal/app/composition.go` calls
`OpenSet(...)` exactly once at boot. No `sql.Open` lives outside
`internal/infrastructure/database/**`.

| Database | Path | Holds | Migrations |
|----------|------|-------|------------|
| **Primary** | `<DataDir>/media/media.db.sqlite` (compat default) | **Unico database** — scripts, jobs, media_assets, clip_folders, voiceovers, youtube_cache, gemma_memory, search_queries, sketchfab, pipeline_runs, worker_nodes, etc. | `migrations/sqlite/*.sql` |
| **Observability** | `<DataDir>/observability/api_requests.db.sqlite` | API request log table + indexes (single purpose: HTTP traffic telemetry). Distinct from Primary so log retention doesn't churn the schema-versioned Primary DB. | `migrations/sqlite/*.sql` |

**Configurable via `cfg.Storage`** (defaults preserve the previous single-file layout):

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
old `<DataDir>/media.db.sqlite` path to the canonical
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
   wired (...)"` error. The monitor path treats the error as a per-video
   skip (Error log, not panic). The composition root must wire
   `Cfg, Log, VideoPipeline, Clips, Monitors, AssetDestResolver,
   FolderMemory, SegmentsSvc` in `ServiceDeps` for `NewService` to
   wire the capability.
4. **Monitor-path recovery convention**: every channel-monitor call site
   that invokes a capability service MUST wrap the call in a
   tightly-scoped `defer recover()` (e.g. inner closure returning
   `(out, err)` with the defer reassigning `err` on panic). Tight
   scope is required: panics in `os.MkdirAll`,
   `findInterestingSegments` (LLM), Prometheus metric helpers, etc.
   must NOT be silently swallowed — they are real bugs that need to
   surface loudly. The recovery MUST include `runtime/debug.Stack()` in
   the log so a non-trivial panic (e.g. nil-deref from a port that
   escaped its nil-guard) is actionable in prod.
5. **Response shape & counter discipline**: `*youtubetypes.ExtractResponse`
   carries `OK`, `Items []ExtractItem`, `Stats` (`Requested` / `Processed`
   / `Skipped` / `Failed`), `Folder`, `Error`. The monitor path treats
   `err != nil` as hard failure (Error log), `resp == nil && err == nil`
   as a defensive hard failure (treats as misconfigured; Error log),
   `resp.OK == false` as business-level failure (Warn log + skip),
   and on success logs `resp.Stats.Processed/Skipped/Failed` as
   separate `zap.Int` fields — never `len(resp.Items)`, which would
   over-report by counting failed items.

Replacing the previous `channel-monitor` "WARN-skip placeholder":
that placeholder documented `*youtube.Service.Extract` was removed
during the ytextraction extraction; the facade above restores it
without leaking capability internal state.

`context.Background()` in non-test code: currently **~9 sites** (refactored from ~20). The remaining sites are either intentional post-write save contexts or top-level composition roots where no parent context exists. Lint gate: `bash scripts/ci-architectural-checks.sh`.


> **Package ownership**: Job-handler mapping (per-`job.Type*` registration,
> claim semantics, lease + renewal, dead-letter dispatch) is registered in
> [`architecture/ownership/jobs.yaml`](architecture/ownership/jobs.yaml);
> per-capability application-layer facade contracts (`internal/application/*`)
> are registered in
> [`architecture/ownership/application.yaml`](architecture/ownership/application.yaml);
> the generated canonical view at
> [`architecture/ownership.generated.yaml`](architecture/ownership.generated.yaml)
> is rebuilt byte-deterministically via
> [`cmd/architecture-aggregate`](cmd/architecture-aggregate) (stdlib-only
> concatenation; no YAML re-parsing round-trip; comments preserved verbatim).
> The twelve `context.Background()` (4 directly mapped to
> `scripts/ci-architectural-checks.sh::Check 1` + 8 documentation-only
> composition roots / shutdown drains / background goroutines) and
> thirteen `context.WithoutCancel` exemption sites registered in
> `AGENTS.md § `context.Background()` allowlist` are tracked here
> as INTENTIONAL EXEMPTIONS. Wave 22 promotes them to a dedicated
> CI gate via PR-CONTEXT-NO-CANCEL-CI-GATE (deadline 2026-07-15).

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

> **Authority**: Configuration boot pipeline
> (`input → load → defaults → validate → immutable`), single-points-of
> -defaults and -validation, the "business services receive narrow
> capability configuration" rule, and the ban on runtime mutation /
> duplicated fallbacks live canonically in
> [`docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#configuration`](docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#configuration).
>
> **Package-level ownership** for `internal/platform/config/**` is registered in
> `architecture/ownership/infrastructure.yaml`; arbitrary capability
> domains MUST NOT parse raw config (godlike/06). Governance keys
> (lint_gates, cross_project_refs, wave_qdrant_005d_hygiene, etc.) have
> moved entirely from the legacy monolithic `architecture/ownership.yaml`
> (725 LoC, deleted in `dc6add3e`) to `architecture/policy.yaml` per the
> "One owner per fact" rule; the per-section ownership tables (
> `architecture/ownership/{modules,jobs,services,application,infrastructure,packages}.yaml`)
> are now the SSOT for *which package owns what*, and
> `architecture/ownership.generated.yaml` is the byte-deterministic view
> rebuilt via `cmd/architecture-aggregate`.

The current loader lives at `internal/platform/config/config.go::Get()`
(target-tree Phase 2, June 2026). The 19 sub-structs of `Config` live
in `internal/platform/config/*.go` (one per file; `types.go` is the
index). The composition root must invoke `(*Config).Validate()`
**explicitly** before any capability boots — `Validate` is **not**
called inside `Get()`. After `Validate()` returns `nil`, the
configuration is treated as read-only; runtime mutation trips the
godlike/06 rule.

Security-sensitive: `VELOX_ADMIN_TOKEN` must be supplied at runtime;
production token MUST NOT be checked in.

## 10. Day-1 commands

The runtime opens the `DatabaseSet` once via `storage.OpenSet(cfg.Storage, log)`
in `internal/app/bootstrap.go::initDatabases`; no `sql.Open` lives outside
`internal/infrastructure/database/**`. Override the canonical DB paths via
the `storage.primary_db_path` and `storage.observability_db_path` config
fields (defaults preserve the previous single-file layout; see §6).

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
| Change a Drive folder | `internal/platform/config/drive.go` (struct + resolver methods) |
| Tune concurrency | `cfg.Concurrency.*`, `cfg.Jobs.MaxParallelPerProject`, `cfg.Jobs.LeaseTTLSeconds` |
| Debug a stuck job | `GET /api/jobs/:id` + `journalctl -u pipelinegen -f` |
| Add a LLM prompt version | bump in `internal/infrastructure/ai/ollama/prompts/`, record in `cfg.Scripts.PromptVersion` |

## 11.5 Target Tree (Phase 0 governance)

The repository is converging from the current two-zone
(`internal/{app,application,domain,infrastructure}`) layout toward the
**target tree** documented in
[`architecture/policy.yaml`](architecture/policy.yaml). The migration is
multi-phase; this section captures the rules and the tracking tool so
each phase can be checked objectively.

As of June 2026, the initial reference target is frozen in
[`architecture/migrations/baseline-inventory-2026-06-29.yaml`](architecture/migrations/baseline-inventory-2026-06-29.yaml)
(capture date 2026-06-29; produced by the Wave-0 close follow-up). All
migration phases are tracked there per the godlike/06 SSOT rule
(architecture docs do not enumerate per-phase state; phases live in the
ratchet tracker). Re-verification: the BASH-only
`baseline-inventory.ratchet_command_post_migration_commit` runs in the
CI container without `yq`/`bc`/`rg` and gates each migration commit
against the baseline's monotone-invariants table.

### Target zones

| Zone | Current path | Target path | Purpose |
| --- | --- | --- | --- |
| Entry points | `cmd/{server,worker,admin}/` | `cmd/{server,worker,admin,archgen,archcheck}/` | Binary main.go only. Each entry point must be a thin shell: root context, config load, `app.Compose` call, mode select, shutdown wait — **no** repo instantiation, route registration, domain parsing, raw DB open, or feature service construction. |
| Composition | `internal/app/` | `internal/app/` (kept) | Only composition root. May import all internal packages. Forbidden: business rules, SQL, DTO normalization, provider selection, ranking logic, feature-specific fallback. |
| Kernel | `internal/domain/` | `internal/kernel/{asset,job,script,event,identity,errors}/` | Shared stable concepts only. Imports **stdlib only** (plus narrow value-only libs). Forbidden: Gin types, SQL/repository impl, external clients (Drive/Qdrant/FFmpeg/Ollama/HTTP), config structs, loggers, feature flags, application services. A type belongs here only when ≥2 real capabilities need the same semantic contract. |
| Capabilities | `internal/application/{scripts,images,assets,...}` (flat) | `internal/capabilities/{assets,artlist,youtube,scripts,images,voiceover,content,channels,jobs,system}/` | One vertical slice per business capability. Each owns `{module.go, contract.go, service.go, http.go, jobs.go, events.go, repository.go, adapters.go}`. Forbidden top-level dumping-ground dirs: `service/`, `repository/`, `models/`, `utils/`, `helpers/`, `common/`. |
| Platform | `internal/infrastructure/{database,drive,qdrant,...}` | `internal/platform/{config,sqlite,drive,qdrant,ffmpeg,process,filesystem,observability,httpserver,ollama,nvidia,youtube}/` | Technical adapters only, **no** business semantics. `platform/sqlite` owns connection/transactions/migration/pragma mechanics — **capability repositories own SQL for their tables**. |
| Leaf utilities | `pkg/` | `pkg/` (kept) | `pkg/*` is leaf-only: zero imports from `internal/`. Holds retry, hashutil, concurrent, sliceutil, textutil, fileutil, urlutil, timeutil, pathutil, defaults, ptrutil, corid, apiutil, handlerutil, sqlutil, termutil, similarity, matchingconfig, testutil, veloxclient, executil. |

### Package-size caps

Caps from [`architecture/policy.yaml`](architecture/policy.yaml) — enforced by `go run ./cmd/archcheck` (Phase 0 report-only).

| Rule | Default | Trigger |
| --- | --- | --- |
| `max_files_per_package` | 40 | Hard limit; packages beyond this trigger a warn violation. Phase N (post split) promotes to gate via `--strict`. |
| `max_lines_per_file` | 500 | Production `.go` file cap. |
| `cmd_main_max_lines` | 200 | Entry-point main.go cap. Today `cmd/admin/cleanup.go` and others are above 200 → reported, not blocked. |
| Constructor direct deps | 8 (future) | Phase N will scan `func New<X>(...)` signatures with a naive arg-count check; currently a future-work note. |
| `forbidden_top_level_dirs` | `service, repository, models, utils, helpers, common` | Generic dumping-ground patterns forbidden. |

### Tracking tool

`go run ./cmd/archcheck` (Phase 0, **stdlib only**, no external deps)
reads `architecture/policy.yaml`, walks the project tree, and emits a
JSON violation report on stdout. Default mode exits 0 even when
violations exist (compat with existing CI). `--strict` promotes to a
hard gate.

The tool is **independent** from `scripts/archcheck`:

- `scripts/archcheck` — legacy ratchets (`allowedInternalRoots`,
  import-pattern drift, CI gate via `scripts/ci-architectural-checks.sh`).
- `cmd/archcheck` — target-tree governance (this section). Phase 1+ may
  consolidate the two.

### Phase ordering (how to read the migration)

| Phase | Scope | Status |
| --- | --- | --- |
| 0 (this PR) | `architecture/policy.yaml` + `cmd/archcheck` (report-only) + ARCHITECTURE.md §11.5 + AGENTS.md pointer | current |
| 1 | Phase-by-phase promotion of each rule to a hard gate via `--strict` in CI | planning |
| 2 | `internal/infrastructure/config` → `internal/platform/config` (~17 file rename) | **done (June 2026)** |
| 3 | `internal/application/<X>` orchestration files reorganised into per-capability `module.go/contract.go/service.go/...` skeleton (file content unchanged; rename only) | planning |
| 4 | SQL redistribution: `internal/infrastructure/database/sqlite/{jobs,outbox,assets,catalog,scripts,outboxevents,idempotency}` → `internal/capabilities/<X>/repository.go`. `platform/sqlite` retains only `set.go`, `migrations.go`, `rotation.go`, `backup.go` | planning |
| 5 | Kernel split: `internal/domain/*` → `internal/kernel/{asset,job,script,event,identity,errors}/`. Information-only hints already emitted by `cmd/archcheck` (#rule `kernel_split_hint`) | planning |
| 6 | `internal/api/` → per-capability `http.go` + a thinner `api/{server,routes,middleware,transport}` shell | planning |

Each phase produces a before/after `cmd/archcheck` JSON report as the
objective success metric.

---

## 12. Pointers to deeper docs

All **legacy** detailed documentation that was previously scattered in `docs/` has been consolidated and removed. **Canonical capability/operations bundles** continue to live under `docs/<domain>/` — they are NOT among the consolidated duplicates; they are authoritative meta-indexes for cross-cutting P0/P1 closures that span multiple commits and were landed under the doc-only-bundle convention (Git-Lesson-2). New domain subdirectories may be added by future bundle landings (e.g. `docs/voiceover/p0-bundle-A1-A6.md` by the PR-VO-A landing and `docs/voiceover/p1-bundle-B1-C1.md` by the PR-VO-B1-C1 landing).
For all critical rules and operational guidelines, please refer to:
- `AGENTS.md`: Critical rules (DB driver, FTS5 ban, schema boundaries, AI gen policy, agent instructions).
- `PROJECT_GUIDE.md`: Quick start guide.
- [`docs/voiceover/p0-bundle-A1-A6.md`](docs/voiceover/p0-bundle-A1-A6.md): Voiceover P0 hardening bundle (PR-VO-A1..A6, June 2026) — six-commit meta-index of canonical voiceover P0 risk closures (atomic state transitions, identity drift, path safety, accounting correctness). Authoritative index for "what A1..A6 do when treated as one bundle"; see `AGENTS.md § Recent cross-cutting closures (June 2026)` for the inline pointer.
- [`docs/voiceover/p1-bundle-B1-C1.md`](docs/voiceover/p1-bundle-B1-C1.md): Voiceover P0→P1 hardening bundle (PR-VO-B1..C1, June 2026) — four-commit meta-index of canonical voiceover P1 risk closures (Drive upload boundary, group/locale identity, sync dedupe key, HTTP endpoint unification with 90-day RFC 8594 Sunset). Authoritative index for "what B1..C1 do when treated as one bundle"; closes the "Future P1/P2 work" pointer from the A bundle.


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
- `internal/platform/config/types.go::StorageConfig` —
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
| `SearchRunnerPort` | `internal/app/youtube_adapters.go::searchRunnerStub` (compatibility adapter; implementation deferred) | returns empty + warn-log |
| `ClipIndexerPort` | `internal/app/youtube_adapters.go::clipIndexerAdapter` | wraps `*clipindexer.Service` |
| `WhisperTranscriberPort` | reserved; nil-by-default; segment.go nil-guards before call | — |
| `ClipFilesPort` | reserved; nil-by-default; segment_cache.go nil-guards before call | — |
| `HashServicePort` | reserved; nil-by-default; service.go::md5String/md5File fallback chain | leaf via `pkg/hashutil` |
| `SubtitleFetcherPort` | reserved; nil-by-default; subtitles.go nil-guards before call | — |

Empty-marker (opaque injection tokens, no method signature): 
`TempFileManagerPort`, `YouTubeCacheStorePort`.

### Canonical DTO

Un solo DTO per i metadata video: `*youtubedto.DownloaderMetadata`
(con 14 fields + `CachedAt`). Back-compat per i nomi storici:
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
- 3 latent-risk fissano l'agenda post-cascade (Thumbnails:nil, searchRunner compatibility adapter silent-empty, typed-nil panic).

---

*If you change the architecture, update this file in the same commit. The
diagram is the contract.*
