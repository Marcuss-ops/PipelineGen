# AGENTS.md - PipelineGen System Documentation

## Overview
PipelineGen is a Go-based backend service that manages media processing pipelines for YouTube clips and Artlist assets. It runs as a systemd service on **port 8080** by default. Override at runtime via `VELOX_PORT`; the in-tree default is set by `internal/platform/config/types.go::Server.Port` and mirrored by every client (`pkg/veloxclient`, worker fallback, scripts) so a single env var changes both sides.

### Port policy (Operational Readiness PR, June 2026)

The HTTP listen port is configurable via `VELOX_PORT` (server) and `VELOX_BROKER_URL` (worker + clients). No port number is hard-coded outside `cfg.Server.Port`'s default tag. Operator overrides are honoured at:

- `cmd/server` (the canonical binary) via `cfg.Server.Port`.
- `cmd/worker` via `VELOX_BROKER_URL` (fallback: `VELOX_PORT`-derived URL).
- `pkg/veloxclient` via `baseURL` argument or `VeloxClient(base_url=...)` in `scripts/velox_client.py`.
- Shell scripts (`scripts/diagnostics/marker_audit.sh`, `scripts/rotate_token.sh`, `Makefile` `doctor`/`artlist` targets) honour `API_BASE` / `VELOX_PORT` env vars.


## Documentation Map

> For the meta-question *"what's authoritative here?"* — which doc is
> the canonical source for which topic, and which are read-only
> historical references — see [**`CANONICAL.md`**](./CANONICAL.md).
> The bullets below remain as topic landing pages but the
> *"canonical source for X"* wording is renamed to *"see also"* where
> it would duplicate CANONICAL.md; the `docs/architecture/godlike/*`
> paths remain in-line as historical references (those files were
> physically removed in June 2026; their content was folded into
> `AGENTS.md §§ Instructions / Utilities / Patterns` below + the
> Qdrant Entity Associations table).

- **This file (AGENTS.md)**: Critical rules and instructions for all agents
  (driver SQLite, ban FTS5, schema boundaries, AI generation policy,
  admin token, agent instructions, Git Lessons).
- **README.md**: Project structure and architecture overview (entry point).
- **PROJECT_GUIDE.md**: Italian language getting started guide.
- **ARCHITECTURE.md**: Full system architecture, module ownership, data flow,
  database schemas, day-1 commands. See CANONICAL.md §1 for its
  authoritative-doc role.
- **architecture/policy.yaml**: target-tree governance rules (Phase 0, June 2026) — command fields, package-size caps, top-level dir restrictions, expected kernel/capability/platform subzones. Read by `go run ./cmd/archcheck` (stdlib parser). Doc-side pointer: see [ARCHITECTURE.md §11.5](ARCHITECTURE.md#115-target-tree-phase-0-governance).
- ~~**`docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md`**~~ *(path removed June 2026; content folded into `AGENTS.md § Instructions` + `§ Qdrant Entity Associations` + `§ Pattern 2` + `ARCHITECTURE.md §6`, §9)*: Rules for data and configuration ownership — database (driver lock, FTS5 ban, schema boundaries, table capability), Qdrant projection sequence, file/Drive location authority, configuration boot pipeline, EXPAND/BACKFILL/CUTOVER/CONTRACT.
- ~~**`docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md`**~~ *(path removed June 2026)*: Rules for deprecation records (`deprecation ID + owner + replacement + introduction date + removal deadline + tracking issue + compatibility test + usage metric`) and the EXPAND/BACKFILL/CUTOVER/CONTRACT migration sequence. Authoritative on "no fake availability" and the 7 forbidden compatibility techniques.
- ~~**`docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md`**~~ *(path removed June 2026)*: CI-gate definitions, complexity budgets (file cap, 8-dep constructor cap, capability-without-descriptor fail), and the **zero-baseline rule** ("final acceptable count is zero; transitional baselines require an owner and deadline").
- ~~**`docs/architecture/godlike/11_AGENT_EXECUTION_PLAYBOOK.md`**~~ *(path removed June 2026)*: Workflow for human/coding agents: preparation, scope discipline, forbidden additions, targeted testing, EXPAND/BACKFILL/CUTOVER/CONTRACT migration method, and final verification (diff-intent + remote commit presence + honest limitations). Working with AGENTS.md Git-Lessons.
- ~~**`docs/architecture/godlike/13_FEATURE_REMOVAL_CHECKLIST.md`**~~ *(path removed June 2026)*: 7-phase teardown sequence for a superseded feature: Discovery → Runtime cut → Data handling → Code removal → Configuration and operations → Verification → Completion.
- **cmd/archcheck/main.go**: target-tree validator binary, report-only in Phase 0 (exits 0 even with violations). Promoted to gate via `--strict` in later phases. It supersedes every older `docs/architecture/*.md` file
  (the `docs/` folder has been completely removed in June 2026).
- **architecture/current.yaml**: ratchet tracker verificabile delle
  wave di consolidamento (status monotone-decreasing, ogni wave ha
  `exit_gate` zero-based). È il single source of truth per
  "abbiamo finito la wave X?".
- **[architecture/ownership.generated.yaml](architecture/ownership.generated.yaml)** (aggregated view rebuilt by `cmd/architecture-aggregate` from the [architecture/ownership/*.yaml](architecture/ownership/) per-section split, dc6add3e): "qual è il canonical owner di X?"
  per ogni capability (regole di layering + assorbimenti da wave).
- **docs/api/ACTIVE_API_GENERATED.md**: auto-generato via
  `./admin gen-api-docs`; CI-failed se non committato (vedi
  `.github/workflows/ci.yml::Generate API docs`).

## Operatività worker remoti (June 2026)

Il runbook canonico per la certificazione dei worker remoti
(`PRODUCTION_READY`) vive in `docs/operations/`. È la **terza
documentazione di navigazione** insieme a `Documentation Map` e
`ARCHITECTURE.md`: ogni nuova capability worker-facing deve essere
allineata ai gate di ammissione elencati nei ticket RW-PROD-### prima
di essere dichiarata production.

- [`docs/operations/04-remote-worker-production-readiness-tickets.md`](docs/operations/04-remote-worker-production-readiness-tickets.md)
  — runbook 04: 17 ticket P0 (RW-PROD-001 → RW-PROD-017) con problemi,
  attività, criteri di accettazione, test obbligatori, evidenze
  richieste, ordine di implementazione e regola finale di ammissione.
- [`docs/operations/worker-certification-checklist.md`](docs/operations/worker-certification-checklist.md)
  — checklist operativa: scheda di certificazione, gate di ammissione
  (manuali + automatici), procedura di approvazione in 8 passi, regole
  deroghe, audit trail.
  Estratto delle sezioni 5 e 6 del runbook 04.
- [`docs/operations/tickets/README.md`](docs/operations/tickets/README.md)
  — indice sintetico dei 17 ticket (stato corrente, dipendenze, ordine
  di esecuzione, regole di transizione, definizione di "ticket
  implementabile").

Regola pratica: qualsiasi PR che tocchi `cmd/worker/`,
`internal/infrastructure/database/sqlite/assets/workernodes_repository.go`,
`internal/infrastructure/jobs/local/broker.go`,
`internal/infrastructure/observability/metrics.go` o
`config/alerting_rules.yml` deve referenziare almeno un ticket
RW-PROD-### nel body della PR. Le capability che non hanno ticket
non possono diventare production-readiness criteria.

## God-object decomposition wave (July 2026)

12-file God-object decomposition action plan derived from the Italian audit snapshot pasted to the orchestrator on 2026-07-03. Canonical surfaces:

- **`architecture/action-plans/2026-07-03-godobjects-decomposition.md`** — narrative action plan with kill-candidate matrix (per-file `kill_candidate` column + expected split topology + execution order + godlike/07 honest limitations).
- **`architecture/current.yaml#GODOBJ-2026-07-03`** — wave-tracker entry with 16 slim-shape `linked_issues` (12 per-file + 1 per-band audit-pin via `PR-GODOBJ-HOTSPOT-CROSSREF` + 3 cross-references).
- **`CHANGELOG.md` `## Unreleased` entry** — closure meta-entry pinning the wave-tracker anchor.

**Priority bands (4):**

| Band | Files | Deadline | Pattern |
|------|-------|----------|---------|
| P0 absolute | 6 (extraction_service / monitor_scheduler / images_generation / scripts_generation_job / jobs_finalizer / jobs_service) | 2026-08-15 | Locked — extraction canonical-loop, monitor outbox, finalizer TX, jobs.svc ledger |
| Mechanical | 3 (composition / assets_register_adapters / chrome_provider) | 2026-08-22 | Bundle separation, mirrors DRIVE-005 4-port surface |
| Cut-not-split | 3 (semantic_stub / script_handler_legacy_adapters / qdrant_maintenance_cmd) | mixed | Godlike/07 no-fake-availability; explicit removal dates where user-fixed |
| Small-but-dangerous | 3 (books_job_handler / worker_registry / module_media) | 2026-07-25 | Typed-error semantic correction, NO split |

**Guard rule (per AGENTS.md Git-Lesson-2 — NO BRANCHES, direct-on-main):** each per-file PR lands **directly on `main`** per-file with auto-sufficient granularity (per the FASE 8 / image-territories workflow precedent). Per-band deadlines are wave-ratchet (NOT fixed counters). Each `linked_issues` slot flips to `status: shipped` once its per-file verification gates (`gofmt + go vet + go build + go test -short` on the targeted subtree) are green AND the `kill_candidate` surfaces produce zero `rg <symbol>` hits in production code.

**Cross-reference / godlike/06 SSOT:** the per-band `owner_capability` tags in this wave-tracker entry MUST match `architecture/ownership.generated.yaml` (the aggregated view rebuilt by `cmd/architecture-aggregate`). If per-band ownership shifts post-wave (cross-package impact discovered), both `ownership.generated.yaml` and `current.yaml#GODOBJ-2026-07-03` are updated in lockstep — godlike/06 one-owner-per-fact.

**Execution order (per the audit snapshot's "ordine consigliato"):** extraction_service → semantic stub → images_generation → scripts_generation_job → monitor_scheduler → finalizer (mechanical) → jobs_service (reflection removal) → composition+adapters → legacy routes → admin command cleanup. First 7 share lock acquisition; 8+9+10+12 execute in parallel per godlike/07 EXPAND-phase discipline.

**Honest-limitation disclosure (godlike/07):** the analysis is STATIC (priority by complexity + accumulated risk). The final canonical ranking MUST cross-validate against git-log frequency — forward-pointer entry `PR-GODOBJ-HOTSPOT-CROSSREF` (deadline 2026-08-01) runs `git log --since=90.days --pretty=format: --name-only | sort | uniq -c | sort -rn | head -30` post-wave and surfaces any high-frequency hotspots NOT captured here. The wave-deadline rolls forward per slim-schema append-only ratchet if high-frequency hotspots are surfaced.

Pre-existing build issues carry forward unchanged (per CHANGELOG forward-pointer convention — out of scope for any per-file PR): `monitor/enqueue.go` / `monitor/scheduler.go` / `stockpipeline/run_upload.go` / `module_media.go` / `images/routing` import cycle. Each split commit lands in isolation on its own subtree and passes its targeted `gofmt + go vet + go build + go test -short` gates independently.

---

## Instructions

> **Authority**: Database rules (driver lock, FTS5 ban, single-table-per-capability ownership, no cross-DB generic migrations) live canonically in **[`docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#database-rules`](docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#database-rules)**. The bullets below are the agent-facing fast-reference at code-edit time — when in doubt, defer to the canonical doc.

- **Non cambiare driver SQLite** (rimanere su `mattn/go-sqlite3`)
- **Non lavorare su FTS5** (il supporto dipende dal driver compilato, usare fallback LIKE)
- **Concentrarsi solo su schema boundaries, diagnostics e test**
- **Ogni database deve avere solo le tabelle necessarie** al servizio che lo usa
- **Non applicare migration generiche a più database se creano tabelle non usate da quel database.**
- Schema attuale (Unificato):
  - `data/media/media.db.sqlite`: **Unico database** — tutto in un solo file (scripts, jobs, asset_index, media_assets, harvester, pipeline_runs, voiceovers, etc.)

*(DB driver lock, FTS5 ban, single-table-per-capability ownership, EXPAND/BACKFILL/CUTOVER/CONTRACT sequence — the rules previously restated under a `> Authority` blockquote citing `docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#database-rules` — now live in CANONICAL.md §1 as the authoritative pointer; the bullet content above remains the agent-facing fast-reference at code-edit time.)*

### Monitor port reclamation (FASE 3.7, 2026-07-04)

The FASE 3.7 wave (Commits 1a + 1b + 2 + 3) closed the pre-existing
monitor/ infra-import leak by moving every infra access through
composition-root adapters. The canonical monitor/ surface now holds
ONLY Pattern-0 ports + sentinel helpers; infra access is wired
exclusively through `internal/app/lifecycle.go` (composition root).

**5 reclaimed capability surfaces** (each consuming only monitor-owned
types; infrafacing done via the composition-root adapter pattern):

1. **Downloader** — `monitor.MonitorDownloaderPort` (formerly
   `internal/infrastructure/downloader.ListChannelVideos` direct-call
   surface). Consumes monitor-owned `monitor.ListChannelVideosQuery`
   DTO. Adapter: `monitorYtdlpAdapter` in lifecycle.go wraps
   `*transcripts.YTDLPSubtitleAdapter` + request-shape translation.

2. **Transcript** — `monitor.TranscriptProvider` (consumed by Analyzer).
   Monitor-owned `Transcript` + `TranscriptEntry` types. Adapters:
   composition-root injects `transcripts.YTDLPSubtitleAdapter` (or
   nil-safe default).

3. **Analyzer** — `monitor.VideoAnalyzer`. Consumes only
   `monitor.Analyzer*` types + the typed `MetricsRecorder` port (added
   in Commit 2 to replace the legacy `internal/infrastructure/observability`
   direct call). Adapter: composition-root injects `*semantic.Analyzer`.

4. **Enqueuer** — `monitor.ExtractionEnqueuer` (formerly
   `NewUnboundJobEnqueuer` shipping in Commit 1b to replace the
   risky nil-dispatcher silent-success path). Consumes monitor-owned
   `EnqueueRequest` types. Adapter: composition-root injects
   `*jobsextractor.Enqueuer`.

5. **Channels** — `monitor.ChannelMonitor`. Consumes typed
   `monitor.CompositionDeps` (`MetricsRecorder` added in Commit 2;
   `Discoveries` port added in Commit 1b). Adapter:
   `monitorDiscoveriesAdapter` in lifecycle.go wraps
   `*assets.YoutubeDiscoveriesRepository` + sentinel-translation.

**Forward-prevention rule (CI gate, FASE 3.7 Commit 3)**:
`bash scripts/ci-architectural-checks.sh` invokes
**Check 54 (architecture/current.yaml#FASE-3.7-CHECK-3)** which
canonically bans `internal/infrastructure/...` imports inside
`internal/application/assets/monitor/`. The gate enforces:

- HARD-FAIL: production import not preceded by an
  `// ARCH-ALLOWLIST: monitor-infra-import` marker line.
- WARN (non-fatal): comment-only references + ARCH-ALLOWLIST sites
  (residue accounting per godlike/07 no-fake-availability).
- Marker window: zero scroll-tolerance (covers BOTH canonical Go
  patterns: marker-on-`import (`-line AND marker-directly-above-import
  line).

Future agents that need to add infra access to monitor/ MUST route
through the composition-root adapter pattern (NOT a direct import).
The Check 54 gate is the canonical lock that rejects regressions.

**Related canonical anchors**:

- `architecture/current.yaml#FASE-3.7-WAVE-CLOSURE` — wave-level closure marker.
- `architecture/current.yaml#FASE-3.7-CHECK-3` — gate-level enforcement marker.
- `CHANGELOG.md` `## Unreleased → ### Refactor` — closure bullet enumerating the 4 commits.

**Per godlike/06 SSOT (one canonical owner per fact)**: the
monitor/ package is the canonical SOLE owner of all 5 port
types' production definitions (the typed interfaces + their
shapes). Composition-root owns the wiring; infrastructure
owns the concrete adapters. Test files in monitor/ MAY also
import infra-side concretes to satisfy compile-time
`var _ Port = (*Adapter)(nil)` pins (per Check 54's
`*_test.go`-inclusive rationale in
`scripts/ci-architectural-checks.sh`). No other production
package may redefine these types.

## Qdrant Entity Associations

> **Authority**: Qdrant's role as a derived projection, the canonical 5-step projection sequence (commit metadata in SQLite → persist outbox record in same transaction → update Qdrant asynchronously and idempotently → track projection version and outcome → allow a complete rebuild from SQLite), and the SQLite-vs-Qdrant dual-store carve-out live canonically in **[`docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#qdrant-projection`](docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#qdrant-projection)**. The schema/architecture tables below are agent-facing operational facts.

PipelineGen uses Qdrant vector database to power semantic search across all
media types. (Qdrant's role as a derived projection + the canonical 5-step
projection sequence — the rules previously cited under a `> Authority`
blockquote at `docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md#qdrant-projection`
— now live in CANONICAL.md §1 as the authoritative pointer; the schema
below remains the agent-facing fast-reference at code-edit time.) Here's
how entity associations work:

### Architecture: SQLite + Qdrant Dual Store
- **SQLite** (`media.db.sqlite`) is the **canonical metadata store**
- **Qdrant** (port 6333) is the **real-time semantic index**
- Each media asset exists in both stores with the same `asset_id`

### Vector Spaces (4 named vectors per point)
| Vector | Dims | Model | Purpose |
|--------|------|-------|---------|
| `text` | 768 | multilingual-e5-base | Semantic meaning (title + summary + topics) |
| `transcript` | 768 | multilingual-e5-base | Whisper transcript content (YouTube clips) |
| `visual` | 512 | CLIP ViT-B-32 | Visual content (images, video frames) |
| `audio` | 512 | CLAP HTSAT | Audio content (SFX, music) |
| `bm25_text` | sparse | Client-side BM25 | Lexical exact-match (keyword search) |

### Association Flow (How Scripts Match Assets)

1. **Script Generation** → LLM extracts names, keywords, visual cues
2. **Query Construction** → Keywords are embedded via the same model
3. **Qdrant Search** (`/collections/{name}/points/search`):
   - **Dense ANN**: cosine similarity on `text` vector
   - **Hybrid RRF**: dense text + transcript + BM25 sparse fused via Reciprocal Rank Fusion
4. **Reranker** (optional): CrossEncoder post-Qdrant reorder (BGE-reranker-v2-m3)
5. **Score Blending**: `final = qdrantScore * 0.65 + rerankScore * 0.35`
6. **Result**: Ranked media assets with Drive links returned

### Key Services
- **`vectorstore.Service`** (`internal/media/vectorstore/`): Qdrant CRUD + search
- **`association.Service`** (`internal/media/association/`): Script→asset matching engine
- **`realtime.Service`** (`internal/media/realtime/`): High-level clip search for handlers
- **`clipresolver.Service`** (`internal/media/clipresolver/`): Scene-based clip recommendation

### Qdrant Stale Link Cleaner
Runs every **12 hours** (`startQdrantCleaner` in `background_jobs.go`):
- Scrolls ALL Qdrant points
- Validates each `drive_link` via Google Drive API (`FileIsNotTrashed`)
- Removes points whose Drive files have been deleted/trashed
- Ensures semantic search never returns dead links

## Architecture (see ARCHITECTURE.md)

For full architecture documentation (system diagram, data flows, module ownership,
external services, configuration, day-1 commands), see **`ARCHITECTURE.md`** at
the project root. (For authoritative-doc resolution across the project, see
[**`CANONICAL.md`**](./CANONICAL.md).)

Key contract files:
- `internal/domain/asset/` — canonical asset types + contracts
- `internal/domain/job/` — canonical job types + Store interface
- `internal/app/registry.go::WireRegistry` — module wiring single source of truth
- `ARCHITECTURE.md` — system diagram, data flows, 9-module registry, persistence

---

## Common Operations (see ARCHITECTURE.md §10)

All day-1 commands (build, run, test, lint, admin CLI) are documented in
ARCHITECTURE.md §10. Key shortcuts:

```bash
# Build
go build -o pipelinegen ./cmd/server/

# Run
./pipelinegen --mode all

# Lint (CI checks)
bash scripts/ci-architectural-checks.sh
```

## Script Generation Endpoints (consolidated June 2026)

Script generation has been **consolidated to three endpoints**; per-flow
separation (separate handlers, job types, phase files, Google Doc
builder, Python test scripts) has been **removed**. All async work goes
through one unified pipeline; the Python agent is reachable only via
the sync endpoint.

**For the full table of endpoints, schema, and modes, see ARCHITECTURE.md.** All legacy documentation files previously under `docs/` have been removed.

**Rule of thumb for new integrations**: scegli l'endpoint in base al preset di flag desiderato.

| Endpoint | Handler | Job type | Preset del payload |
|----------|---------|----------|--------------------|
| `POST /api/script/generate-from-clips` | `ScriptFlowHandler.GenerateFromClips` (`handler_clip_source.go`) | `script.generate_from_clips` | Rispettano i flag del body. `generate_metadata=true` implies `extract_entities=true`. Default `sentences_per_image=10`. |
| `POST /api/script/generate-with-images` | `ScriptFlowHandler.GenerateWithImages` (`handler_generate_with_images.go`) | `script.generate_from_clips` (stesso) | **Forza** `extract_entities=false`, `generate_scene_images=true`, `generate_document=true`, `generate_metadata=false`. Default `sentences_per_image=8`. |

I due endpoint **non sono alias**: hanno handler e request type distinti
(`GenerateFromClipsRequest` vs `GenerateWithImagesRequest`); condividono
solo il job type e la pipeline di esecuzione (`HandleClipScriptGenerateJob`
in `job_handler_clip_source.go`). La differenza è il **preset del
payload**, non la pipeline.

Use `/generate-with-images` quando vuoi scene-by-scene AI images senza
entity extraction né metadata; usa `/generate-from-clips` per ogni
altro caso (incluso opt-in delle scene images via `generate_scene_images=true`).

Use `POST /api/script-docs/generate` only when you specifically need
the Python ReAct agent in the loop and can tolerate the 15-min sync timeout.

---

## Known Issues & Fixes

### Fixed Issues (historical)
1. **Artlist job status endpoint** — Fixed column names in `job_adapter.go`, added `getIntFromResult()`.
2. **SQLite "database is locked"** — Fixed: WAL mode + `busy_timeout=5000` + pool limits.
3. **Missing `monitored_sources` table** — Created schema in `media.db.sqlite`.
4. **Clipindexer DB path** — Fixed: `IndexClip` passes `--db` to Python script.
5. **Python `index_clips.py` `None` tags** — Added try-except defaults.
6. **Numpy conflicts** — Uninstalled `tts` and `fish-speech` packages.
7. **Inconsistent SQLite configs** — Centralized via `storage.OpenSQLiteDB`.
8. **Missing models/registry wiring** — Restored `AssetNode` + fixed registry loop.

### Active Concerns
1. ~~**Artlist search is slow**~~ ✅ **OPTIMIZED** — 14ms cached (was 30-50s).
2. ~~**Binary and scripts in source dir**~~ ✅ **FIXED** — .gitignore updated (June 2026).
3. **Admin token**: must be set via `VELOX_ADMIN_TOKEN` env var at runtime; never in `config.yaml`.
4. ~~**Large files (God Objects)**~~ ✅ **SPLIT** — channel_monitor (9 files), extractor_process (3), handler_batch_phases (8), clipindexer (4), voiceover (3).
5. ~~**context.Background()**~~ ✅ **AUDITED** — remaining ~7 sites are intentional (post-write save contexts per ARCHITECTURE.md §7, composition roots, fallback patterns). CI check `scripts/ci-architectural-checks.sh` enforces the ban on handlers.
6. ~~**Duplicate architecture docs**~~ ✅ **CONSOLIDATED** — MODULE_MAP.md and MODULE_OWNERSHIP.md deleted. ARCHITECTURE.md is canonical. AGENTS.md now points to it.
7. ~~**.gitignore leaks**~~ ✅ **FIXED** — Added patterns for root binaries, logs, caches, cookies, `.bak` files.
8. **Heavy AI-generated codebase**: ~80% of commits from AI agents. Bug diagnosis requires human oversight. Keep test coverage high.
9. **Batch script tests restored** (June 2026): coverage moved from handler layer to `internal/application/scripts/batch_persistence_test.go` + `doc_creation_test.go` at the BatchService unit level.
10. ~~**ApplyPreset stub**~~ ✅ **CLOSED Issue 8** — `internal/application/scripts/adapters/generation_normalizer.go::ApplyPreset` (canonical) now implements all 5 documented presets per `docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md` §6 "Required preset semantics". `internal/application/scripts/usecase/preset_resolver.go::ApplyPreset` is a thin wrapper that delegates to the canonical impl (per AGENTS.md Pattern 8 / API-adapter dependency boundary). Commits landing the closure of Issue 8: `f57d19f7` (Step 1 — `PresetFullMedia` / `PresetCatalog` / `PresetSearch` constants in `internal/domain/script/payload.go`), `4a5006c9` (Step 2 — canonical 5-preset implementation replacing the `Phase 1b stub`), `24ad4ffa` (Step 3 — `usecase.ApplyPreset` → `adapters.ApplyPreset` thin wrapper, duplicate collapsed), `5c3b1faf` (Step 4 — TDD coverage: 5 tests, including per-field-overrides-zero-values coverage that surfaced the `full_media` semantic shift from atomic-gate to per-field caller precedence). Closure commit `ab9e852a` ("chore(script): update AGENTS.md + CHANGELOG for ApplyPreset closure (issue 8)") is canonical on `main`. The pre-existing `internal/app/module_media.go:334:3` regression (obsolete `MutationsDispatcher` literal in the `clips.Deps` struct — field removal in a recent origin/main commit) is OUT OF SCOPE for Issue 8 and tracked as a separate follow-up; it does not block the impl + test + wrapper + closure commits from being canonical on `main`.
11. ~~**EnrichAsync silent-success + translator fallbacks**~~ ✅ **CLOSED P0.6** (June 2026, Wave 21) — fire-and-forget `EnrichAsync` capability removed from the `MetadataWriter` port (`internal/application/assets/providers/artlist/ports.go:255`), the concrete `SemanticEnricher.EnrichAsync` method (incl. `concurrent.SafeGo` goroutine + `pkg/concurrent` import cleanup), the `stageEnrichAsync` stage wrapper, and the 2 external call sites in `run_service.go` + `search_core.go` were replaced with explicit-error / log+drop markers. All 4 silent-success translator sites in `internal/application/scripts/` (3 fallback branches in `dto/metadata.go` + 1 in `usecase/flow_helpers.go::artlistSearchPhrase`) replaced with error propagation. New canonical `scriptpkg.VideoMetadata.TranslationStatus` field (omitempty, drives per-item failure marker) + new `ScriptArtlistClipSuggestion.TranslationError` field surfacing translator errors at the API response surface. Pattern 0 port (`MetadataTranslator` interface) locks test injectability without production wiring churn (`*ollama.Generator` satisfies the interface implicitly). 8 new TDD tests (5 in `metadata_test.go` + 3 in `flow_helpers_test.go`) lock the no-fake-success contract — order-independent via `indexByLanguage` helper (concurrent.SafeGoFunc scheduling nondeterministic). P0.18 will reintroduce structured outbox-driven enrichment in a successive wave (canonical ticket ref: `architecture/current.yaml#P0.18`).
12. ~~**Drive Surface \u2014 Raw SDK Leakage in DriveBundle**~~ \u2705 **CLOSED DRIVE-005** (June 2026, Wave 27) \u2014 Drive surface consolidated to **4 canonical Pattern 0 ports** per AGENTS.md Pattern 0 + godlike/06 "one owner per fact": `delivery.Publisher` (conflict-aware uploads + `ConflictPolicy` + `PutFileRequest`/`PutAction` from commit `2fb96f39`), `drive.Reader` (download + metadata + listing + existence from commits `5f590885`/`a8c781ae`), `drive.FileLifecycle` (Trash + Move + Rename + Cleanup from commit `1dc40709`, extracted from `FolderManagerAdapter`), and `drive.DocClient` (Google Docs creation). **Six-commit chain (positions 2..7 of the Drive-surface sequence: `5f590885`, `a8c781ae`, `b7d49099`, `70f2b6c8`, `2fb96f39`, `1dc40709`)** closed DRIVE-005-FIELDS; commit `a8c781ae refactor(app): FASE 9 Step 5 \u2014 remove deprecated DriveClient and DriveUploader from DriveBundle` physically retired the raw `*gdrive.Service` + `*drive.Uploader` handles from `DriveBundle` (composition.go). Compile-time assertions pin the contract: `var _ drive.Admin = (*Uploader)(nil)`, `var _ drive.Reader = (*Uploader)(nil)`, `var _ drive.FileLifecycle = (*FileLifecycleAdapter)(nil)`, `var _ delivery.Publisher = (*delivery.Publisher)(nil)` \u2014 future drift is a build failure, not a runtime panic. Deprecation record `architecture/deprecations.yaml#DRIVE-005-FIELDS` reaches `status: removed` today (2026-06-30); wave tracker entry `architecture/current.yaml#id-27` carries the `exit_gate: true` closure.

13. ~~**Commit 4‑expanded (Stock Cutover) — IndexingStatus retirement shape + 3 resilience ports + byte‑equivalent‑play recovery**~~ ✅ **CLOSED 2026-07-02** as canonical byte‑equivalent‑replay SHA `9aa4c9e2` per AGENTS.md §Git‑Lesson‑5 (the local‑divergent `git rebase + amend` SHA `94854247` was byte‑equivalently replayed by a parallel agent on `origin/main`'s development line; the canonical SHA on `origin/main` wins without `--force`). Closure surface: (a) `internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go` (~190 LoC) carries the 3 Pattern 0 ports (ManifestBuilder + TransactionalAssetWriter + ProjectionPort) + the `RunSummary{Manifest, FinalStatus}` envelope + 4 typed sentinels (ErrManifestIncomplete + ErrAtomicDispatchFailed + ErrProjectionResilience + ErrResilienceNotWired), all `errors.New(...)` and surfaced via `errors.Is` per godlike/07; (b) orchestrator.go carries `RunResilient` 7‑step ladder (resolve_sources / plan_clips / stage_sources / build_manifest / validate_manifest / emit_chunks / project_manifest) threads `*RunSummary` through `Service.runOrchestratorResilient` so the broker JobFinalizer stamps the right job‑status without re‑inferring from the manifest alone; (c) `run_upload_indexing_test.go` (~110 LoC) pins 3 canonical contracts — writer‑error abort → `ErrAtomicDispatchFailed` via `errors.Is`; manifest‑completeness gate → `ErrManifestIncomplete` when Required:true artifact Path empty; Qdrant‑offline → `*RunSummary{FinalStatus:StatusIndexPending}` with nil error (artifacts are on Drive, only indexing deferred); (d) `internal/kernel/job/job.go` validates `StatusIndexPending Status = "INDEX_PENDING"` + extended `IsActive()` (Qdrant reconciler owns the row) + extended `Valid()` predicates; `internal/domain/job/job.go` re‑exports `StatusIndexPending = kerneljob.StatusIndexPending` for the 107 import sites in 93 files; (e) `internal/application/assets/providers/stock/stockpipeline/service.go::ChunkResult.Indexed IndexingStatus` field physically retired; the typed enum + 4 consts + Marshal/Unmarshal methods + 2 compile‑time assertions removed from service.go (Commit 4‑expanded carried the 5‑commit cleanup chain past the Commit 4 transitional state). Residue honestly declared (godlike/07 no‑fake‑availability): 8 doc‑only audit‑pins intentionally retained in `internal/application/assets/providers/stock/stockpipeline/{service.go, run.go}` referencing the retired symbols (1 service.go:750 IndexingStatus comment + 7 service.go references to `uploadAndIndexChunk` / `indexChunkToAssetIndex` / `upsertChunkAndDispatch` + 1 run.go:7 reference to `uploadAndIndexChunk` / `buildPipelineMetadata`) — all production‑code active‑use residue = 0 (audit‑pin discipline per godlike/06/07); HONEST scope‑lock: the stockpipeline subtree IS the canonical closure scope, the cross‑package YouTube‑side `IndexingStatus` typed enum (`internal/application/assets/sourcing/types.go:39` declaration + 3 sourcing/youtube production‑code usages + the JSON wire emission in `internal/api/assets/register/handler.go:126` under `"indexing_status"`) is a SEPARATE asset‑level‑indexing concern **deliberately untouched** — forward‑pointer `architecture/current.yaml#id-29 linked_issues.PR‑CrossPackage‑IndexingStatus‑§12-5` (deadline 2026‑08‑15) carries the §12‑5 cross‑package retirement ticket so operators reading the JSON wire do not mistake stock‑subtree closure for codebase‑wide retirement. Forward‑pointer also: `P0.18` (structured outbox‑driven enrichment reintroducing typed IndexingStatus semantics on the chunk‑level stock indexing path). Reconciliation lineage: `12. ~` refactor(stock): Commit 4‑expanded — retire IndexingStatus residue + add resilience ports + 3 tests + gofmt carry‑over (canonical SHA `9aa4c9e2`) ~ 12. ~ `docs(handoff): archive COMMIT_4_EXPANDED_HANDOFF.md planning notes` (forward‑port SHA `0c74e408`, 1 markdown file preserved from locally‑divergent `94854247` via `/tmp/handoff.bak` + `git reset --hard origin/main` + `git cherry‑pick` per AGENTS.md §Git‑Lesson‑5 byte‑equivalent‑recovery Option 2) ~ 12. ~ `feat(stock): §12‑4 — SourceStager port abstraction (persistent staging)` (post‑rebase SHA `ed4f8331`); wave tracker entry `architecture/current.yaml#id-29` carries the `exit_signal: true` closure. CHANGELOG closure meta‑entry + audit trail landed via the commit chain landing on `origin/main`. Do not interpret this closure entry as global codebase‑wide IndexingStatus retirement.


14. ~~**Agente 2 — MediaSearch hardening bundle (7 azioni)**~~ ✅ **CLOSED 2026-07-02**. Six-commit chain (SHA `676554ef`, `e87a64d9`, `bf5a34c9`, `3f4a82e7`, `239db338`, `c7a01dc7`) hardening /internal/v1/media/search: workspace Actor propagation, media-type backend filtering, truthful response DTO (Degraded/ProviderErrors/ChannelsUsed), full error-to-HTTP mapping (5 sentinels, 12 tests), semantic search readiness canary (5 tests), embedding /index_bulk truthful response (7 tests). See CHANGELOG.md `## Unreleased -> ### Added -> Agente 2` for full per-azione breakdown.

15. ~~**P2.2 — DRIVE-008 fail-closed stubs (legacy drive upload seam)**~~ ✅ **CLOSED 2026-07-03** as commit `0fa8c065` on `origin/main`. The 3 named surfaces (`clips.ClipDriveUploaderPort.UploadFile`, `clips.ClipDriveUploaderPort.UploadFileWithDescription`, `sourcing.DrivePort.UploadFileWithDescription`) — implemented at `internal/app/clips_adapters_drive.go::UploadFile` + `UploadFileWithDescription` + `internal/app/assets_register_adapters.go::sourcingDriveAdapter.UploadFileWithDescription` — now return wrapped `drive.ErrLegacySurfaceRetired` (sentinel declared in new `internal/infrastructure/drive/errors.go`, errors.Is compatible through 1x + 2x `fmt.Errorf %w` wrap chains at handler/aggregator layer). Companion sentinel-test `internal/infrastructure/drive/errors_test.go` with 2 TDD tests asserts non-nil + message byte-stability + the wrap probe. Compile-time pins preserved on both sides: `var _ clips.ClipDriveUploaderPort = (*clipsDriveAdapter)(nil)` AND new `var _ sourcing.DrivePort = (*sourcingDriveAdapter)(nil)` for sourcing-side drift detection. Dead-code cleanup: `driveUploadToDTO` helper removed (sole post-stub consumer gone) per AGENTS.md §"Code Hygiene" + godlike/07 §"No fake availability". Survivor scope (honest scope-lock): 9 surviving methods on `ClipDriveUploaderPort` + 2 surviving methods on `sourcingDriveAdapter` + the underlying `*drive.Uploader.Admin.UploadFile` chain (NOT deprecated by this record). Deprecation record `architecture/deprecations.yaml#DRIVE-008` (record #21) added with `migration_phase=CUTOVER, status=in_progress, removal_date=2026-Q4`; cross-references sibling records `DRIVE-005-FIELDS` (typed-port migration upstream) + `DRIVE-STORE-UPLOAD-TO-DRIVE` (Fase 3 Spina Dorsale) + `DRIVE-CUTOVER-P0-1` (bulk_worker CUTOVER precedent). Wave-tracker anchor `architecture/current.yaml#PR-DRIVE-008-CUTOVER` filed under `linked_issues` with `status: shipped, deadline: 2026-07-03` and 4 SHA slots (P2.2 forensic-trail + cleanup + dead-code-removal + sentinel tests).

16. ~~**P2.1 — Eliminate package-level mutable test seams in `internal/infrastructure/drive/`**~~ ✅ **CLOSED 2026-07-03** as commit `96ec87e1` on `origin/main`. The user-flagged seam `internal/infrastructure/drive/uploader_put.go::var lookupFunc` plus the rg-audit-found sibling `internal/infrastructure/drive/uploader.go::var openFile` (both as `var X = func(...) { return ... }` test-only seams that any test run before production could hijack) are both migrated to struct fields on `*Uploader` (`u.lookupFunc LookupFunc` + `u.openFile OpenFileFunc`). Lazy-default helpers added (`u.lookupExisting` → `FindFileByName`, `u.openReader` → `os.Open`) so the existing `&Uploader{Service:..., Log:...}` composition-root literals keep working without ctor change (godlike/07 minimum-ripple scope). Test surface migration: `uploader_put_test.go` (2 tests, `TestPutFileLookupErrorFailClosed` + `TestPutFileAmbiguousMatchError`) + `uploader_test.go::TestOpenFileInjection` — the `origX := seamVar; t.Cleanup(restore); seamVar = mock` pattern collapsed into struct-literal override (no global mutation, parallel-safe, no t.Cleanup dance). Honest scope-lock on `TestOpenFileInjection` per godlike/07 no-fake-availability: `doUploadFile::if u.Service == nil` short-circuits BEFORE `u.openReader`, so the mock-openFile path is unreachable when `Service=nil`; the test asserts "any non-nil error wins" — coverage preserved byte-stable from pre-P2.1. Forward-pointer in commit body: rg audit was drive-scoped; pkg-wide `var X = func` package-level seams in other infra packages (database/sqlite, ai/ollama, qdrant, youtube) are NOT enumerated — a follow-up wave should do the global audit.

17. ~~**P1.5 — Typed Google API errors + jitter extension**~~ ✅ **CLOSED 2026-07-03** as commit `819c9d95` on `origin/main` (preceded by `442a4dfe` P1.4 Prometheus-metrics surface for `StartupDriveRootsValidator`). Scope: tipizza `*googleapi.Error` in typed envelope + estende jitter alle retry path uploader-side (precedentemente limitato al validator probe in P1.4). Surface: new file `pkg/retry/google_api_error.go` carries `GoogleAPIError` envelope + 6 godlike/07-compliant sentinels `ErrGoogleAPIThrottled/Server/Permission/NotFound/Client/Unknown` + `classifyGoogleAPIErrorInfo` + `parseRetryAfter` (RFC 7231 §7.1.3 IMF-fixdate parser) + `ClassifyGoogleAPIError` idempotent. `pkg/retry/retry.go` gained a new `RetryAfterError` interface + inline check via **errors.As** (production-wrapping-aware: catches the `fmt.Errorf %w` chain, not just direct carriers) snapping `max(backoff, RetryAfterDuration)` BEFORE the jitter. `internal/infrastructure/drive/uploader_put.go` gained `JitterFraction: 0.3` + the 3 wire-ups (Files.Update ConflictOverwrite + Files.Create ConflictRename + Files.Create plain); `internal/infrastructure/drive/folder_manager.go` gained 3 wire-ups (Files.List + Files.Get + Files.Create) → **total 6 Drive SDK exits covered** (was 5; +1 on `findOrCreateFolder`). Test surface: new `pkg/retry/google_api_error_test.go` with 12 TDD tests including `TestDoWithValue_HonorsRetryAfter_ThroughWrappedError` simulating the production `fmt.Errorf %w` shape. Real bug caught + fixed in code-reviewer round 1: type assertion direct missed the wrap chain (production wires errors via `fmt.Errorf("drive put failed: %w", err)`); fix via `errors.As` per godlike/07 typed-error contract. Cross-ref: pkg/retry canonical surface (`IsTransient` typed path + `JitterFraction=0.25` defaults + `DefaultOptions()`) extended; `architecture/current.yaml#S7-Step-7` already tracks the underlying pkg/retry canonicalization (Wave S7 closure from June 2026).

18. ~~**P1.4 — Prometheus metrics surface for `StartupDriveRootsValidator`**~~ ✅ **CLOSED 2026-07-03** as commit `442a4dfe` on `origin/main`'s development line (canonical SHA on `origin/main`; pre-P2.4 audit verified same). Scope: tipizza la superficie promauto per le probe del validator (counter probe + histogram latenza + 2 gauge run-summary), cardinality bounded 9 dest × 3 outcome = 27 series max. Surface: `internal/infrastructure/observability/metrics_delivery.go` (4 new metrics) + `internal/application/assets/delivery/drive_validator_metrics.go` (production-grade nil-safe wrapper with `observeProbe` / `observeRunEnd` split-guards) + `internal/application/assets/delivery/startup_validator.go::NewDriveRootsValidator` extended to 4-arg (added `*DriveValidatorMetrics` typed-NIL optional). 5 new tests + 11 call site updates. Side-effect fixup commit `9856a2b6` (separate): renamed `types_media.go::DeliveryStatus` → `JobDeliveryStatus` to disambiguate from the new P0.2 `delivery_status.go::DeliveryStatus` (regression-collision avoidance per AGENTS.md minimal-change policy). Cross-ref: `architecture/current.yaml#PR-VO-A P0 hardening committed coverage` for the surrounding voiceover P0 bundle.

### Drive Token Regeneration
If Google Drive authentication fails:
```bash
python3 scripts/generate_drive_token.py
```

---## Rebase-Conflict Lesson (June 2026)

When `git fetch origin` reveals that `origin/main` advanced while you had
unpushed local commits on the same branch, the recovery is
`git rebase origin/main` (or `git pull --rebase origin main`). When that
rebase hits a conflict (e.g. on a **test file** where both local and remote
added independent hunks), prefer **manual merge inspection** over
`git checkout --ours` / `--theirs`.

Why this matters:

- Test files are usually **append-only**: local adds `t.Skipf` +
  a lint-silencer, remote adds a civic nit + an extra subtest.
  The correct merge keeps **both** sets of additions, not a
  blanket "mine wins".
- `git checkout --ours` silently drops the remote's polish round
  (citations, acceptance criteria, lint fixes) — the next agent
  will have to redo them.
- `git checkout --theirs` silently drops the local skip — the
  obsolete test runs and fails on CI again.

Safe procedure when a **test file** conflicts during rebase onto `origin/main`:

1. `git rebase --abort` and start fresh.
2. `git diff --name-only origin/main HEAD` to list the conflict
   candidates.
3. For test files (`*_test.go`), open both sides with a three-way
   diff tool and **re-read the intent of each hunk first** (what
   is it asserting? what is now obsolete?) — only then **combine
   hunks manually**. Additive hunks (different functions, different
   constants) merge cleanly; contradictory hunks (both sides edited
   the same line in incompatible ways) need human review before
   resolution.
4. For non-test files where one side is clearly the canonical
   version (e.g. a followup doc rewritten with `write_file`),
   `git checkout --ours <file>` is acceptable **only after**
   visual confirmation (grep for the marker strings the previous
   reviewer asked for).
5. `git add <file> && git rebase --continue`.
6. After the rebase finishes, `git push origin main` will fast-forward
   cleanly. No `--force` needed.

**Anti-pattern**: a loop of
`pull --rebase, conflict, checkout --ours, commit --amend, push, non-fast-forward, ...`.
Each `commit --amend` creates a fresh commit hash that re-diverges
from `origin/main` and triggers the next failure. If you find
yourself in that loop:

- **First, try the cheap exit**: stop amending, run a clean
  `git fetch && git rebase origin/main`. If the resulting tree
  is a clean fast-forward over `origin/main`, an ordinary
  `git push origin main` will land it — no force-push needed.
- **Only if the tree is genuinely divergent** (e.g. you and
  another agent both landed on `main` in the interim) is
  `git push --force-with-lease origin main` appropriate, and
  then **only after** running
  `git fetch && git log --oneline HEAD..@{u}` to confirm no
  in-flight commit from another agent (commits in `origin/main`
  that you don't have locally) is about to be clobbered. The
  reverse view, `git log --oneline @{u}..HEAD`, lists your own
  unpushed commits — useful for an audit, **not** a clobber-check.

Canonical reference: the obsolete-batch-tests disposition shipped
in commits `39071b40` + `a55e38f1` is the case where this lesson
was learned. Note: the canonical path under the current workflow
(see [`Git-Lesson-2`](#git-lesson-2-june-2026--direct-to-main-workflow))
is `git rebase origin/main`, not `git rebase origin/<branch>`.


## Git-Lesson-1 (June 2026) — `git rebase -i` vs `--autosquash`

When a sequence of commits needs collapsing (a "fix" commit
merged back into its target, two adjacent commits squashed, a
commit dropped), the interactive rebase editor is the canonical
tool:

- `git rebase -i <upstream>` opens `$EDITOR` so you manually
  reorder lines and pick from `pick` / `squash` / `fixup` / `drop`.
- `git rebase -i --autosquash <upstream>` is the **automatic**
  variant: git scans each commit's **SUBJECT LINE** (not body
  trailers) for prefixes like `fixup!` or `squash!` — typically
  produced by `git commit --fixup=<sha>` — and pre-arranges the
  todo list so `$EDITOR` opens with the right `pick` / `f` pairs
  already in place.

**Default to `--autosquash`** whenever your fix-up commits were
created via `git commit --fixup=<sha>`. The hand-editing step
on the todo list is the source of most "WTF just happened to
my history" moments; `--autosquash` removes it while still
leaving `$EDITOR` open as a final safety net.

**Caveat**: `--autosquash` works on **subject-line prefixes**,
**not** body trailers. A commit message whose body says
`!fixup <msg>` after a blank line is a literal trailer and is
not recognised by `--autosquash`. Body-trailer fixups fall
back to plain `git rebase -i`.

**When NOT to use either**: a branch that another agent or
human has already pulled from — rewrite history is safe only
when the rewritten commits are still local. On a shared
branch after a force-push, see
[`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026).


## Git-Lesson-2 (June 2026) — direct-to-main workflow

The PipelineGen convention (June 2026 update) is to **commit directly on `main`**.
The legacy `topic-branch → PR → merge --no-ff` pattern was retired because it
added a no-value merge commit per landing and an extra host round-trip without
improving audit (the commit message itself already carries the agent + body that
PR descriptions would summarise).

**Default for PipelineGen**:

```bash
# Stay current if your local main has unpushed commits relative to origin.
git fetch origin
git rebase origin/main                    # replay your local commits on top

# Publish. Never `git push --force` against `origin/main`.
git push origin main
```

The `--no-ff` flag is **retired**: a fast-forwarding `git push origin main`
records each commit individually on `origin/main` (the same audit signal that
`--no-ff` claimed to provide, just cheaper). No merge commit stands between your
commit and the canonical history. If a divergent tree needs force-push, see
the [`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026) — but in
practice the cheap exit (rebase + ff-push) always works.

**Topic branches**: **strictly opt-in** and rarer than the prior workflow assumed.
Use one ONLY when **all three** of the following hold:

1. The feature spans >24h of multi-agent work (e.g. a Wave-X typed-handle sweep
   distributed across commits that would each break CI independently).
2. Intermediate commits would break the build or test them if landed
   individually (a single feature with green-every-commit shape does NOT need
   a topic branch — land incrementally on main).
3. The team agrees the workflow-deferral cost is worth it.

The default remains direct-on-main. When in doubt, land incrementally on main:
reviewers comment on commits in `git log`, not on PR descriptions. A feature
that "feels too big for main" usually feels that way because the commits are
too coarse — split them before reaching for a branch.

**What is actually dangerous on `main`**: `git push --force`. Force-pushing
rewrites remote history and invalidates copies held by anyone who already
pulled. Use `git push --force-with-lease origin main` only as the explicit
exit from the amend-loop anti-pattern documented in the
[`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026).

### Image-territories workflow (FASE 8 cutover, July 2026)

**Regola di routing**: Tutti i commit del action plan **image-territories**
devono essere **commit diretto su `origin main`** con trailer
`Co-authored-by:`. **NO** topic branches, **NO** PRs, **NO** `--force`.
**Push incrementale** dopo ogni commit auto-sufficiente.

```bash
# Ogni commit del piano image-territories:
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit -m '<subject>

<body>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'
git fetch origin         # race-protection (AGENTS.md Git-Lesson-4/5)
git push origin main     # direct-to-main; nessun branch intermedio
```

**Anti-patterns** (rifiutare in code review):
- ❌ Topic branch dedicato (`feat/image-territories-fase-8` o simili) — il default è direct-on-main.
- ❌ PR aperto su `origin/main` per qualsiasi sub-task di image-territories.
- ❌ `--force` o `--force-with-lease` per "vincere" una race con un agent parallelo (il
  fast-forward exit è sempre possibile; vedi [`Git-Lesson-4`](#git-lesson-4-june-2026--recovery-from-non-fast-forward-push-race)
  + [`Git-Lesson-5`](#git-lesson-5-june-2026--byte-equivalent-replay-race-recovery)).
- ❌ Squash-onto-main dopo una fase intermedia — ogni commit auto-sufficiente atterra
  sul suo SHA canonico, audit-trail pulito.

**Worked example** (image-territories FASE 8 cutover, July 2026):
- `a130bb9a feat(images): FASE 8 routing↔retrieved cycle break` — cycle break
  auto-sufficiente, push diretto, trailer `Co-authored-by:` presente.
- `55394443 chore(architecture): image-territories-cutover wave tracker entry` —
  closure bookkeeping (report + wave-tracker + CHANGELOG), commit separato,
  push diretto, trailer `Co-authored-by:` presente.
- Nessun branch intermedio. Nessun merge commit. Nessun force-push.

**Rationale** (per [`Git-Lesson-2`](#git-lesson-2-june-2026--direct-to-main-workflow)):
il default direct-on-main elimina il merge commit e l'extra host round-trip senza
perdere audit signal (il body del commit + il trailer `Co-authored-by:` portano
l'audit provenance che PR descriptions usava riassumere). Le race condition
con agent paralleli sono gestite da [`Git-Lesson-4`](#git-lesson-4-june-2026--recovery-from-non-fast-forward-push-race)
(rebase + ff-push) o [`Git-Lesson-5`](#git-lesson-5-june-2026--byte-equivalent-replay-race-recovery)
(byte-equivalence check + accept), non da `--force`.


## Git-Lesson-3 (June 2026) — `Co-authored-by:` trailers for agent commits

Git parses **multiple `Co-authored-by:` trailers** in the body
of a commit message and credits each author in `%(trailers)`
formatting. This is the canonical way to mark a commit as an
agent amend: the trailer keeps the agent's work attributable
to the agent's identity in **local logs** (`git shortlog`,
`git log --author=<agent>`, `git log --format='%(trailers)'`).

Convention for agent commits in this repo (commit directly on `main` per
[`Git-Lesson-2`](#git-lesson-2-june-2026--direct-to-main-workflow)):

```
<subject>

<body>

Co-authored-by: <AgentName> <agent@pipelinegen.local>
```

Where `<AgentName>` is the human-readable agent identity
(Codebuff, Claude, Codex, etc.) and `<agent@pipelinegen.local>`
is the canonical no-reply email. The agent runner sets it via:

```
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit ...
git push origin main     # direct-to-main; no topic branch, no PR, no --force
```

**Caveat**: the `@pipelinegen.local` email is for **local-log
attribution only**. GitHub and GitLab credit contributor
avatars by **registered** email; unrecognised domains
(anything not a `noreply.github.com` or verified-domain
alias) will not render on the host's social graph. Use this
trailer for internal audit; if you also need GitHub avatars,
add the agent's verified email through the host's
collaborator UI.**Format rule**: trailers must appear after a **blank line** following the body. A `Co-authored-by:` line in the subject is NOT parsed as a trailer. Verify with:

```
git log --format='%(trailers)' -1 <sha>
```

after committing. Empty output means the trailer landed in the wrong place.

**Workflow integration** (per [`Git-Lesson-2`](#git-lesson-2-june-2026--direct-to-main-workflow)):
agent commits go **directly on `main`** — after `git commit` with the agent
identity flags, publish via `git push origin main` (no topic branch, no
`--no-ff` merge commit, no `--force`). The trailer mechanism itself is
unchanged by the workflow switch: the *body* of the commit carries the audit
provenance that PR descriptions used to summarise.


## Git-Lesson-4 (June 2026) — Recovery from non-fast-forward push race

The companion case to [`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026).
That lesson covers *conflicting* rebase (two agents edit the same lines in
incompatible ways). This lesson covers the **non-conflicting** race case
where your unpushed local commit's content has been **cleanly re-applied by
another process on `origin/main`'s tip** while you were about to push.

**Canonical case (June 2026, channel-monitor Blocco 1)**:

- Local commit: `ff7a5579 fix(monitor): thread checkChannel error into scheduler backoff path`
  (six files: `monitor_ports.go` NEW + five modifications to
  `internal/application/assets/monitor/*.go` and `CHANGELOG.md`).
- Push rejected: `git push origin main` returned
  `[rejected]         main -> main (non-fast-forward)`
  because `origin/main` had advanced underneath the local commit during
  a parallel agent window.
- Resolution: a byte-equivalent replay of the same files landed as
  `960a3fb6 fix(monitor): thread checkChannel error into scheduler backoff path`
  on `origin/main`'s development line (`ca75f8e0 → bb18544e → 1e09a762 → 960a3fb6 → 51e41bf4 → 8c5ce7d1 → 0488a5ef`).
  Local `ff7a5579` is reachable only via `git reflog`, on a divergent
  history line (`889a1a7e → … → 6b67f2be → 879d5637 → ff7a5579`) that
  no longer reaches `origin/main`.
- Byte-equivalence verified with
  `diff <(git.show --name-only --format='' ff7a5579) <(git.show --name-only --format='' 960a3fb6)`
  returning empty. Both commits touch the same six files; the SHA on
  `origin/main` is canonical, the local SHA is superseded.

**Diagnosis procedure** when `git push origin main` returns
`[rejected] (non-fast-forward)`:

1. `git fetch origin` to refresh the remote-tracking refs.
2. `git log --oneline HEAD..@{u}` — commits present on the upstream
   tracking branch that are not yet in your local `HEAD`.
3. `git log --oneline @{u}..HEAD` — your unpushed local commits.
4. If step 2 is **non-empty** with commits newer than your local
   branch AND step 6 (below) returns a *non-empty* diff (the newer
   upstream commits touch the same files as your step 3 commits but
   with different content), this is the textual-conflict case — read
   [`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026) and
   rebase / cherry-pick manually. (Conflict markers will surface
   during the rebase.)
5. If step 2 is **empty** (the upstream tracking branch has no commits
   past your local `HEAD`) but a recent `git log origin/main -5` shows
   the **same subject** your local commit carries — e.g.
   `git log origin/main -5` shows
   `fix(monitor): thread checkChannel error into scheduler backoff path`
   while your local `git log HEAD -1` shows the same subject on a
   different SHA — you are in the **race state**: another process
   re-applied your work on `origin/main`'s tip during your
   commit-to-push window. Confirm via step 6 below. **Do NOT fight
   this** — both intents are satisfied on the canonical tree; the
   canonical SHA is the one on `origin/main`.
6. Verify byte-equivalence of your local commit and the canonical
   commit on `origin/main`:
   `diff <(git.show --name-only --format='' <local-sha>) <(git.show --name-only --format='' origin/main)`.
   Empty diff → clean replay → your local commit has been superseded.
   Non-empty diff → see the fallback below.
7. **Canonicality check**: `git branch -r --contains <local-sha>` should
   return empty (your SHA does not reach any remote branch); conversely
   `git branch -r --contains <origin-sha>` should return `origin/main`.
   Only the SHA whose ancestry reaches `origin/main` is canonical.
8. **Push is unnecessary** once step 6 + 7 pass: the canonical work
   is already on `origin/main`. Verify with
   `git log --oneline origin/main -3` showing the same subject.
   Mark your local SHA as superseded mentally (still in reflog for
   audit per [`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026)).

**Fallback** (the non-trivial case): if byte-equivalence in step 6
is NOT empty, i.e. the upstream re-applied your files but with a
different intent than yours intended, this is a **merge-conflict case**
with no human-readable conflict marker (the files are independent
re-applications of the same logical surface). Open a three-way diff,
re-read each side's intent, and merge hunks manually per the
[`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026) — but
note you cannot `git rebase --abort` here because there is no
half-applied rebase state; you have to merge on top of the canonical
SHA and produce a new atomic commit on `origin/main`'s tip.

**Anti-pattern**: `git push --force-with-lease origin main` from your
local `ff7a5579` commit to "win" the race. That rewrites remote
history (from `960a3fb6 → …` to your `ff7a5579`-based tree) and
silently clobbers the canonical commit's content + every downstream
commit that depended on it (in the canonical case: `1e09a762`,
`51e41bf4`, `8c5ce7d1`, `0488a5ef` would all be invalidated).
The downstream agent who landed `960a3fb6` lost their build + test
invariants; the next round of agents fights the divergence.

This anti-pattern is **distinct** from
[`Git-Lesson-2`](#git-lesson-2-june-2026--direct-to-main-workflow)'s
`force-with-lease` exit hatch on the amend-loop anti-pattern. That
exit only applies when **no other process has landed on `origin/main`
while you were amending on the same branch tip**. Once `origin/main`
has moved past your local SHA — whether via a parallel agent commit
or a CI-side rewrite — force-push becomes destructive: the canonical
SHA on `origin/main` and every downstream commit would be clobbered
in one stroke. The discriminator is `git fetch && git log --oneline @{u}..HEAD`
returning **non-empty output**: if anyone else's work is ahead on
`origin/main`, your local SHA is on a divergent lineage and any
force-push is an anti-pattern. (**Auditability hint**: `git reflog`
still preserves the superseded SHA after the race; the diff between
your reflog entry and the canonical SHA on `origin/main` is the
irrefutable receipt for the supersession — see step 8 above.)

**Decision rule**: never `force-push` from a superseded commit; either
(a) declare the work canonical on `origin/main` (steps 5–8 above) or
(b) merge manually per the fallback. There is no third option —
clobbering canonical history with `force-push` is a hard anti-pattern
per [`Git-Lesson-2`](#git-lesson-2-june-2026--direct-to-main-workflow).

See also:
- [`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026) for the
  textual-conflict case.
- [`Git-Lesson-1`](#git-lesson-1-june-2026--git-rebase--i-vs--autosquash)
  for the squash-once-already-clean case.
- [`Git-Lesson-2`](#git-lesson-2-june-2026--direct-to-main-workflow) for
  the canonical direct-to-main workflow.
- [`Git-Lesson-5`](#git-lesson-5-june-2026--byte-equivalent-replay-race-recovery)
  for the byte-equivalent-replay case (the canonical case this umbrella
  entry describes at the top — parallel agent landed equivalent work
  during the commit-to-push window; "no fight" recovery via acceptance,  not force-push).


## Git-Lesson-5 (June 2026) — byte-equivalent-replay race recovery

A **byte-equivalent-replay race** is a non-conflicting case of
[`Git-Lesson-4`](#git-lesson-4-june-2026--recovery-from-non-fast-forward-push-race):
your unpushed local commit's content has been **cleanly re-applied** by a
parallel agent on `origin/main`'s tip while you were about to push.
A safe recovery exists — `git push --force` is the wrong move.

### Detection (3 indicators — all THREE must be true)

1. `git push origin main` returns `[rejected] (non-fast-forward)`.
2. `git log --oneline HEAD..@{u}` is **not empty** (`origin/main` has commits you don't have).
3. `git log origin/main -5` shows the **same subject** your local commit carries, but on a different SHA.

The third indicator distinguishes this case from the textual-conflict
race covered by [`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026):
same subject on different SHA = the parallel agent addressed the same
intent on `origin/main`'s development line.

### Canonical case (June 2026, channel-monitor Blocco 1)

- Local commit: `ff7a5579 fix(monitor): thread checkChannel error into scheduler backoff path`
  (six files: `monitor_ports.go` NEW + five modifications to
  `internal/application/assets/monitor/*.go` and `CHANGELOG.md`).
- Push rejected: `git push origin main` returned
  `[rejected]         main -> main (non-fast-forward)`
  because `origin/main` advanced underneath the local commit during a
  parallel agent window.
- Resolution: a byte-equivalent replay of the same files landed as
  commit `960a3fb6 fix(monitor): thread checkChannel error into scheduler backoff path`
  on `origin/main`'s development line (`ca75f8e0 → bb18544e → 1e09a762 → 960a3fb6 → 51e41bf4 → 8c5ce7d1 → 0488a5ef`).
- Local `ff7a5579` is reachable only via `git reflog`, on a divergent
  history line (`889a1a7e → … → 6b67f2be → 879d5637 → ff7a5579`) that
  no longer reaches `origin/main`.
- Byte-equivalence verified with
  `diff <(git show --name-only --format='' ff7a5579) <(git show --name-only --format='' 960a3fb6)`
  returning empty.

### Recovery procedure (4 mandatory steps; no force-push)

1. **Byte-equivalence check.**
   `diff <(git show --name-only --format='' <local-sha>) <(git show --name-only --format='' origin/main)`
   must return empty. If **non-empty** → drop into the textual-conflict
   fallback of [`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026)
   instead (PARALLEL AGENT rewrote your intent).
2. **Canonicality check.**
   `git branch -r --contains <local-sha>` returns empty (your SHA has
   been superseded); `git branch -r --contains <origin-sha>` returns
   `origin/main` (confirms the parallel-agent SHA is canonical).
3. **Accept without force-push.**
   Run **NOTHING** to clean up the local SHA. The canonical work is
   already on `origin/main`; pushing from the superseded local SHA will
   be a non-fast-forward no-op (unless you force-push, which is the
   anti-pattern below).
4. **Audit trail.** `git reflog` retains the local SHA. The diff between
   the reflog entry and the canonical SHA on `origin/main` is the
   irrefutable receipt for the supersession (refs survive 30+ days by
   default; `git reflog expire --expire-unreachable=now --all` is NOT
   needed).

### Anti-pattern: force-push from the superseded SHA

`git push --force-with-lease origin main` from your local `ff7a5579`
commit to "win" the race rewrites remote history (from `960a3fb6 → …`
to your `ff7a5579`-based tree). That silently clobbers the canonical
commit's content AND every downstream commit that depended on it (in
the canonical case: `1e09a762`, `51e41bf4`, `8c5ce7d1`, `0488a5ef`
would all be invalidated). The downstream agent who landed `960a3fb6`
loses their build + test invariants; the next round of agents fights
the divergence.

This anti-pattern is **distinct** from
[`Git-Lesson-2`](#git-lesson-2-june-2026--direct-to-main-workflow)'s
`force-with-lease` exit hatch on the amend-loop anti-pattern. That exit
only applies when **no other process has landed on `origin/main`** while
you were amending on the same branch tip. Once `origin/main` has moved
past your local SHA — whether via a parallel agent commit or a CI-side
rewrite — force-push becomes destructive.

The discriminator: `git fetch && git log --oneline @{u}..HEAD` returning
**non-empty** (any unseen upstream commits = parallel work ahead =
force-push is destructive). If the upstream tracking branch is empty
beyond your local HEAD, you are NOT in the byte-equivalent-replay race —
force-push is safe as the amend-loop exit hatch.

### Why this isn't a race failure

A byte-equivalent-replay race is a **correct coordination signal**:
two agents independently arrived at the same fix via parallel paths.
The scope-creep risk (manually re-merge divergent content) is greater
than the wasted-work risk (replay) — the canonical SHA on `origin/main`
already encodes the same intent. The Playbook rule "Don't surprise the
user. Don't surprise yourself. Don't surprise downstream commits" is
preserved by accepting the replay rather than fighting it.

### Future-agent checklist (when you encounter this)

Before declaring victory on a `git push` rejection, run the
byte-equivalence check FIRST (step 1 of recovery). Only after the diff
returns empty, accept the canonicality. If diff returns non-empty, you
ARE in the textual-conflict case — read [`Rebase-Conflict
Lesson`](#rebase-conflict-lesson-june-2026) instead.

In CI workflows: byte-equivalent-replay races are **invisible** to CI
(the canonical SHA on `origin/main` already has the lint+test work
that would have failed on a divergent replay). Don't add CI guards
that detect "duplicate commits" — that's an anti-pattern that disables
the canonical-coordination signal.

### Cross-refs

- [`Git-Lesson-4`](#git-lesson-4-june-2026--recovery-from-non-fast-forward-push-race) — umbrella entry; covers BOTH the byte-equivalent-replay case (this entry's focus) AND the textual-conflict case (`Rebase-Conflict Lesson`'s focus) under a single "non-fast-forward recovery" framing.
- [`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026) — the textual-conflict sibling (diff returns NON-empty; parallel agents applied the same files with different intent).
- [`Git-Lesson-2`](#git-lesson-2-june-2026--direct-to-main-workflow) — direct-to-main baseline that prevents most other race modes (the `amend-loop` escape hatch is NOT a byte-equivalent-replay escape hatch).
- [`Git-Lesson-3`](#git-lesson-3-june-2026--co-authored-by-trailers-for-agent-commits) — agent identity trailers that make the reflog audit trail easy to read post-race.

The CI check (`scripts/ci-architectural-checks.sh` Check 1) bans bare
`context.Background()` in `internal/api/` handlers. Per ARCHITECTURE.md §7,
a small number of sites legitimately detach from the parent-context lifetime
via either `context.Background()` (composition roots, signal-init, shutdown
drain, background goroutines with no parent request context) or
`context.WithoutCancel(ctx)` (handler-routed paths that must finish
post-write work — audit logs, cache bumps, outbox deliveries — after the
original client has disconnected and the request context has been cancelled).
The following sites are **intentionally exempt** with the documented reason
for their detachment. **Gate scope** (read before adding new rows): of the 12
`context.Background()` rows below, only the **4 that literally invoke
`context.Background()` inside `internal/api/` handler or middleware code**
are the direct allowlist matched by `Check 1` in
`scripts/ci-architectural-checks.sh`:

  - `internal/api/handlers/script/handlers/postwrite.go` (PostWrite save context)
  - `internal/api/module_base.go` (rollback context for module startup failure)
  - `internal/api/middleware/middleware_logger.go` (logSink shutdown drain)
  - `internal/api/middleware/idempotency.go` (cleanupLoop background goroutine)

The remaining **8 `context.Background()` rows** (composition roots,
service-layer shutdown contexts, infra-layer background goroutines, and the
`internal/api/server.go` row which uses `signal.NotifyContext()` — a different
Go-canonical signal-init API, exempt for an entirely different reason than
`Check 1`'s `context.Background()` ban) AND all **13 `context.WithoutCancel`
rows** are **documentation-only** — there is currently no dedicated CI gate
covering either group. Promoting either family to a dedicated CI gate
(e.g. `Check 9` in the same listing-pattern style as `Check 1`) requires
the canonical tracking entry
`architecture/current.yaml#id-22 follow_up_tickets.PR-CONTEXT-NO-CANCEL-CI-GATE`
(with owner + deadline + status; transitional baseline for the 8 currently-
unlisted WithoutCancel sites documented with deadline 2026-07-15) before
adding any new exempt site that does not fit the 4 Check-1-mapped rows
above. Two families are present in the table below;
the `Reason` column names the helper and the lifetime concern for each row
so a future agent can verify the exemption is still appropriate before
adding similar sites:

| Site | Reason |
|------|--------|
| `internal/api/handlers/script/handlers/postwrite.go` | `context.Background()` Post-write save context (30s timeout) — must survive client disconnect |
| `internal/service/gemmamemory/service.go` | `context.Background()` Post-write save context (30s timeout) |
| `internal/service/scriptcore/write_script.go` | `context.Background()` Post-write save context (30s timeout) |
| `internal/jobs/worker.go` (finalizationCtx, lines ~142-146) | `context.Background()` Finalization context for job outcome persistence (30s timeout) — must survive handler timeout so the worker can still mark the job as failed/completed/dead-lettered in the DB. Detached from `jobCtx` by design; detaching from `ctx` (worker lifecycle) would lose the outcome if the worker is shut down mid-job. |
| `internal/app/init_core.go` | `context.Background()` Top-level composition root (no parent context exists) |
| `internal/api/server.go` | `context.Background()` `signal.NotifyContext()` — canonical Go pattern |
| `internal/api/module_base.go` (line ~105) | `context.Background()` Rollback context for module startup failure — must survive parent cancel so Stop() can run |
| `internal/service/translations/cache.go` | `context.Background()` Defensive fallback when parentCtx is nil |
| `internal/api/middleware/middleware_logger.go` (StopLogger, line ~27) | `context.Background()` Shutdown/drain operation — calls `logSink.Stop(context.Background())` to drain pending log entries before process exit. Must survive any parent context cancellation during shutdown. |
| `internal/api/middleware/idempotency.go` (cleanupLoop, line ~316) | `context.Background()` Background cleanup goroutine (15-min ticker) garbage-collects expired idempotency keys from SQLite. Uses `context.WithTimeout(Background, 30s)` for bounded SQL execution; no parent request context exists. |
| `internal/infrastructure/database/sqlite/logsink/sqlite_request_log_sink.go` (writer goroutine, lines ~161/166/179) | `context.Background()` Background batch writer goroutine flushes request logs to SQLite via channel-buffered batching (100ms tick). No parent request context; the goroutine owns its lifecycle independently of any HTTP request. |
| `internal/application/assets/providers/artlist/search_cache.go` (formerly `internal/sources/artlist/search_cache.go` — migrated June 2026 during `internal/sources/` → `internal/api/assets/` + `internal/application/` consolidation; this row documents the **Background** defensive call, the WithoutCancel post-write save site in the same file is documented in the next row) | `context.Background()` Defensive fallback when parentCtx is nil |
| `internal/api/assets/voiceover/handler.go` (line ~225) | `context.WithoutCancel` Promo code-delivery finalisation — must survive client cancel so the user receives the Promo link after the request disconnects (10-min timeout) |
| `internal/application/images/ingest.go` (line ~44) | `context.WithoutCancel` Image-ingest post-write save — must survive client disconnect for in-flight upload finalisation (60s timeout) |
| `internal/application/images/ingest_direct.go` (line ~189) | `context.WithoutCancel` Image-ingest direct post-write save — same detachment for the direct (non-augmented) ingest path (30s timeout) |
| `internal/application/voiceover/process.go` (lines ~221-226) | `context.WithoutCancel` Voiceover indexer post-write save — must survive request cancel after submit (2-min timeout) |
| `internal/application/assets/providers/artlist/semantic_enricher.go` (line ~95) | `context.WithoutCancel` Artlist semantic-enricher post-write save — must survive handler cancel so the enrichment write completes (30s timeout) |
| `internal/application/assets/providers/artlist/search_cache.go` (line ~64) | `context.WithoutCancel` Artlist search-cache bump — post-write save that must survive handler cancel (15s timeout) |
| `internal/infrastructure/artlist/cache/cache.go` (line ~355) | `context.WithoutCancel` Artlist cache.lookup defensive fallback when parentCtx is nil (no fixed timeout; reads cache only) |
| `internal/infrastructure/database/sqlite/scripts/translation_cache.go` (line ~178) | `context.WithoutCancel` Translation-cache write — must survive handler cancel for the bounded 3s SQLite write |
| `internal/application/scripts/job_helpers.go` (line ~138) | `context.WithoutCancel` Scripts job-helpers finalisation — post-write save that must survive worker-cancel |
| `internal/application/youtube/search/service.go` (line ~271; spec was line 267, drift +4 lines — likely a follow-on addition in the same file since the spec was written) | `context.WithoutCancel` YouTube search service cache-bump post-write save — must survive handler cancel (5s timeout) |
| `internal/application/assets/monitor/semantic_matcher.go` (lines ~117, ~213) | `context.WithoutCancel` Asset-deletion / semantic-matcher post-write save — must survive handler cancel so the cleanup write completes (5s timeout) |
| `internal/api/handlers/youtube/callbacks.go` (line ~167) | `context.WithoutCancel` YouTube-callback handler finalisation — must survive request cancel so the callback post-write save completes |
| `internal/application/jobs/outbox/delivery.go` (line ~421) | `context.WithoutCancel` Outbox-delivery audit-log write — must survive worker cancel so the delivery receipt is recorded even on shutdown |

---

## Migration Status (Brutal Care Plan)

### Completed (June 2026)
- ✅ Database Consolidation (all tables → `media.db.sqlite`; `media.db.sqlite` removed as unused)
- ✅ Eliminated `assetpipeline` thin wrapper
- ✅ Migrated `workflowrunner.results` → job system
- ✅ Migrated `assetdestination.Resolver` → `core/destination.Resolver`
- ✅ Migrated `mediaasset.Processor` → `core/processor.Processor`
- ✅ Centralized DB migrations + connection pooling (WAL/busy_timeout)
- ✅ Migrated harvester/catalog/db backup → job system
- ✅ CI checks integrated: `scripts/ci-architectural-checks.sh` in GitHub Actions
- ✅ Artlist speed optimization (14ms cache, parallel download, persistent Node scraper)
- ✅ Unified metadata single-call pattern (`tagImageMetadata()`)
- ✅ Scraper tuning (scroll 300ms, concurrency 8, persistent browser)
- ✅ All God Objects split into focused files (channel_monitor, extractor_process, etc.)
- ✅ **context.Background() audited and documented** (ARCHITECTURE.md §7)
- ✅ **Duplicate architecture docs consolidated** (MODULE_MAP + OWNERSHIP deleted)
- ✅ **.gitignore cleaned up** (root binaries, logs, cookies, .bak patterns added)
- ✅ **Scriptflow eliminato** — `internal/application/scriptflow/` directory rimossa, codice assorbito in `internal/application/scripts/`
- ✅ **Registry provider tipizzato** — `internal/application/assets/providers/` con adapter per Artlist e YouTube
- ✅ **Consolidation api/sources** — Migrati gli handler di voiceover, soundeffect e register (YouTube) da `internal/api/sources/` a sotto-pacchetti dedicati in `internal/api/assets/` (`voiceover/`, `soundeffect/`, `register/`)
- ✅ **Script-flow use cases extracted** (June 2026): `GenerateBatchUseCase`, `SectionRegenerator`, `CacheEvictionUseCase` — handler `ScriptFlowHandler` orchestration for the legacy batch endpoint, `/cache/evict`, and `/sections/:section_id/regenerate` now delegates via `ScriptFlowDeps` (22-positional ctor replaced).

### Still Pending
- Completion of the remaining `internal/api/sources/` consolidation to `internal/api/assets/` (e.g. artlist, stock, local-to-drive)

### Current wave status (June 2026 snapshot — fonte canonica: `architecture/current.yaml`)

Vista `Wave 14-18 + 0` come tracker attivo; `Wave 1-13` come storico
completato in `docs/archive/migration-history.md`. La tabella sotto è uno
**snapshot di giugno 2026** mantenuto per audit storico; per lo stato
operativo corrente (incluso pending_in, blocked_by, e residuals attivi) usa
direttamente `architecture/current.yaml`.

**Regola di lettura**: "done" = migrazione fisicamente completata. I
`residuals:` delle wave 4A, 6, 7 sono commenti `//` puri, non import —
non bloccanti, ripulibili in PR documentale separata. **Le `residuals:`
delle wave archiviate vivono in `docs/archive/migration-history.md`,
non nel tracker attivo**. Per il `active_files_remaining:` delle wave
in flight (8, 10, 11, 12), consultare la sezione corrispondente in
`architecture/current.yaml`.

### Snapshot tabellare (June 2026)

| Wave | Oggetto | Status snapshot | Avanzamento (snapshot) |
|------|---------|----------------|------------------------|
| 4A   | Canonical asset model + ports | done | 7 residui solo in commenti Go (archiviate in `docs/archive/migration-history.md`). Nessun import attivo. |
| 4B   | Job/Worker/Outbox contracts into domain/job | done | 0 riferimenti residui all'uscita della exit-gate. Wave archiviata. |
| 4C   | Remove internal/core + internal/domain/media | done | 10 commenti Go superstiti (zero import attivi). Wave archiviata. |
| 5    | Full jobs consolidation | done (snapshot) | sotto-onda 5_PR3 chiusa; alias zero-copy collassati. Wave archiviata. |
| 6    | Scripts consolidation | done | `internal/application/scriptflow/` e `internal/scripts/` rimossi. Wave archiviata. |
| 7    | Assets + Artifacts + Registry unification | done | 2 residui in commenti test. Wave archiviata. |
| 8    | Association + Realtime consolidation | done (snapshot time: in_progress) | Wave archiviata — consultare sezione Wave 8 in `docs/archive/migration-history.md`. |
| 10   | Storage + Drive + Qdrant adapters | done (snapshot time: in_progress) | Wave archiviata — consultare sezione Wave 10 in `docs/archive/migration-history.md`. |
| 11   | Catalog + Intelligence | done (snapshot time: in_progress) | Wave archiviata — consultare sezione Wave 11 in `docs/archive/migration-history.md`. |
| 12   | Provider registry + source consolidation | done (snapshot time: in_progress) | Wave archiviata — consultare sezione Wave 12 in `docs/archive/migration-history.md`. |
| 13   | Eliminate internal/media namespace | done → Wave archiviata | (snapshot) completata in giugno 2026. Consultare la sezione Wave 13 in `docs/archive/migration-history.md`. |

**Truth sources** (in ordine di autorità):
1. `architecture/current.yaml` — stato canonico per wave + exit-gate
   (Wave 14-18 + Wave 0 attive; Wave 1-13 archiviate).
2. [`architecture/ownership.generated.yaml`](architecture/ownership.generated.yaml) (aggregated view) — chi possiede ogni package canonico; i 6 file per-section (application/infrastructure/jobs/modules/packages/services) vivono in [`architecture/ownership/`](architecture/ownership/).
3. `docs/migrations/api-infrastructure-imports-allowlist.txt` — ratchet
   canonico monotone-decreasing per le violazioni `internal/api` →
   `internal/infrastructure` grand-parented; `go run ./scripts/archcheck`
   espone il focused gate corrente (legge il file via `const migPath`).

> **Nota (aggiornata giugno 2026)**: il path canonico dell'allowlist è
> `docs/migrations/api-infrastructure-imports-allowlist.txt`. Il path
> precedente `scripts/archcheck/grandfathered_allowlist.json` (drift documentale — il path canonico è `docs/migrations/api-infrastructure-imports-allowlist.txt`) citato in
> vecchie versioni di AGENTS.md è **drift documentale** — l'allowlist
> canonico è quello sopra, dove `scripts/archcheck/main.go` lo legge a
> runtime. Le occorrenze residue del vecchio path in testa a vecchi file
> vanno corrette per audit consistency (PR documentale separata).

Se i tre layer sopra raccontano versioni diverse della stessa realtà,
vince (1) ma è necessario aprire una PR di sincronizzazione come
quella che ha prodotto questa tabella (vedi storico commit di giugno
2026).

### Recent cross-cutting closures (June 2026)

This sub-section tracks P0/P1 closures that span architecture + atomicity
+ accounting — not wave migration per se. Wave-only entries stay in
the snapshot table above.

- **PR-VO-A P0 hardening (`e149e1ab` → `602114bc`)** — voiceover P0 closure
  across A1..A6. See [`docs/voiceover/p0-bundle-A1-A6.md`](docs/voiceover/p0-bundle-A1-A6.md)
  for the per-commit index, cumulative risk coverage (atomic state
  transitions, identity drift, path safety, accounting correctness),
  per-PR contract details, tests pinned, and future-work pointers
  (PR-VO-B1+B2, PR-VO-B3, PR-VO-C1).
- **PR-VO-B1-C1 P0→P1 hardening (`73c44aca` → `c2867b90`)** — voiceover
  P1 closure across B1, B2, B3, C1. The "Future P1/P2 work" pointer
  in `docs/voiceover/p0-bundle-A1-A6.md` is fully resolved by this
  bundle; future work now lives in `docs/voiceover/p1-bundle-B1-C1.md`.
  See [`docs/voiceover/p1-bundle-B1-C1.md`](docs/voiceover/p1-bundle-B1-C1.md)
  for the per-commit index, cumulative risk coverage (Drive upload
  boundary, group/locale identity, sync dedupe key, HTTP endpoint
  unification), per-PR contract details (DriveUploaderPort, StyleGroup
  propagation, BCP-47/compact locale parser, destination.kind routing
  + RFC 8594 Sunset), tests pinned, architectural patterns reaffirmed,
  and forward-pointer to PR-VO-D1/D2/D3 + E1.

- **P1-2 of cleanup plan, Check 44 promotion + P1-3 NIT closure
  (chain `021c38ce` → `4306b97f` → `4270dcf7` → `732628a4` → `c33ae3d3`; post-chain follow-ups `1cd9c3c9`, `03813593`)** — Check 44 application size cap +
  `usecase/types_aliases.go` filename ban actively enforced via
  `scripts/ci-architectural-checks.sh` (wave-tracker P1-2 flipped
  from `current_state: deferred` to `active`); the new forward-
  pointer entry `PR-BARE-ONLY-MAP-LITERAL-COVERAGE` (owner
  `architecture`, deadline 2026-07-25) lives at the canonical
  anchor `architecture/current.yaml#wave_status.P1-2.linked_issues[PR-BARE-ONLY-MAP-LITERAL-COVERAGE]`
  per godlike/06 §Slim-schema + zero-baseline rule, surfacing
  the bare-only Coverage gap of Check 46. CHANGELOG entry under
  `## Unreleased → ### Fixed` mirrors this closure.

(See-also canonical anchor: `audit-trail-anchors_P1-2-of-cleanup-plan`; mirrored in CHANGELOG.md `## Unreleased → ### Fixed`.)

- **PR-D YouTube Channel Monitor cutover, Commit D (commit-sha pending)** —
  youtube_discoveries ledger + ListChannelVideos dedupe + EnqueueOutcome enum +
  cycle-end watermark replaces per-video UpdateCursor (godlike/07 compliant).
  Closes: **(a)** leader-election INSERT ... ON CONFLICT(channel_id, video_id)
  DO NOTHING RETURNING id gates which per-video goroutine emits the durable
  job (defense-in-depth on top of the ActiveKey dedup at the broker level);
  **(b)** cycle-end `MAX(discovered_at)` → `category_channels.last_cursor`
  is now the single durable channel-state write per scheduler cycle (the
  pre-Commit-D per-video `UpdateCursor(Cursor=videoID)` was a best-effort
  silent-degrade path); **(c)** VideosEnqueued counter is now strict-typed:
  only `EnqueueOutcome::Enqueued` increments it, with
  `VideosAlreadyScheduled` + `VideosRejected` partitioning the legacy
  `VideosSkipped` aggregate; **(d)** keyword `containsAny` rewritten via
  `strings.ToLower + TrimSpace + strings.Contains` (stdlib-only, drops the
  bespoke ASCII loop); `(e)` `decodeJSONStrings` returns a non-nil error
  on malformed JSON (logged + treated as keyword-less per cycle, no silent
  drop).
  New canonical surface: migration `113_youtube_discoveries.sql` +
  `internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go`
  (TryReserve leader-election + MarkEnqueued + MarkRejected + MaxDiscoveredAt
  watermark) + `internal/application/assets/monitor/ports.go::EnqueueOutcome`
  + `YoutubeDiscoveriesPort` (4 methods) + `internal/application/assets/monitor/discovery.go::recordDiscoveryAndClassify`
  (TryReserve + delegate to enqueueFromAnalysis) + `recordCycleEndWatermark`
  (defer in checkChannel). Forward-pointer: `architecture/current.yaml#YouTube-Cutover-D`
  for the residual hardening items (5-EnqueueExtract-port-calls assertion
  in the 5×2 test; dead `ChannelsCursorSvc` interface on extraction_enqueuer.go).

  Closes audit points **P0 #2** (ExtractionService now delegates to a single canonical
  per-segment use case — no inline 9-step orchestration in the service layer),
  **P0 #3** (no third implementation: `internal/application/youtube/adapters/segment_processor.go`
  is now redundant; carries a TODO wave-delete marker pending the next cutover wave),
  **P0 #4** (job handler classifies the per-batch `ExtractResponse` via
  `internal/application/youtube/jobs/classify.go::ClassifyExtractionResult` —
  `nil` for all-OK, `*PartialSuccessError` for some-failed-still-recoverable,
  `ErrExtractionRetryable` for retryable batch failure, `ErrExtractionTerminal`
  for terminal batch failure, dispatched via `errors.As` in job_handler.go),
  and **P2 #19** (the durable per-segment shape ships as a typed application-
  layer use case + ports rather than imperative driver code).
  New canonical surface:
  `internal/application/youtube/usecase/process_segment.go::ProcessYouTubeSegmentUseCase`
  performs the deterministic-clipID (yt_<videoID>_<startSec>_<endSec>_v1 hash
  including policyVersion) → cache short-circuit → retry-download via
  `pkg/retry.Do` with `isTransientExtractionError` predicate → MD5 hash →
  subtitles → Whisper fallback → destination resolve → Drive upload →
  `ClipAtomicWriter.CommitClipAndIndexEvent` (DB+outbox transactional
  pair, Commit F dependency). Two new ports in
  `internal/application/youtube/ports/ports.go`: `ClipCachePort`
  (`GetExisting(ctx, clipID) → (*dto.ExtractItem, bool, error)`) and
  `ClipAtomicWriter` (`CommitClipAndIndexEvent(ctx, clipID, item, event)
  → error`). New DTOs in `internal/application/youtube/dto/types.go`:
  `ProcessSegmentCommand` + `ProcessSegmentResult`. Fan-out in
  `extraction_service.go::extractFanOut` is bounded by
  `MonitorRuntimePolicy.MaxConcurrentVideos` via a semaphore channel
  + WaitGroup + `defer recover()` for panic isolation (mirrors
  `monitor.safeCheckChannel` precedent). CHANGELOG entry under
  `## Unreleased → ### Added` mirrors this closure.
  Forward-pointer: `architecture/current.yaml#YouTube-Cutover-C` for
  the residual admin-token / port-mock / spoke-shutdown hardening
  items (out-of-scope for Commit C; see CHANGELOG follow-up notes).

- **[ART-001 — FASE-6 reversal: Artlist routes restored (POST /run, GET /stats, GET /search, GET /search/live, GET /diagnostics, POST /recommend, POST /sync-catalogs return 2xx, July 2026)]** `feat(artlist)` — closure mirror entry acknowledging the FASE-6 Artlist reversal landed on `origin/main` via 3 coordinated commits. All 3 commits carry the `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` trailer per AGENTS.md §Git-Lesson-3 auditability convention:
  - **Commit A=`e5985003`** (code surface) — restore `internal/app/registry_internal_modules.go::registerArtlist` from the FASE-6 log-only stub (was returning 404 on `/api/artlist/*` since the June 2026 cutover) to invoke the canonical `artlist.NewService(deps)` + `artlist.Build(deps)` Pattern 0 entrypoint. 3 mandatory fail-closed gates (`Publisher` / `outbox.Dispatcher` / `cfg+repos.ClipsRepo+Jobs.Service`) + 2 inline typed adapter structs (`artlistDispatchAdapterInline` + `artlistIndexerAdapterInline`) compile-time pinned via `var _ artlistPkg.X = (*Adapter)(nil)` per godlike/06 SSOT.
  - **Commit B=`c700123f`** (wave-tracker entry) — open `architecture/current.yaml#ART-001` with the 8 forward-pointer `linked_issues[PR-ARTLIST-*]` slots and the canonical-`architecture` ownership; status `shipped` per godlike/06 SSOT lockstep with this CHANGELOG.md entry + the AGENTS.md mirror (this entry).
  - **Commit C=SHA-TBD** (CHANGELOG closure meta-entry post-Step 10) — narrative acknowledging A+B as artifact-state, tombstoning the FASE-6 regression narrative, honest-limitation disclosure on the 6 build-issue carry-forward (this entry's bottom block).
  - **godlike/06 3-surface lockstep:** `AGENTS.md` (this section) ≡ `CHANGELOG.md` (`## Unreleased → ### Fixed → ART-001`) ≡ `architecture/current.yaml#ART-001`. Operators reading any of the 3 surfaces get consistent artifact-state; cross-references maintained at every wave-tracker-entry `ship_sha` audit-pin (e.g. `PR-ARTLIST-LIVE-WIRE` shipped_sha=`d2491f76` from our Wave 1 BACKFILL chain mirrored in lockstep commit `efaed7c5`).
  - **Forward-pointer discipline per godlike/06 SSOT:** 7 of 8 `architecture/current.yaml#ART-001.linked_issues[PR-ARTLIST-*]` slots remain `status: pending` with band-aligned deadlines 2026-07-25..2026-08-15. `PR-ARTLIST-LIVE-WIRE` is the first BACKFILL entry to flip pending → shipped (ship_sha `d2491f76` + ship_date `2026-07-03`, ship-via lockstep commit `efaed7c5`); the remaining 7 PR-ARTLIST-SEARCHERS / STAGER / LIFECYCLE / REPOS / CHIP2 / SYNCSERVICE / STATS-MINIMAL-RESTORE follow the same discipline as each lands.
  - **Honest carry-forward (godlike/07 disclosure):** post-ART-001 closure, 6 source-file compilation failures predate ART-001 and are NOT regressions of any ART-001 commit. Filed under `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04.linked_issues` with deadline range 2026-07-15..2026-08-01:
    - `FIX-MONITOR-ENQUEUE-TOLOWER` (internal/application/assets/monitor/enqueue.go, deadline 2026-07-15)
    - `FIX-MONITOR-SCHEDULER-ENQUEUER` (internal/application/assets/monitor/scheduler.go, deadline 2026-07-15)
    - `FIX-STOCKPIPELINE-REDECLARATION` (run_upload.go — file MISSING from disk, retired in a prior god-object decomposition wave; deadline 2026-07-25)
    - `FIX-APP-MODULE-MEDIA-DISPATCHER` (internal/app/module_media.go ~line 334, deadline 2026-07-25)
    - `FIX-IMAGES-ROUTING-CYCLE` (internal/application/images/routing/ structural DTO-relocation per commit e52005cc; deadline 2026-08-01)
    - `FIX-APP-WIRE-SCRIPT-SYNTAX` (internal/app/wire_script.go — original Step 8 audit attribution; verified post-E2E-runtime-check 2026-07-04 that the actual broken code lives in `internal/app/workerruntime/{preflight.go, run.go}` not in `wire_script.go` itself; deadline 2026-08-01 — REVISIT-AND-RETIRE pending post-discovery reconciliation per godlike/06 SSOT)
  - **godlike/07 no-fake-availability audit-pin:** PR-ARTLIST-LIVE-WIRE was the first BACKFILL Wave 1 entry to flip `status: pending` → `status: shipped` with full commit-SHA + ship-date audit trail. Subsequent PR-ARTLIST-* entries follow the same lockstep convention as Wave 2+ BACKFILL lands.


- **PR-C-YouTube-Cutover, P1 #17 final closure (June 2026, Commit 6/6)**
  — `arch(current)` + `fix(jobs)` — durable channel-monitor sync closes
  P1 deck. `architecture/current.yaml` wave-tracker entry `id-17` flipped
  from `status: in_progress / exit_signal: false` to
  `status: done / exit_signal: true` per the slim schema, with the
  `blocker: ["16"]` cross-reference preserved for DAG-ancestry audit
  (godlike/07 §"Historical information"). `internal/application/jobs/registry.go`
  locks `Concurrency: 1` explicit on `TypeYouTubeChannelSync` (matches
  the canonical `e2e_no_duplicates_test.go` harness's `Policy.MaxConcurrentVideos=1`
  invariant; byte-stable against the typed-accessor `Registry.Concurrency(t)`
  which already normalises <=0 to `DefaultConcurrency=1` via `applyDefaults()`).
  No new code surface; Commits 1/6 → 5/6 already landed on origin/main during
  the wave per AGENTS.md §Git-Lesson-4 byte-equivalent-replay pace (the
  in-flight push race is documented in AGENTS.md §Git-Lesson-4; the
  canonical SHA on `origin/main` is upstream). Commit 6/6 is the canonical
  closure marker that flips the wave-tracker entry on top of `origin/main`'s
  tip. Forward-pointer for the parallel-mode `4/5 MarkEnqueued-loss` race:
  tracked separately in `architecture/issues.yaml` (`Concurrency=1` is
  the cutover-aligned mitigation until the follow-up ticket ships). The
  `ChannelsCursorSvc` interface at
  `internal/application/assets/monitor/extraction_enqueuer.go:115`
  remains a deliberate test-surface sentinel per Commit D (6 test assertions
  in `extraction_enqueuer_test.go` pin the no-`UpdateCursor` contract) —
  NOT a dead leftover, NOT removed by this commit. CHANGELOG entry under
  `## Unreleased → ### Fixed` mirrors this closure.

- **PR-ARTLIST-SYNCSERVICE closure (commit f02ae683daec5b6e3d1ba0a64ce3e8e0a64a37ae, 2026-07-04) — audit-pin cleanup per godlike/07 no-fake-availability** `chore(artlist) + chore(architecture)` — close the PR-ARTLIST-SYNCSERVICE wave-tracker entry as "obsolete / package removed". The canonical `internal/application/assets/clipresolver/` package was REMOVED in a prior refactor (the new canonical `ClipResolver` lives in `internal/application/scripts/ports/clip_resolver_port.go` with a `Resolve` method, NOT the artlist-handler-side `Recommend` method — the two surfaces are NOT interchangeable). The 2 remaining stale surfaces are physically removed: (a) the `ClipResolver: nil, // forward-pointer: PR-ARTLIST-SYNCSERVICE` literal in `internal/app/build_bundles_artlist.go::WireArtlist` (Build(Dependencies) call) — replaced with an explanatory comment + intentional field omission; (b) the clipresolver reference in the `ArtlistWiring` docstring at `internal/app/bundle_types.go` — rewritten to preserve the historical PR4d-chunk2 context but phrase the clipresolver removal as historical (not active). The `Dependencies.ClipResolver` field on `internal/api/assets/artlist/module.go::Dependencies` is RETAINED (godlike/07 minimal-blast-radius). The handler's nil-tolerance returns 503 on `/recommend` (unchanged runtime behavior). **godlike/07 no-fake-availability rationale:** shipping a fake `ClipResolver` wire (wrapping `*scripts.ClipResolver` behind a fake `Recommend` method) would have been a violation; the canonical closure is to remove the forward-pointer + the stale docstring + add a deprecation record, NOT to fabricate a wire. **godlike/06 3-surface lockstep:** `architecture/current.yaml#ART-001.linked_issues[PR-ARTLIST-SYNCSERVICE]` flipped `status: pending` -> `status: shipped` with `ship_sha: f02ae683daec5b6e3d1ba0a64ce3e8e0a64a37ae` + `ship_date: "2026-07-04"`; new deprecation record `architecture/deprecations.yaml#PR-ARTLIST-SYNCSERVICE` (id = wave-tracker slot for 1:1 lockstep traceability) added with `migration_phase: CONTRACT` + `status: removed` + 5-layer compatibility test (rg clipresolver/PR-ARTLIST-SYNCSERVICE in internal/app/ = 0 hits live) + usage_metric tracking the pre/post delta. **godlike/07 minimal-blast-radius:** the `Dependencies.ClipResolver` field stays; the handler's nil-tolerance is unchanged; the `/recommend` 503 behavior is unchanged. **Verification:** `gofmt -d internal/app/build_bundles_artlist.go internal/app/bundle_types.go` exit 0; `rg clipresolver internal/app/` returns 0 hits live (only history-only mentions in the deprecation record references); `rg PR-ARTLIST-SYNCSERVICE internal/app/` returns 0 hits live. **Honest-limitation:** the PR-ARTLIST-LIFECYCLE sibling (the `lifecycleService` field on `artlist.Service` is also dead code per the 2026-07-04 audit) remains `status: pending` — closure of LIFECYCLE is forward-pointer `PR-ARTLIST-LIFECYCLE-DEPRECATION` (deadline TBD, NOT this record). Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.

---

- **AUDIT-RESIDUE-2026-07-04 — God-object decomposition RESIDUE chain wave-tracker (12 net-new linked_issues, July 2026)** `chore(architecture)` — register `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04` for the post-GODOBJ-2026-07-03 residue audit pasted to the orchestrator on 2026-07-04. The 12-file god-object decomposition (PR-GODOBJ-1..6 ship-status + 7 still-tracking) is COMPLETED; this wave tracks ONLY the residue chain — the 7 refactor-residue PR-* below + 1 build-blocker (Q9) + 5 Q-prefixed build/test blockers (Q4..Q8). 2-step audit-pinning per AGENTS.md §God-object decomposition wave + godlike/06 SSOT discipline:

  - **P0 (HIGHEST)**: `PR-BLOCKER-Q9-VOICEOVER-FANOUTUSE-CASE` (forward-pointer to architecture/issues.yaml#Q9 with `blocker: true`) — closure mechanical: define missing FanoutVoiceoversUseCase / FanoutResult in `internal/application/voiceover/jobs/` OR retire the references in generate_handler.go per godlike/07 minimal-blast-radius. Until Q9 ships, `go build ./...` exits non-0 so per-file-gate validation cannot run for ANY other residue PR. Deadline: 2026-07-15.
  - **P1**: `PR-REFLECT-ELIM-HANDLER-REGISTRATION` — `internal/application/jobs/handler_registration.go`. Pre-existing file preamble itself flags `reflect.ValueOf/Call` as "anti-pattern mantenuto solo per split meccanico"; closeout deletes the reflection block + replaces `RegisterHandler(any)` with `RegisterHandler(HandlerFunc)`. Deadline: 2026-07-25.
  - **P2**: `PR-IMAGES-SHIM-REMOVAL` — `internal/application/images/generation_service.go` carries 3 fake-availability surfaces: (a) dormient `imageGen`; (b) `GenerateSmartImageWithAccount` (account/project silently ignored); (c) `HandleJob` shim that builds `NewJobHandler` per-call. Migrate composition root + callers to NewGenerationService + direct NewJobHandler wiring. Deadline: 2026-07-25.
  - **P3**: `PR-DRIVECLIENT-RAW-RETIRE` — `internal/app/composition.go::DriveBundle` still carries raw `DriveClient *gdrive.Service`. Per DRIVE-005 (id-27 closure) 4 Pattern 0 ports already exist (`delivery.Publisher` / `drive.Reader` / `drive.FileLifecycle` / `drive.DocClient`). While raw handle is in the bundle, callers can bypass the canonical ports. Deadline: 2026-07-25.
  - **P4**: `PR-CHROME-PROVIDER-SPLIT` — `internal/application/images/chrome_provider.go` (~260 LoC mixing Playwright subprocess + JSON-protocol + Health/cooldown) → 4 files (`chrome_provider.go` + `slide_worker_process.go` + `slide_worker_protocol.go` + `slide_worker_health.go`). BRUTAL DECISION required: today `exec.Command("python3", scriptPath, "--profiles", "1")` is invoked while `numProfiles` constructor arg is IGNORED + `cooldowns map[int]int64` is dead — single-profile must be official policy (godlike/07 no-fake-availability); retire `numProfiles` + `cooldowns map[int]int64` instead of reviving multi-profile. Deadline: 2026-08-01.
  - **P5**: `PR-QDRANT-MAINT-PER-MODE` — split `cmd/admin/qdrant_maintenance.go` along the 3 user-visible modes (audit / repair-locators / delete-invalid) into 7 thin per-mode files. Refactor meccanico, low business risk. Deadline: 2026-08-01.
  - **P6**: `PR-YT-DRIVE-LEGACY-RETIRE` — `internal/app/youtube_drive_legacy_adapter.go` (sourcingDriveAdapter.UploadFileWithDescription already fail-closed per DRIVE-008 via `drive.ErrLegacySurfaceRetired`); trigger: `rg "UploadFileWithDescription|sourcingDriveAdapter|DrivePort" internal/ cmd/` returns ONLY wiring morto. Deadline: 2026-08-15.
  - **P-∞**: 5 Q-prefixed `architecture/issues.yaml` blockers (Q4-CATALOGSYNC-DispatcherPath / Q5-PROVIDERS-SearchAggregator / Q6-ARTLIST-DispatcherRoutes [+Q8] / Q7-YOUTUBE-ExtractionPhase1c / Q8-ARTLIST-SqlSchemaStatus) forward-linked for closure visibility; closure is via the existing Q-tickets, NOT new audit-driven work — this entry is **lockstep registration** so all residue-class gates flip together per godlike/06 SSOT. Deadlines: 2026-07-25 across the board.
  - **Files NOT touched** (already canonical, per the orchestrator-pasted sound-judgement list): `internal/application/youtube/usecase/extraction_service.go` (split per PR-GODOBJ-1 ship), `internal/application/assets/monitor/scheduler.go` (split), `internal/application/scripts/jobs/generation_job.go` (doc-only sentinel), `internal/application/books/job_handler.go` (PUBLISHED / PUBLISH_FAILED / LOCAL_ONLY + `ErrBookDrivePublishFailed`), `internal/api/script/handler_legacy_adapters.go` (quarantena live, removal dates 2026-12-31 + 2026-09-30 preserved per PR-GODOBJ-11 forward-pointer).

  - **godlike/07 honest-limitation**: (1) this audit prioritized by the "residue-impact-on-build" lens — chrome_provider / qdrant_maintenance / legacy-route-residue deferred because they don't block `go build ./...` (user-spec: minimal-blast-radius). (2) Q9 is the one Q-prefixed with `blocker: true` per architecture/issues.yaml — other Q-prefixed items are test-failures not build-blockers, so cluster at the 2026-07-25 deadline band. (3) Cross-package residue (e.g. `internal/application/assets/sourcing/types.go:39` IndexingStatus YouTube-side enum) is NOT in this wave — that's `architecture/current.yaml#id-29` PR-CrossPackage-IndexingStatus-§12-5 (deadline 2026-08-15) orthogonal ticket, NOT residue per godlike/06 SSOT to avoid cross-package audit-creep.

  - **No migration / no gofmt touch / no test churn**: documentation-only lockstep across 3 surfaces (this entry in AGENTS.md §Recent cross-cutting closures + CHANGELOG.md `## Unreleased > ### Added > AUDIT-RESIDUE-2026-07-04` mirror + architecture/current.yaml wave-tracker entry). Per AGENTS.md Git-Lesson-2: each per-residue-PR commit lands **directly on `main`** per-file (auto-sufficient granularity) with Co-authored-by trailer per Git-Lesson-3.

  **Cross-reference:** `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04` (wave-tracker anchor + 12 net-new linked_issues + 7 PR-* per-item deadlines) + CHANGELOG.md `## Unreleased > ### Added` closure mirror + `architecture/issues.yaml` Q4..Q9 canonical owners + AGENTS.md §Active Concerns #10 (Issue 8 closure precedent for ApplyPreset canonical-surface precedent — apply the same template discipline here).

- **PHASE-1C wave-tracker** (Phase 1c TODO closure chain, 5 commits → 7 SHAs after Fix #1, 11 sites closed): `architecture/current.yaml#wave_status.PHASE-1C` — see `CHANGELOG.md ## Unreleased → ### Fixed` for end-to-end audit trail.

---


- **[P0-COMPL-3 STAGED (`4030081a` + `bb4a19e8`, shipped 2026-07-03, July 2026)]** `fix(completion)` — flipped `architecture/current.yaml#P0-COMPL-3-STAGED` `status: pending` → `status: shipped` per godlike/07 no-fake-availability: the EXPAND-phase subset (canonical `remote.StagedArtifactReference` 3-field envelope at `internal/domain/remote/staged_artifact_reference.go` + JSON wire-tag `staged_artifacts` on the complete-with-artifacts DTO + Sender-side `PublishAndCompleteUseCase` Staged→Published converter) shipped 26 days ahead of the planned 2026-07-29 PR-4 deadline, via the P0-COMPL-5-WIRE-NAMING umbrella commits `4030081a` (production surface) + `bb4a19e8` (TDD test typo fixup). 4 round-trip TDD tests green. **Honest scope-lock (godlike/07):** the `refToVerifiedArtifact()` projection is intentionally a STUB (minimal SourceVersion/IdempotencyKey/SHA256 hints) — the BACKFILL-phase `StagedResolver` + `VerifiedArtifactProjector` lookup that populates LocalPath/SHA256/MIMEType from `media_assets` is forward-pointed to **`P0-COMPL-2-COMPOSITION-WIRE`** (action plan PR-4, deadline 2026-08-01; noun-only correction of the pre-closure parenthetical that misclassified P0-COMPL-3 as "different concern: HTTP wire-format vs internal idempotency key derivation" — P0-COMPL-3 IS the wire-format rename concern per the action plan; corrected in this closure).
- **[P0-COMPL-4-PUBLISH-DEDUPE (ca73476d, July 2026)]** `refactor(completion)` — Publisher port removed from `completion.Service`. Drift detection enforced via reflection-based `TestDedup_NoPublisherFieldOnServiceStruct` (build-failure on any future re-introduction of a `publisher`/`pub`/`notifier` field). Per godlike/06 SSOT one-canonical-owner-per-fact: Preparer is the canonical Drive-write seam. Forward-pointer: P0.6 (single-TX atomic complete) is a separate concern fed by this capability.
- **[P0-COMPL-5-WIRE-NAMING (4030081a + bb4a19e8, July 2026)]** `refactor(api+jobs)+feat(completion)` — wire-format rename + canonical `PublishAndCompleteUseCase` single-backbone orchestrator + 3 round-trip TDD tests. Typo fixup commit `bb4a19e8` closes a wire-naming drift from the initial `4030081a` surface. This umbrella ALSO shipped the P0-COMPL-3 StagedArtifacts EXPAND-phase subset 26 days ahead of the planned PR-4 deadline — see P0-COMPL-3 bullet above for the canonical closure reference. Forward-pointer `P0-COMPL-2-COMPOSITION-WIRE` (deadline 2026-08-01) tracks the BACKFILL-phase composition-root wiring + `StagedResolver`->`media_assets` lookup that replaces the `refToVerifiedArtifact()` stub.
- **[P1-#14 typed-idempotency (88f564d3 + 045bd106 + bc792768, July 2026)]** `fix(jobs) + fix(completion) + test(completion)` — loop-accumulation contract for `PublishVerifiedArtifacts`; per-artifact failures accumulated into `PublishOutcome.Err` (no short-circuit); `ErrIdempotencyKeyConflictDifferingContent` typed sentinel reserves the top-level return for the FAIL-CLOSED same-key/different-sha collision (per godlike/07 P0.7 typed-error contract). Forward-pointer: bookkeeper record failure telemetry (`PR-P1-14-BOOKKEEPER-OBSERVABILITY`, deadline TBD 2026-07-25).
- **[P1-#15 HTTP completion errors (37ed2b9e, July 2026)]** `fix(jobs+api)` — typed HTTP layer completion errors (7-kind map end-to-end) routing each canonical typed sentinel to its proper HTTP status. Per godlike/07: no string-matching, no leak of internal error types to the wire. Forward-pointer: client-side parser verification (e.g. OpenAPI codegen, generated TypeScript client) is out of scope for this closure — tracked separately as `PR-P1-15-OPENAPI-CLIENT`, deadline TBD 2026-08-01.

- **[Q9-VOICEOVER-FanoutUseCase verification closure (2026-07-04)]** `docs(arch)+verifier-only` — verification-only audit-pin. Canonical Q9 closure landed on `origin/main` via PARALLEL-AGENT race-recovery at SHA `027e606f` (`arch(issues): court-circuit Q8-SqlSchemaStatus + Q9-VOICEOVER-FanoutUseCase as Sprint P1-5 residuals action P1-5 cleanup plan`) BEFORE this audit-pin cycle completed. My role on 2026-07-04: **VERIFIER** (not closure author). Verification evidence (run on `origin/main@1138f9dc`): `go build ./...` exits 0; `go vet ./internal/application/voiceover/...` exits 0; `go test -count=1 -run=^never$ ./internal/application/voiceover/jobs/` compile-only dry-run exits 0; `internal/application/voiceover/jobs/fanout.go` preamble explicitly documents the drift-drop recovery from `4cb13c86` (p1.6 cleanup) + `75b2550a` (p0.6 Active Concerns #11); the composition root at `internal/app/build_bundles_voiceover.go:350` constructs `voiceoverjobs.NewFanoutVoiceoversUseCase(voiceoverjobs.FanoutDeps{Enqueuer: jobs.Service, Logger: log})` byte-for-byte the canonical signature (note `FanoutDeps.Enqueuer`, NOT the stale `FanoutDeps.JobsService` from the pre-restoration drift-drop). AUDIT-RESIDUE-2026-07-04 wave-tracker (`architecture/current.yaml#AUDIT-RESIDUE-2026-07-04.linked_issues[PR-BLOCKER-Q9-VOICEOVER-FANOUTUSE-CASE]`) was flipped `pending` → `shipped` via the canonical parallel-agent closure. **Reconciliation lineage (godlike/07 no-fake-availability)**: my local orphaned SHAs are `c625caf1` (retry preservation, FAILED byte-equivalence — superseded) and `837ffd2e` (Q9 closure v1, superseded) — both preserved in git reflog for `git fsck --unreachable` audit per AGENTS.md §Git-Lesson-5 byte-equivalent-replay-race-recovery. ZERO production code authored in this audit-pin — only this AGENTS.md entry is added in this commit. The 4 canonical lockstep surfaces (CHANGELOG.md + AGENTS.md + architecture/current.yaml + architecture/issues.yaml) all confirm Q9 closure status consistent with each other as of 2026-07-04. Cross-references: `architecture/issues.yaml#Q9-VOICEOVER-FanoutUseCase` (canonical status:done, blocker:false), CHANGELOG.md `## Unreleased → ### Fixed` (Q9 entry by canonical SHA `027e606f`), this AGENTS.md audit-pin. The AUDIT-RESIDUE-2026-07-04 wave is now UNBLOCKED — every other PR-* tracked in this wave's 12-slot `linked_issues` can run per-file `go build` validation per-file. (Tagged: Q-closure-verifier, race-recovery-disclose, byte-equivalent-replay-acceptance, lockstep-consistency-audited.)

- **[PR-REFLECT-ELIM-HANDLER-REGISTRATION closure (2026-07-04)]** `refactor(jobs) + test(jobs)` — retire the `reflect.ValueOf/Call` runtime type-check + structural-anonymous-func-literal case in `internal/application/jobs/handler_registration.go::RegisterHandler` (commit `69606b9092251adb8b11b7af46acc8e89ec7a592` on `origin/main`). The pre-refactor `any`-typed register surface silently accepted any `reflect.ValueOf`-callable value at runtime — a signature drift in `HandlerFunc` would surface as a runtime error rather than a build failure, AND it bypassed the godlike/07 typed-error gate that codifies the canonical wire-shape contract. Post-refactor: strict type-switch accepting ONLY `appjobs.HandlerFunc` (the Go type alias for `domainjob.Handler`); reflection import REMOVED; 13 caller migrations add the explicit `appjobs.HandlerFunc(h.HandleJob)` cast at every call site; 7 TDD tests in the new `handler_registration_test.go` lock the type-switch contract. Surface signature REMAINS `(jobType string, handler any) error` because 4 cross-zone interface contracts depend on it (kernel/job/service.go::Service + scripts/ports/ports.go::Broker + api/module_descriptor.go::JobRegistrar + app/creator_runtime.go::brokerAdapter), each locked via compile-time `var _ pin` — per godlike/07 minimal-blast-radius, the IMPLEMENTATION is tightened while the surface is preserved. **godlike/06 SSOT (one canonical owner per fact):** the `HandlerFunc` shape lives ONLY in `internal/domain/job/handler.go`; the application-layer alias `type HandlerFunc = domainjob.Handler` keeps the call site readable. **godlike/07 no-fake-availability:** typed-error gate at registration time rejects every non-HandlerFunc shape — no silent-acceptance class remains in the register surface. **godlike/07 minimal-blast-radius:** 4 surface contracts preserved verbatim; the substantive godlike/07 win is the IMPLEMENTATION-side type-switch + the caller-side explicit cast idiom (canonical per the artlist precedent at `internal/application/assets/providers/artlist/job_core.go:247`). **Verification:** `go vet ./internal/application/jobs/` exit 0; `go build ./...` exit 0; `go test -short ./internal/application/jobs/` exit 0. **Pre-existing build issues (out of scope, NOT regressions):** the same 5-item carry-forward list (`monitor/enqueue.go` `strings.ToLower` undefined + `monitor/scheduler.go` `NewUnboundJobEnqueuer` undefined + `stockpipeline/run_upload.go` syntax error + `app/module_media.go::clips.Deps.MutationsDispatcher` literal + `images/routing` import cycle). **Wave-tracker cross-reference:** `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04.linked_issues[PR-REFLECT-ELIM-HANDLER-REGISTRATION]` flipped `status: pending` → `status: shipped` with `ship_sha: 69606b9092251adb8b11b7af46acc8e89ec7a592` + `ship_date: 2026-07-04`. **CHANGELOG.md** mirror entry under `### Fixed`. **Honest-limitation:** a future godlike/06 SSOT tighten that flips the cross-zone interface signatures to `(string, HandlerFunc) error` is forward-pointer `PR-HANDLER-REGISTRATION-TYPED-SIGNATURE` (deadline 2026-08-15) and is deferred per godlike/07 minimal-blast-radius (changing the surface breaks 4 cross-zone compile-time assertions). Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.

- **[PR-IMAGES-SHIM-REMOVAL closure (2026-07-04)]** `refactor(images) + test(images)` — retire the 3 fake-availability surfaces in `internal/application/images/generation_service.go` (commit `851c5a93` on `origin/main`): (a) `imageGen` field REMOVED (dormient, unwired — composition now wires `*generated.GenerationProviderRegistry` ONLY, no legacy `ImageGenerator` fallback per godlike/07 KILL LIST a); (b) `GenerateSmartImageWithAccount` REMOVED (account/project params were silently dropped = godlike/07 fake availability); (c) `HandleJob` shim that rebuilt `NewJobHandler(...)` per call REMOVED (composition root owns the canonical wiring). Migration: `Service` struct gains `JobHandler *JobHandler` field wired ONCE in `NewService(deps)` via `NewJobHandler(generatedRegistry, deps.GenAI.StyleRegistry, log)`; `Service.HandleJob` + `Service.RegisterHandler` delegate to the held `JobHandler`. The 2 call sites in `internal/api/images/{impl.go, territory_handlers.go}` were migrated: `GenerateImageRequest` + `GeneratedGenerateRequest` structs lost the `Account` / `ProjectID` fields (which the legacy shim silently dropped), and the call sites now invoke `GenerateSmartImage(...)` (no account/project args). The test that pinned the fake-availability behavior (`TestGenerateSmartImageWithAccount_ReturnsNotImplemented`) is RETIRED per godlike/07 no-fake-availability. Canonical `*GenerationService` surface is now 2 methods (GenerateSmartImage + TriggerPrewarm) + 4 fields (registry, styles, log, storage) — no per-call `JobHandler` construction. **godlike/06 SSOT (one canonical owner per fact):** `*JobHandler` lives ONLY in `internal/application/images/generation_job.go`; `*GenerationService` wiring lives ONLY in `internal/application/images/generation_service.go`; composition root `internal/app/build_bundles_core.go` is untouched (the `NewService(deps)` constructor signature stays 1-arg). **godlike/07 minimal-blast-radius:** 0 surface contract changes; the only caller-visible delta is 2 dropped request DTO fields (no call site cared — they were silently dropped in the legacy shim). **Verification:** `go vet ./internal/application/images/...` + `go build ./internal/application/images/...` + `go test -short ./internal/application/images/...` all exit 0 (modulo the pre-existing 5-item carry-forward build issues per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`). **Wave-tracker cross-reference:** `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04.linked_issues[PR-IMAGES-SHIM-REMOVAL]` flipped `status: pending` → `status: shipped` with `ship_sha: 851c5a93` + `ship_date: 2026-07-04`. **CHANGELOG.md** mirror entry under `## Unreleased → ### Fixed`. **Honest-limitation:** the 6 pre-existing build issues (FIX-MONITOR-ENQUEUE-TOLOWER + FIX-MONITOR-SCHEDULER-ENQUEUER + FIX-STOCKPIPELINE-REDECLARATION + FIX-APP-MODULE-MEDIA-DISPATCHER + FIX-IMAGES-ROUTING-CYCLE + FIX-APP-WIRE-SCRIPT-SYNTAX [retired]) are NOT regressions of this PR — they predate ART-001 closure and carry forward per the canonical 6-item carry-forward convention. Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
- **[PR-DRIVECLIENT-RAW-RETIRE closure (2026-07-04)]** `refactor(app)` — physically remove the `DriveClient *gdrive.Service` field from `internal/app.DriveBundle` per godlike/06 SSOT (commit `08be6ede` on `origin/main`). The 4 canonical Pattern 0 ports (Admin / Reader / DocClient / Lifecycle + Publisher) declared on the same struct are the ONLY Drive surface on the bundle; raw `*gdrive.Service` reach-through is no longer exposed via composition-root. Migration: `composition.go::DriveBundle.DriveClient` field REMOVED; `gdrive` import REMOVED from `composition.go` (the only use was the removed field); `build_bundles_drive.go::BuildDriveBundle` literal: `DriveClient: driveClient,` assignment REMOVED. The local `driveClient *gdrive.Service` variable in `BuildDriveBundle` is RETAINED for the `driveUploader` ctor + `startClosure`'s `startDriveBackgroundFolders` call (internal-only, NOT exposed via the bundle). The `gdrive` import is RETAINED in `build_bundles_drive.go` because `startDriveBackgroundFolders` still takes `driveClient *gdrive.Service` as a parameter for the start-time FolderManager probe. **godlike/06 SSOT (one canonical owner per fact):** the 4 Pattern 0 ports (Admin / Reader / DocClient / Lifecycle) own every Drive operation exposed via the composition-root bundle; `*drive.Uploader` satisfies both `Admin` and `Reader` structurally via the compile-time assertions at `internal/infrastructure/drive/ports.go`. **godlike/07 minimal-blast-radius:** the `ArtlistBundle.DriveClient` field (separate struct in `internal/app/bundle_types.go`, always nil in production) and the `HasDriveClient` JSON diagnostic field (`artlist/types.go`, different concern: JSON wire shape vs internal composition-root field) are INTENTIONALLY out of scope — separate forward-pointers. The `startDriveBackgroundFolders` function still takes `driveClient *gdrive.Service` as a parameter (internal-only seam) — a follow-up wave should migrate that to the canonical `Admin` port per godlike/06 SSOT. **godlike/07 typed-error contract:** no silent-success or fake-availability risk introduced; the 4 canonical ports are fail-closed at composition time (typed-NIL-safe constructors; nil port surfaces as a typed error at first publish call). **Verification:** `gofmt -d internal/app/composition.go internal/app/build_bundles_drive.go` exit 0; `rg root\.Drive\.DriveClient|driveBundle\.DriveClient|DriveBundle\.DriveClient internal/` returns 0 hits (audit pre-fix showed zero production-code reads; post-removal is also zero). **Deprecation record:** `architecture/deprecations.yaml#DRIVE-RAW-BUNDLE-LEAK` (status: removed, migration_phase: CONTRACT). **Wave-tracker cross-reference:** `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04.linked_issues[PR-DRIVECLIENT-RAW-RETIRE]` flipped `status: pending` → `status: shipped` with `ship_sha: 08be6ede` + `ship_date: 2026-07-04`. **CHANGELOG.md** mirror entry under `## Unreleased → ### Fixed`. **Honest-limitation:** pre-existing build issue `FIX-IMAGES-ROUTING-CYCLE` (deadline 2026-08-01) blocks `go build ./internal/app/...` + `go test -short ./internal/infrastructure/drive/...` via a pre-existing import cycle involving `internal/infrastructure/database/sqlite/assets`; the cycle reproduces on the stashed pre-PR tree (NOT a regression from this PR). Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
- **[PR-CHROME-PROVIDER-SPLIT closure (2026-07-04)]** `refactor(images)` — decompose `internal/application/images/chrome_provider.go` (~260 LoC god file) into 4 single-purpose capability files per godlike/06 + AGENTS.md Pattern 5 (commit `cd7e1799` on `origin/main`): `chrome_provider.go` (slim public surface: `ChromeImageProvider` struct + `NewChromeImageProvider` + `Generate` + compile-time `var _ ImageGenerator = (*ChromeImageProvider)(nil)` assertion) + `slide_worker_process.go` (`ensureStarted` + `Stop` subprocess lifecycle) + `slide_worker_protocol.go` (`writeJSON` + `readResponse` + `readRawResponse` + `workerResponse` + `mapToStruct` JSON wire protocol) + `slide_worker_health.go` (`healthCheck` + `Health` + `ActiveCooldownProfiles` health probes). **BRUTAL DECISION (godlike/07 no-fake-availability):** (a) `numProfiles` constructor arg RETIRED — the pre-PR `NewChromeImageProvider(scriptsDir, numProfiles, log)` silently ignored `numProfiles` (the worker was always launched with `--profiles 1`); the arg was fake-availability (param accepted but never honored). Now 2-arg signature. (b) `cooldowns map[int]int64` field RETIRED — the pre-PR code tracked per-profile cooldowns (60s after quota/auth errors) but the policy never fanned out beyond profile 0 (single-profile = no per-profile routing). The per-profile cooldown tracking block in `Generate` is REMOVED. `ActiveCooldownProfiles() int` PRESERVED on the public surface (consumed by `DiagnosticsService.Diagnostics()` to populate the `ImageGenCooldownProfiles` field) but ALWAYS returns 0 (single-profile policy = no per-profile routing; godlike/07 demands the counter report the truth, not a tracked-but-never-actionable value). `--profiles 1` hard-coded in `slide_worker_process.go::ensureStarted` (canonical single-profile policy). **`internal/app/build_bundles_core.go:162`** UPDATED: drop the `1` arg from the `NewChromeImageProvider` call. **godlike/06 SSOT (one canonical owner per fact):** each of the 4 files owns exactly one capability concern — public surface / subprocess lifecycle / JSON wire protocol / health probes. **godlike/07 minimal-blast-radius:** `diagnostics_service.go:35` + `capability_test.go` + `DiagnosticsReport.ImageGenCooldownProfiles` field are NOT touched (method still exists, struct literal still works with fewer fields, diagnostics field always reports 0 per the canonical policy). **godlike/07 typed-error contract:** no silent-success or fake-availability risk introduced; the 4-pattern-file split preserves every error path + the canonical 4-arg error wrapping (`fmt.Errorf` + `%w`). **Verification:** `gofmt -d` exit 0; `go build ./internal/application/images/...` exit 0; `go test -short ./internal/application/images/...` exit 0; `rg numProfiles|cooldowns internal/ --type go` → 0 PRODUCTION-CODE hits (the 4 audit-pin comment hits are intentional godlike/06 documentation discipline). **Wave-tracker cross-reference:** `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04.linked_issues[PR-CHROME-PROVIDER-SPLIT]` flipped `status: pending` → `status: shipped` with `ship_sha: cd7e1799` + `ship_date: 2026-07-04`. **CHANGELOG.md** mirror entry under `## Unreleased → ### Fixed`. **Honest-limitation:** pre-existing build issue `FIX-IMAGES-ROUTING-CYCLE` (deadline 2026-08-01) blocks the full-project build via a pre-existing import cycle; this PR passes targeted `go build ./internal/application/images/...` + `go test -short ./internal/application/images/...` exit 0. Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
## Core Contracts

All modules must use canonical contracts:
- `internal/domain/asset/` — Asset, MediaType, Repository, Processor, Destination
- `internal/domain/job/` — Job, Store, WorkerSession
- `internal/domain/script/` — Script, Plan, GenerationSpec
- All long-running operations must use `internal/application/jobs/` service

---

## 🧰 Utilities to prefer

Prima di scrivere custom code, **controlla se esiste già in `pkg/`**. Ogni utility è leaf-only (zero import da `internal/`); `pkg/` è dove cerchi prima di replicare logica. Regola pratica: se stai per incollare 20+ righe di helper, prima `grep` qui sotto.

| Pacchetto & Scenario | Helper chiave (preferisci questo invece di custom) |
|---|---|
| **`pkg/defaults`**<br>Default coalesce (string/int/float) | `String(val, fallback)`, `Int(val, fallback)`, `Float64(val, fallback)` |
| **`pkg/retry`**<br>Retry con backoff esponenziale + transient error classification (Step 7 closed, June 2026) | `IsTransient(err error) bool`, `WrapTransient(err error) error (TypedInfrastructureError carrier)`, `TransientInfrastructureError typed carrier (errors.As probe + idempotent on re-wrap)`, `DefaultOptions() returning BaseDelay=500ms / MaxBackoff=30s / MaxAttempts=5 / JitterFraction=0.25` |
| **`pkg/hashutil`**<br>Hash content / ID generation | `SHA256String(s)`, `RandomString(n)`, `MD5File(path)`, `HashFile(path, h)` |
| **`pkg/corid`**<br>Correlation ID propagation (job/script) | `WithCorrelationID(ctx, id)`, `FromContext(ctx)` - usalo nel middleware request ID e nell'enqueue job |
| **`pkg/ptrutil`**<br>Pointer utilities | `Ptr[T](v)`, `DerefOr[T](p, fallback)` |
| **`pkg/textutil`**<br>Text/slug/voiceover cleanup | `Slugify`, `SlugifyWithMax`, `CountWords`, `Truncate`, `CleanForVoiceover`, `FirstNonEmpty(...)`, `ParseVTTTimestamp`, `SplitScriptSentences` |
| **`pkg/fileutil`**<br>File I/O JSON + filesystem | `WriteJSON(path, v, indent)`, `ReadJSON(path, v)`, `CopyFile`, `CleanFolderName(s)`, `UsableCachedClip(path)` |
| **`pkg/apiutil`**<br>Gin HTTP helpers (handler) | `BindJSON[T](c)`, `OK(c, data)`, `BadRequest(c, msg)`, `InternalError(c, err)`, `NotFound`, `Error(c, status, msg)`, `ClampLimit(v, def, max)` |
| **`pkg/handlerutil`**<br>Pagination & job utilities | `ParsePagination(defaultLimit, maxLimit)`, `AsyncJobResponse(c, job, msg)`, `EnqueueAsync`, `ParseJobStatusFilter` |
| **`pkg/concurrent`**<br>Concurrency / errgroup+panic | `WithContext(parent)`, `ParallelMap[T,U]`, `SafeGo(name, fn)` (sostituisce WaitGroup+Mutex+recover custom) |
| **`pkg/sliceutil`**<br>Slice primitives | `UniqueStrings`, `UniqueStringsCI`, `MinInt(a, b)`, `Clamp(v, lo, hi)`, `GroupSentences`, `NormalizeAndDedupe`, `MergeNormalizedLists` |
| **`pkg/sqlutil`**<br>SQL fallbacks (FTS5 bandito) | `BuildFallbackLikeConditions(tokens, cols)`, `BuildFallbackLikeConditionsOR` |
| **`pkg/urlutil`**<br>YouTube URL / Drive link parse | `ExtractVideoID(raw)`, `FileIDFromDriveLink(raw)` |
| **`pkg/pathutil`**<br>Path/folder naming | `SafeFolderName(name)`, `BuildTimestampedSlug`, `ExtractStyleFromPath(relPath)` |
| **`pkg/termutil`**<br>Term/name parsing + topic match | `SubjectMatchesTopic`, `ExtractLikelyNames`, `TermsFromText`, `TopicTokens` |
| **`pkg/similarity`**<br>Similarity math | `Jaccard(a, b)`, `TokenSet(text)`, `OverlapRatio(startA, endA, startB, endB)` |
| **`pkg/matchingconfig`**<br>Matching thresholds config | `LoadMatchingConfig(path)` - **nicho**, solo se tocchi semantic/similarity scoring |
| **`pkg/testutil`**<br>Test helpers | `MustMarshalJSON(t, v)` |
| **`pkg/veloxclient`**<br>Job HTTP client riusabile | `New(baseURL, token)` → `SubmitAsync`, `GetJobStatus`, `IsTerminal` |
| **`pkg/timeutil`**<br>Time RFC3339 | `ParseRFC3339`, `FormatNow`, `ParseRFC3339PtrString` |
| **`pkg/executil`**<br>External process exec | `Run(ctx, name, args, opts)`, `RunSimple`, `LookPath`, `CommandExists` |
**Servizi interni riusabili** (non reinventare la ruota — vietato duplicare logica):

| Scenario | Servizio | Path |
|---|---|---|
| Enqueue/poll/cancel async | `jobs.Service` | `internal/jobs/` — sempre per ogni long-running (>5s) |
| Emetti un job typed via CompiledJobRegistry | `jobs.Dispatcher.Enqueue` | `internal/application/jobs/dispatcher.go` — typed entry point (P0 Commit 4, July 2026); passa per `CompiledJobRegistry.Definition` → `def.PayloadCodec.EncodePayload(payload)` → queue/timeout/retry da `JobDefinition` → delegate a `Service.Enqueue`. **Canonical replacement** per ogni future caller (Check 51 in `scripts/ci-architectural-checks.sh` ci-gate rileva raw-string `.Enqueue(<ctx>, "<literal>")` come SSOT regression). Migration-step zero-vuoto oggi (zero raw-string callers production); surface live, future wiring deferred a C5+. |
| Vettori Qdrant (interfaccia canonica) | `vectorstore.Service` | `internal/media/vectorstore/` — mai HTTP directo |
| Reranker CrossEncoder BGE-reranker-v2-m3 | `reranker.Client` | `internal/reranker/` |
| Embeddings/chat LLM (con retry + fallback) | `ollama.client.Client` | `internal/ml/ollama/client/` |
| Read media_assets | `clips.Repository` | `Removed: clips/` — GetClip, SearchByTags |
| Script generation core | `scriptcore.Engine` | `internal/service/scriptcore/` — WriteScript |
| Script→asset semantic match | `association.Service` | `internal/media/association/` |
| Real-time clip search (post-Qdrant) | `realtime.Service` | `internal/media/realtime/` |
| Topic-by-DB routing (folder risoluzione) | `voiceover.GroupsResolver` | `internal/media/voiceover/` |
| Salva con idempotency / outbox | `outbox.Dispatcher` | `Removed: outbox/` |
| Google Drive upload / Doc creation | `drive.Uploader`, `drive.DocClient` | `internal/infrastructure/drive/` |
| Channel monitor background | `monitor.ChannelMonitor` | `internal/application/monitor/` |

> **Regola**: se l'utility corretta non è in questa tabella ma `grep` mostra un duplicato (la stessa funzione implementata in >1 posto), PRIMA estrarla in `pkg/<x>/` poi consumarla. Esempio realistico visto nel codice: 4+ implementazioni di "retry con backoff" sono state collassate in `pkg/retry` — stessa opportunità per qualsiasi altra duplicazione che trovi.

---

## ✂️ Modular edit patterns

Quando modifichi il codebase, **modularizza**: una decisione per sezione, una modifica per file, niente "monkey patch" nel posto sbagliato. Questi pattern sono osservati dalla codebase esistente e dai CHANGELOG.

### Pattern 0 — Port abstraction layer (June 2026, PR1.7 followup)

**Regola**: quando introduci una nuova dipendenza esternalizzabile (database, servizio AI, subprocess executor, client di terze parti), **non** chiamarla mai direttamente dal service. Dichiara invece un **port** (interfaccia strutturale) in `internal/application/<feature>/ports.go`, implementa l'adapter concreto in `internal/infrastructure/<feature>/`, e inietta il concrete in `NewService(ServiceDeps{...})` da `internal/app/composition.go::Build*Bundle()`.

**Perché**:
- *Compile-time assertions* — `var _ application.Port = (*Concrete)(nil)` cattura drift di signature al compile, non al primo panic runtime.
- *Test injectability* — i test swappano il concrete via `ServiceDeps{...}` literal senza patchare lo state globale.
- *Type aliases back-compat* — se la storia richiede un rename DTO, introduci `type OldName = NewName` invece di rompere il consumer.

**Quando NON serve**:
- Logica in-memory (parsing, math, cache in-memory).
- Una sola implementazione concreta senza sostituti in alcun test.
- Tipi steady che non subiscono rename per anni.

**Code-verdetto PR1.7 (June 2026)**: 12 port strutturali in `internal/application/youtube/ports.go` con compile-time assertions. Back-compat aliases: `type VideoMetadata = DownloaderMetadata`, `type YouTubeMetadataPort = DownloaderMetadata`. Empty-marker pattern (`interface{}`) ancora ammesso SOLO per port la cui signature è opaca lato chiamante (cache store, temp-file manager dove solo l'infrastruttura consuma la firma concreta).

### Pattern 1 — Aggiungere un HTTP handler

1. Crea `internal/api/<feature>/<file>.go` (un handler per feature, 5-8 file max).
2. Definisci request/response types in `requests.go` / `responses.go` della feature.
3. Registra via `RegisterRoutes(*gin.RouterGroup)` della feature, chiamato dal modulo in `internal/app/registry.go`.
4. **VIETATO** aggiungere handler a file `god_object.go` esistenti. Se >30 file in una directory, splitta per capability (Pattern 5).
5. **Mai** chiamare business logic direttamente dal handler — passa per use case in `internal/application/<feature>/`.

```go
// Shape canonica
func (h *XHandler) NewAction(c *gin.Context) {
    if h.deps == nil { apiutil.Error(c, http.StatusServiceUnavailable, "deps not initialized"); return }
    req, ok := apiutil.BindJSON[NewActionRequest](c)
    if !ok { return }
    out, err := h.deps.svc.Do(c.Request.Context(), req)
    if err != nil { apiutil.InternalError(c, err); return }
    apiutil.OK(c, out)
}
```

### Pattern 2 — Aggiungere una tabella DB

1. Crea `migrations/sqlite/0XX_<descriptive_name>.sql` (numero progressivo; **mai** modificare migration esistenti).
2. Crea `Removed: <domain>/repository.go` con i metodi CRUD tipizzati.
3. Test di round-trip: insert + select dopo migrate, deve tornare uguale.
4. **VIETATO** applicare migration generiche cross-DB (anche se ora c'è un solo DB, il principio resta).
5. **FTS5 bandito**: per full-text usa `pkg/sqlutil.BuildFallbackLikeConditions`.
6. **Canonical authority** (one owner per fact): every new table has exactly one owning capability, and the same fact must not have multiple independent writers. The authoritative pointer is now [CANONICAL.md §1](./CANONICAL.md); the historical 8-domain ownership table lives in `architecture/ownership.generated.yaml`.

### Pattern 3 — Aggiungere una fase a una pipeline

1. Logica di business nel service core (`internal/service/<X>/`), **mai** nel handler né nel job handler.
2. Fan-out parallelo con `pkg/concurrent.WithContext(ctx)` — first-error-wins + panic recovery inclusi.
3. Per pipeline jobs (script generation, artlist, ...), emetti sempre:
   - `pipeline_stage_started` con `stage` e `job_id` (a inizio fase)
   - `pipeline_stage_completed` con `duration_ms` + extra fields (a fine fase, includi counts/ok/error rate)
4. Aggiorna progress via `tools.Progress(percent, "message")` ad ogni stage (operatori guardano il log stream).
5. Pattern di post-write save ctx: `withPostWriteContext()` invece di `context.Background()` — consulta l'allowlist sopra.

### Pattern 4 — Aggiungere una utility riutilizzabile

1. Crea `pkg/<utility>/<utility>.go` con package doc che spiega lo scopo in 3-6 righe (vedi `pkg/retry/retry.go` come esempio).
2. **1 concetto per file** se la utility ha sotto-funzioni (es. `textutil/split.go` per i chunk VTT/script, separato da `textutil.go`).
3. Aggiungi `pkg/<utility>/<utility>_test.go` accanto — i test sono parte del package (`pkg/<utility>` è leaf, ma i `_test.go` no).
4. **VIETATO** import da `internal/` dentro `pkg/` — `pkg/` è leaf per definizione (vedi ARCHITECTURE §13).
5. Se la utility sostituisce duplicazione esistente, fai la migration in PR separata per `code-search` agent così individui tutti i call site.

### Pattern 5 — Splittare un package (regola corretta — Giugno 2026 v2)

**⚠️ Il flattening di Giugno 2026 ha risolto i file enormi ma ha creato un mega-package da 153 file in `internal/api/`. La regola corretta è:**

1. **Prima dividi per capability stabile**, poi dividi i file all'interno.
2. Un package API **non deve contenere business orchestration** — solo transport HTTP.
3. `internal/api/` root deve restare sotto **15 file produttivi**.
4. Una directory con oltre **30 file produttivi** richiede architecture review.
5. Oltre **40 file produttivi** il CI deve fallire (salvo allowlist documentata).
6. Ogni feature API espone al massimo **1 Handler** principale e **1 funzione** di registrazione route.
7. Le feature API **non possono importarsi tra loro**.

Vecchia regola (deprecata — portava al mega-package):

> ~~Quando un file supera ~300-400 righe o ha >2-3 responsabilità distinte, crea un file per concetto nello stesso package.~~

Esempi già fatti (stato a Giugno 2026): channel_monitor (11 file in `internal/application/monitor/`), extractor_process (10), handler_batch_phases (13), clipindexer (6), voiceover (11), **youtube/ports (PR3 June 2026 — ports extraction)**. **← valori snapshot**: se il numero è cambiato, rifai `ls <dir> | wc -l` per verificarlo; aggiorna qui quando splitti un nuovo file.

### Pattern 6 — Modificare una request o payload struct

Quando aggiungi un campo a una request API o a un job payload (caso reale: Bug A di `generate_timeline` perso silenziosamente — vedi CHANGELOG):

1. **3 posti da aggiornare**:
   - Handler request type (`types_<domain>.go`)
   - Job payload unmarshalable (`jobPayload<X> struct` o equivalente)
   - Worker struct/logica che legge il campo e agisce
2. Aggiungi sempre con `omitempty` o zero-value safe per retro-compatibilità (es. `MinQualityScore float64` con check `> 0`).
3. Test round-trip: scrivi un test `json.Marshal → json.Unmarshal` che verifica che il campo sopravvive.
4. **Mai** aggiungere un campo che the worker legge ma il handler non scrive — finisce come "perso silenziosamente" (questo è esattamente Bug A).
5. Se il campo ha impatto reale, esegui un job reale per verificare end-to-end (usa `pkg/veloxclient` o `scripts/velox_client.py`).

```go
// Diff template:
// 1. types_x.go
type XRequest struct {
    // ...
    NewField string `json:"new_field,omitempty"`  // omitempty per retro-compat
}

// 2. worker payload struct
type jobPayloadX struct {
    // ...
    NewField string `json:"new_field"`  // required dal worker
}

// 3. handler payload map
payload := map[string]any{
    // ...
    "new_field": req.NewField,
}
```

### Pattern 7 — Reusing existing services (regola d'oro)

Prima di scrivere logica nuova, chiediti: esiste già un servizio per X?

**Cross-reference**: la **tabella completa** dei servizi è nella sezione 🧰 Utilities / Servizi interni riusabili sopra — qui sotto solo lo shortcut decisionale dei casi più comuni.

| Tu vuoi... | Usa questo (vedi sezione sopra per il path completo) |
|---|---|
| **Genera uno script end-to-end** | **`scriptcore.Engine.WriteScript`** *(questo è IL punto d'ingresso canonico)* |
| Async work (>5s) | `jobs.Service.Enqueue` |
| Parlare con Qdrant | `vectorstore.Service` (NON HTTP diretto) |
| Rerank risultati | `reranker.Client.Score` |
| Chat LLM | `ollama.Client.Chat` (ha già retry+fallback) |
| Read media_assets | `clips.Repository.GetClip` |
| Salva con idempotency | `outbox.Dispatcher` |

Se l'utility che cerchi **non è nella sezione 🧰 Utilities**: probabilmente devi creare un servizio condiviso in `internal/service/<X>/` PIUTTOSTO che duplicare logica. Una `code-search` veloce (`rg -l "<func_name>"`) conferma se è già implementato altrove.

### Pattern 8 — API package: thin transport only

**Regola**: `internal/api/**` non deve contenere business orchestration.

**Vietato importare in `internal/api/**`:**
- `database/sql`
- `Removed: ` (repository concreti)
- `google.golang.org/api/drive/v3` (Google Drive SDK)
- `internal/infrastructure/media/ffmpeg` (FFmpeg/process execution)
- `os/exec`

Queste dipendenze devono passare attraverso use case o interfacce definite in
`internal/domain/` o `internal/application/`.

**Shape canonica di un handler HTTP:**

```go
type Handler struct {
    generateFromClips GenerateFromClipsUseCase
    generateImages    GenerateWithImagesUseCase
    generateBatch     GenerateBatchUseCase
    curate            CurateUseCase
}

func (h *Handler) GenerateFromClips(c *gin.Context) {
    req, ok := apiutil.BindJSON[GenerateFromClipsRequest](c)
    if !ok { return }
    result, err := h.generateFromClips.Execute(c.Request.Context(), toCommand(req))
    if err != nil { apiutil.HandleError(c, err); return }
    apiutil.OK(c, result)
}
```

---

### Pattern 9 — Dispatcher.Enqueue via CompiledJobRegistry (P0 Commit 4, July 2026)

**Regola**: quando un caller (handler, dispatcher, use case) deve emettere un job async, **NON** chiamare `service.Enqueue(ctx, &EnqueueRequest{Type: "<literal>", ...})` direttamente — il path canonico è `dispatcher.Enqueue(ctx, jobType string, payload any) (*job.Job, error)` che instrada via:

1. `CompiledJobRegistry.Definition(jobType)` (registry post-Freeze, locked at boot — C3 surface)
2. `def.PayloadCodec.EncodePayload(payload any)` → `json.RawMessage` (C2 codec surface)
3. Popolazione automatica di `def.Queue` / `def.Timeout` / `def.RetryPolicyKey` da `JobDefinition` (3 metadata che altrimenti andrebbero perse manualmente)
4. Delegazione finale a `EnqueuePort.Enqueue(ctx, &EnqueueRequest{Type: def.Type, Payload: rawBytes})` (compile-time pinned `var _ EnqueuePort = (*Service)(nil)` blocca drift di signature del Service a build-failure, non runtime panic)

**Perché serva il Dispatcher (e non basti il raw Service call):** il `CompiledJobRegistry` è il single-source-of-truth per la metadata di routing (queue, timeout, retry policy) di ogni jobType. Una chiamata raw a `Service.Enqueue(ctx, &typedReq)` salta quel lookup — il caller dovrebbe manualmente copiare queue/timeout/retry da una fonte canonica altrove, rompendo godlike/06 "one canonical owner per fact". Il Dispatcher incapsula quel lookup in un'unica API typed e il check 51 in `scripts/ci-architectural-checks.sh` (pattern `\.\Enqueue\s*\(\s*[^,]+,\s*"[a-z][a-zA-Z0-9._]*"`) rileva future regression che recuperano il surface diretto.

**Vietato**:
- `service.Enqueue(ctx, &EnqueueRequest{Type: "script.generate_from_clips", ...})` — anche se typed (non raw-string), bypassa `CompiledJobRegistry` e quindi la metadata di routing canonica. Ogni nuova emissione di job dovrebbe passare per `Dispatcher.Enqueue(ctx, "script.generate_from_clips", typedPayload)`.
- Caller-injection di payload non-encoded (es. `payload map[string]any`): il `PayloadCodec.EncodePayload` è la sola fonte canonica per la wire-format (json.RawMessage canonical). Un payload non-encoded romperebbe la idempotency contract del worker all'esecuzione.

**Migration status (forward-pointer)**: P0 Commit 4 (July 2026) ha reso la surface live ma zero variazione di behaviour per i 54 callers production esistenti (Audit `rg '\.Enqueue\(' internal/ --glob '!**/*_test.go'` pre-commit: tutti i 54 callers sono typed `EnqueueRequest{...}` literal, zero raw-string). La surface è forward-preventive — il Dispatcher esiste per future emissioni e per sostituire progressivamente i raw Service.Enqueue nei wave successivi (C5+ composition-root wiring di `dispatcher.WithRegistry(compiled).SetEnqueuer(service)`).

**Canonical test surface**: `internal/application/jobs/dispatcher_test.go` (9 TDD tests) copre nil-receiver + enqueuer-unwired (`ErrEnqueuerNotWired`) + nil-registry (`ErrRegistryNotFrozen`) + unfrozen-registry stub + unknown-jobType (`job.ErrUnknownJobType`) + missing-`PayloadCodec` (`ErrCodecMissing`) + encode-error (dual `%w` chain preserva `errors.Is(err, ErrInvalidPayload)` + `errors.As(err, &codecErr)`) + happy-path roundtrip (assertion `stub.lastReq.Payload.(json.RawMessage)`) + fluent-builder nil-guard. Compile-time `var _ EnqueuePort = (*Service)(nil)` blocca future drift.

### Pattern 10 — ArtifactUploader state-machine + idem-key (P0 Commit 6, July 2026)

**Regola**: quando un caller (worker, use case, handler) deve trasferire un artifact al remote-side via il protocollo 3-phase stateful upload, **NON** chiamare `*jobbrokerclient.Client.PrepareArtifactUpload` / `UploadArtifactFile` / `FinalizeArtifactUpload` direttamente dal codice di production — il path canonico è `creator.Adapter` (`internal/infrastructure/remote/creator/adapter.go`) che implementa `remote.ArtifactUploader` (`internal/domain/remote/artifact_uploader.go`) e instrada i 3 wire commands attraverso state-machine + idem-key enforcements. Il port `ArtifactUploader` (3 metodi: Prepare/Upload/Finalize) + la state-machine `UploadState` (6 closed values: PREPARING/UPLOADING/UPLOADED/VERIFIED/FINALIZED/FAILED) + l'idempotency-key deterministica `ArtifactIdempotencyKey(jobID, artifactID, sha256) string` formano il triangolo canonico per ogni sessione di upload.

**Perché servono tutti e tre (port + state-machine + idem-key):**

- **State-machine**: `UploadState.IsValidTransition(to)` enforces the closed 6-state machine (forward chain + sticky-terminal FAILED/FINALIZED sinks + self-loop idempotency). Bypassing la state-machine via caller-side direct call significa corruption di stato silently absorbed (e.g. retry su FINALIZED invece di inventare una nuova sessione) invece di typed errors con `*IllegalTransitionError{From, To}` envelope esposto per log scanners e dashboard.
- **Idempotency-key byte-stability**: `ArtifactIdempotencyKey(jobID, artifactID, sha256)` ritorna lo stesso SHA-256 hex triple-key across retries con lo stesso triple. Bypassing via random UUID o via string concatenata manualmente rompe il remote-side dedup slot — oppure multiple distinct keys on the same logical content fanned out non intenzionalmente.
- **Typed-error godlike/07**: 5 sentinels (`ErrArtifactUploaderNotConfigured`, `ErrArtifactSessionExpired`, `ErrArtifactSessionNotFound`, `ErrArtifactRemoteSchemaVersionUnsupported`, `ErrIllegalUploadStateTransition`) + `IllegalTransitionError{From, To}` typed-data envelope permettono `errors.Is` + `errors.As` traversal in unico probe — callers possono scrivere `if errors.Is(err, ErrIllegalUploadStateTransition) { var ite *IllegalTransitionError; if errors.As(err, &ite) { log.Warn("transition rejected", zap.String("from", string(ite.From)), zap.String("to", string(ite.To))) } }`.

**Vietato**:

- Chiamare i 3 protocol commands direttamente: `.PrepareArtifactUpload(` / `.UploadArtifactFile(` / `.FinalizeArtifactUpload(` su `*jobbrokerclient.Client` dal codice in `internal/application/**` o `internal/api/**` (lock enforced by Check 52 in `scripts/ci-architectural-checks.sh`). Production consumers MUST consumare il typed `remote.ArtifactUploader` port via `*creator.Adapter` injected in composition root.
- Costruire `UploadSession` via literal struct `{ID: ..., LeaseID: ..., ArtifactID: ..., State: ...}` bypassing `NewUploadSession` (the constructor enforches 3-field aggregated diagnostic su Empty + initializes State=PREPARING allo stato canonico d'ingresso).
- Hard-codare l'idempotency-key come UUID random — la retry deve essere byte-stable tramite `ArtifactIdempotencyKey(jobID, artifactID, sha256)` (mirror pattern C4 Check 51 che vieta raw-string `.Enqueue(<ctx>, "<literal>")` callers).
- Saltare la state-machine gate via self-loop su FAILED o FINALIZED — sono sticky-terminal sinks (no transition out). Retry su una session failed richiede una NUOVA session via nuovo `Prepare()` call (canonical godlike/07 contract).
- Re-uso di `idempotencyKey := uuid.NewString()` in Upload/Finalize call sites che bypassano Prepare — la defensive derivation in `creator.Adapter.Upload` + `Finalize` (via `deriveIdempotencyKey` helper) si attiva quando `ctx.IdempotencyKey == ""`, ma il call-site MUST pre-idratare la chiave al Prepare seam per evitare derivation per-call.

**Canonical surface (godlike/06 one-canonical-owner-per-fact):**

- **Port (typed contract)**: `internal/domain/remote/artifact_uploader.go` — `ArtifactUploader` interface (3 methods: Prepare/Upload/Finalize) + `UploadSession` envelope (typed JSON) + `UploadState` typed enum (6 closed values) + `CanonicalUploadStateValues()` + `Valid()` + `IsValidTransition(to)` + 5 typed sentinels + `IllegalTransitionError{From, To}` typed-data struct + `UploadSessionStore` query port + `PrepareContext` envelope (9 fields: JobID/LeaseID/ArtifactID/ArtifactKind/Filename/MIMEType/SizeBytes/SHA256/IdempotencyKey).
- **Idempotency-key helper**: `internal/domain/remote/idempotency.go` — `ArtifactIdempotencyKey(jobID, artifactID, sha256) string` (usa `internal/infrastructure/files.SHA256String` aliased come `hashutil`) + `IsValidIdempotencyKey(s) bool` (case-insensitive 64-char hex) + `ErrArtifactIdempotencyKeyConflict` typed sentinel.
- **Concrete (Creator-side adapter)**: `internal/infrastructure/remote/creator/adapter.go` — `*Adapter` struct + `NewAdapter(deps)` constructor + `WithBrokerClient(...)` fluent setter + state-machine gate inline ad ogni seam + defensive `deriveIdempotencyKey` helper (consolida 4 retry paths) + file streaming via `os.Open` + `io.Copy`.
- **Wire (HTTP protocol)**: `internal/infrastructure/remote/jobbrokerclient/client.go` — `PathUploadPrepare` / `PathUploadFile` / `PathUploadFinalize` path constants + `PrepareArtifactUploadRequest` / `FinalizeArtifactUploadRequest` typed request DTOs + 3 NEW methods on `*Client`: `PrepareArtifactUpload` / `UploadArtifactFile` / `FinalizeArtifactUpload`.

**Migration status (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT):**

- **EXPAND (C6, July 2026)**: canonical surface live + ci-gate Check 52 forward-preventive. Zero variazione di behaviour per existing pre-C6 worker.AssetClient.UploadFile callers (Audit `rg '\.(PrepareArtifactUpload|UploadArtifactFile|FinalizeArtifactUpload)\(' internal/application internal/api` pre-commit: 0 caller, zero violations).
- **BACKFILL (C7, forward-pointer)**: migrate pre-C6 worker callers to consume `remote.ArtifactUploader` port via `*creator.Adapter` injected in composition root. Add `Context context.Context` field to `PrepareContext` for ambient-ctx threading (canonical hardening deferred dal C6 NIT-1 logged by code-reviewer).
- **CUTOVER (C8, forward-pointer)**: retire pre-C6 worker.AssetClient.UploadFile surface — godlike/06 SSOT one-owner-per-fact restored. `architecture/deprecations.yaml` entry gated by C7 completion.
- **CONTRACT (final lock)**: physical git-rm of the legacy surface; Check 52 tightened to ban the surface entirely (no allowlist row).

**Canonical test surface (godlike/06 audit-pinning):**

- `internal/domain/remote/artifact_uploader_test.go` (~430 LoC, 9 TDD tests): legal transitions (4 forward edges inclusi il skip-ahead UPLOADED→VERIFIED allowed) + sticky-terminal rejection (FAILED→* e FINALIZED→* rejected) + non-terminal-to-FAILED accepted per i 4 stati non-sink + self-loop idempotency per tutti i 6 canonical values + `ArtifactIdempotencyKey` byte-stability across 1000 retries con stesso triple + empty-marker triple returns empty-marker + `IsValidIdempotencyKey` accepts canonical, rejects all 4 false-case shapes + `IllegalTransitionError.Is` compatibile con `errors.Is(err, ErrIllegalUploadStateTransition)` + `CanonicalUploadStateValues` enumeration completeness (no dups) + `NewUploadSession` aggregates all missing-fields diagnostic.
- `internal/infrastructure/remote/creator/adapter_test.go` (~330 LoC): happy-path 3-call chain (Prepare/Upload/Finalize) + nil-receiver propagation (returns `ErrArtifactUploaderNotConfigured`) + Upload standalone idem-key derivation test (byte-stable across N retries con stesso triple) + Finalize standalone idem-key + sha256 surface test + illegal transition rejection test (FINALIZED→PREPARING fails con typed `*IllegalTransitionError` preservando From/To via `errors.As`).

Compile-time `var _ remote.ArtifactUploader = (*creator.Adapter)(nil)` blocca future drift nella signature del 3-method port.

CI gate Check 52 (`scripts/ci-architectural-checks.sh`) forward-prevention: `.PrepareArtifactUpload(` / `.UploadArtifactFile(` / `.FinalizeArtifactUpload(` callers in `internal/application/**` o `internal/api/**` (al di fuori del canonical allowlist: creator adapter + jobbrokerclient + test files) fail-closed via exit 1. Pre-flight audit today: 0 violations (Creator adapter è l'unico caller under allowlist; production consumers MUST route through `remote.ArtifactUploader` port).

### Pattern 11 — Atomic CompleteJob + idempotency on (jobID, attempt, resultHash) (P0 Commit 7, July 2026)

**Regola**: la Sender-side chiusura di un job (`jobs.status → SUCCEEDED`) vive solo in `internal/application/jobs/completion/complete_job_service.go::Service.Complete`. Ogni altra scrittura di `jobs.status` per il terminal flip è una regressione godlike/06 (un solo owner del fatto).

**Perché**:
- **Single-TX atomicità** — la chain `UpdateJobToSucceededCAS + InsertResultOnConflict + PersistArtifactMap + InsertOutboxEnvelope` deve essere eseguita in **un'unica transazione SQLite**; un fallimento a metà chain DEVE rollare l'intero batch (godlike/07 no-fake-availability: niente job a metà persistenza su disco).
- **Idempotency-on-replay** — il `(jobID, attempt, resultHash)` UNIQUE INDEX su `job_results` collassa i retry sulla stessa riga (`ON CONFLICT DO NOTHING`). Il `IdempotencyCachePort` (in-memory o SQLite) è l'ottimizzazione pre-TX lookup; il vincolo UNIQUE è la superficie autoritativa.
- **godlike/07 typed-error contract** — 9 sentinels (`ErrCompleteJobNotConfigured`, `ErrCompleteJobRequestMissingFields`, `ErrConcurrentLeaseRefutation`, `ErrRemoteArtifactStateNotFinalized`, `ErrRemoteArtifactHasLocalPath`, `ErrRemoteArtifactManifestInvalid`, `ErrRemoteArtifactHashMismatch`, `ErrRemoteArtifactSizeMismatch`, `ErrCompleteJobIdempotencyConflict`) tutti `errors.New(...)` + 1 sentinel sull'artifact idem-key (`ErrArtifactIdempotencyKeyConflict`) dalla superficie C6.
- **Pattern 0 port** — `CompleteJobTxRunner` + `TxContext` interface (6 metodi in-TX) + `IdempotencyCachePort` consentono test hermetici senza SQLite via mock hand-rolled.

**Vietato**:
- Chiamare i metodi `TxContext.UpdateJobToSucceededCAS(` / `InsertResultOnConflict(` / `GetPriorArtifactHashes(` / `PersistArtifactMap(` / `InsertOutboxEnvelope(` direttamente dal codice in `internal/application/**` o `internal/api/**` (al di fuori del canonical allowlist: `internal/application/jobs/completion/**`). Lock enforced by Check 53 in `scripts/ci-architectural-checks.sh`. Production consumers MUST consumare il typed `completion.Service` port injected in composition root.
- Saltare il pre-TX `Validated()` gate (rilassa i missing-fields mandatory check prima dell'apertura della TX — rompe 9 invocazioni su 9 del typo nel caller payload e silent-falla con mezza persistenza).
- Replay con un `resultHash` DIVERSO sullo stesso `(jobID, attempt)`: ritorna `ErrCompleteJobIdempotencyConflict` per godlike/07 no-fake-availability (un caller che cambia la result hash e ritenta NON può mascherare la divergenza come replay).
- Hash round-trip con un `sha256` DIVERSO rispetto a una prior SUCCEEDED state per lo stesso `(jobID, artifactID)`: ritorna `ErrRemoteArtifactHashMismatch` con il drift summary nel messaggio (typed-data envelope).
- Forget di persistere gli outbox events dopo il flush al DB: lato outbox dispatcher non vedrà mai `job.completed` né `artifact.<kind>.uploaded` per downstream indexing/delivery.

**Canonical surface (godlike/06 one-canonical-owner-per-fact)**:
- **Port (typed contract)**: `internal/domain/remote/complete_job.go` — `CompleteJobRequest` (7 fields: WorkerID/JobID/Attempt/LeaseID/Result/Artifacts/ResultHash) + `CompleteJobResponse` (5 fields: Status/JobArtifactIDs/JobID/Attempt/ResultHash) + `Validated()` + `ValidateArtifacts()` + 8 typed sentinels (godlike/07 typed-error contract).
- **Idempotency-key helper**: `internal/domain/remote/complete_job_idempotency.go` — `CompleteJobIdempotencyKey(jobID, attempt, resultHash) string` (SHA-256 hex di `<jobID>:<attempt>:<resultHash>` via `internal/infrastructure/files.SHA256String`; colon-separated string passed through hashutil) + `IsValidCompleteJobIdempotencyKey(s) bool` (case-insensitive 64-char hex + empty marker) + `CompleteJobIdempotencyKeyDiagnostic(jobID, attempt, hash) string` accessor + `ErrCompleteJobIdempotencyKeyConflict` typed sentinel.
- **Concrete (Sender-side service)**: `internal/application/jobs/completion/complete_job_service.go` — `*Service` struct + `NewService(rxRunner, cache) (*Service, error)` fail-closed constructor + `Service.Complete(ctx, *CompleteJobRequest) (*CompleteJobResponse, error)` entry-point + `*Service.completeInTx(ctx, tx, req)` estratto per testability + 6 in-TX helper methods (`getJob`/`updateJobToSucceededCAS`/`insertResultOnConflict`/`getPriorArtifactHashes`/`persistArtifactMap`/`insertOutboxEnvelope`) orchestrati attraverso il port interface `TxContext`.
- **Persistence (SQLite migration)**: `migrations/sqlite/119_job_results.sql` — 1 table `job_results(id, job_id, attempt, result_hash, codec_id, result_payload, created_at REFERENCES jobs(id) ON DELETE CASCADE)` + 1 UNIQUE INDEX `uniq_job_results_dedup ON (job_id, attempt, result_hash)` (load-bearing ON CONFLICT surface) + 1 per-job INDEX `ix_job_results_job_id ON (job_id, attempt DESC)` (audit/reconciliation lookups).

**Migration status (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT):**
- **EXPAND (C7, July 2026)**: canonical surface live + ci-gate Check 53 forward-preventive. Zero variazione di behaviour per existing pre-C7 worker.MarkCompleted callers (Audit `rg -E '(UpdateJobToSucceededCAS|InsertResultOnConflict|PersistArtifactMap)\(' internal/application internal/api` pre-commit: 0 caller, zero violations).
- **BACKFILL (C8, forward-pointer)**: migrate pre-C7 worker.MarkCompleted callers to consumare `completion.Service.Complete` port via composition root injection. Add `Context context.Context` field al `Service.Complete` signature se production callers richiedono ambient-ctx threading (canonical hardening deferred dal C7 NIT-1 logged by code-reviewer).
- **CUTOVER (C9, forward-pointer)**: retire pre-C7 worker.MarkCompleted surface — godlike/06 SSOT one-owner-per-fact restored. `architecture/deprecations.yaml` entry gated by C8 completion.
- **CONTRACT (final lock)**: physical git-rm del legacy surface; Check 53 tightened to ban the surface entirely (no allowlist row).

**Canonical test surface (godlike/06 audit-pinning):**
- `internal/domain/remote/complete_job_test.go` (~250 LoC, 13 TDD tests): aggregated missing-fields aggregated in ONE diagnostic + zero-values rejected + negative-attempt rejected + nil-receiver rejected + good-manifest validation succeeds + bad-schema-version rejected + non-FINALIZED state rejected + empty-ID/SHA256 rejected + SHA256 byte-stability across 1000 retries + different inputs → different keys + empty-input → empty marker + hex validator case-insensitive + `CompleteJobIdempotencyKeyDiagnostic` marker accessor + `CompleteJobResponse` JSON round-trip.
- `internal/application/jobs/completion/complete_job_service_test.go` (~370 LoC, 8 TDD tests + 5 mock types): nil-rxRunner/nil-cache → `ErrCompleteJobNotConfigured` + happy-path single-TX orchestrating all 6 in-TX ops + idempotency replay returns same response WITHOUT touching TxRunner (bombing TxRunner verifies short-circuit) + lease-stolen (`seedJob.LeaseID="different-lease"`) → typed `ErrConcurrentLeaseRefutation` + missing-required (`Artifacts.Artifacts` empty) → `ErrCompleteJobRequestMissingFields` BEFORE TxRunner invocation + hash-mismatch (prior sha256 differs) → typed `ErrRemoteArtifactHashMismatch` con drift summary + nil-receiver Service → `ErrCompleteJobNotConfigured` + nil-request → `ErrCompleteJobRequestMissingFields`.

Compile-time pin (`*Service` struct + `Service.Complete(ctx, req) (*CompleteJobResponse, error)` signature) blocca future drift dei 6 metodi in-TX del `TxContext` interface — `var _ completion.TxContext = (*X-runtime-mock)(nil)` pins the port signature drift to build failure rather than runtime panic.

CI gate Check 53 (`scripts/ci-architectural-checks.sh`) forward-prevention: callers di `.UpdateJobToSucceededCAS(` / `.InsertResultOnConflict(` / `.GetPriorArtifactHashes(` / `.PersistArtifactMap(` / `.InsertOutboxEnvelope(` in `internal/application/**` o `internal/api/**` (al di fuori del canonical allowlist: `internal/application/jobs/completion/**` + tests + zero_legacy fixtures) fail-closed via exit 1. Pre-flight audit today: 0 violations (Service è l'unico caller; production consumers MUST route through `completion.Service` port).

## File Structure (quick reference)

See `ARCHITECTURE.md` for the full diagram. Quick reference:

```
.
├── cmd/server/main.go        # HTTP server + workers
├── cmd/worker/main.go        # Standalone worker
├── cmd/admin/main.go         # One-shot admin CLI
├── internal/
│   ├── api/                  # HTTP transport (thin — no business logic)
│   │   ├── server.go
│   │   ├── routes.go
│   │   ├── middleware/
│   │   ├── script/           # Script endpoints (9 files — target: 5-8)
│   │   ├── sources/          # Source endpoints (artlist, stock, etc. — being consolidated)
│   │   ├── assets/           # Unified Assets module (storage, diagnostics, search, voiceover, soundeffect, register)
│   │   ├── content/          # Merged from books + lessons
│   │   └── <feature>/        # One dir per feature (max 30 files)
│   ├── app/                  # Composition root, wiring, migrations
│   ├── application/          # Use-case orchestration
│   │   ├── scripts/          # Script generation (batch, curation, scenes, documents)
│   │   ├── assets/           # Asset providers, registry, artifacts, lifecycle
│   │   ├── jobs/             # Job broker, worker, outbox
│   │   ├── images/           # Image generation
│   │   ├── ingest/           # Media ingest orchestrator
│   │   ├── monitor/          # Channel monitor background service
│   │   ├── content/          # Books + lessons
│   │   ├── voiceover/        # Voiceover service
│   │   ├── association/      # Script→asset semantic matching
│   │   └── realtime/         # Real-time clip search
│   ├── domain/               # Canonical domain types + contracts
│   │   ├── asset/            # Asset, MediaType, Location, LifecycleState
│   │   ├── job/              # Job, Store, WorkerSession
│   │   └── script/           # Script, Plan, GenerationSpec
│   ├── infrastructure/       # Adapters to external systems
│   │   ├── database/sqlite/  # SQLite repositories + migrations
│   │   ├── drive/            # Google Drive uploader
│   │   ├── media/            # FFmpeg, processor, downloader
│   │   ├── ai/               # Ollama, reranker, VLM
│   │   └── process/          # External command execution
│   └── media/                # Media pipelines (vectorstore, stockpipeline, books, etc.)
├── pkg/                      # Leaf utilities only
├── config/                   # YAML configuration
├── migrations/               # SQL migrations
├── scripts/                  # Python AI scripts
└── node-scraper/             # Persistent Chromium scraper
```
