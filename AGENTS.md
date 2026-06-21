# AGENTS.md - PipelineGen System Documentation

## Overview
PipelineGen is a Go-based backend service that manages media processing pipelines for YouTube clips and Artlist assets. It runs as a systemd service on **port 8080** by default. Override at runtime via `VELOX_PORT`; the in-tree default is set by `internal/infrastructure/config/types.go::Server.Port` and mirrored by every client (`pkg/veloxclient`, worker fallback, scripts) so a single env var changes both sides.

### Port policy (Operational Readiness PR, June 2026)

The HTTP listen port is configurable via `VELOX_PORT` (server) and `VELOX_BROKER_URL` (worker + clients). No port number is hard-coded outside `cfg.Server.Port`'s default tag. Operator overrides are honoured at:

- `cmd/server` (the canonical binary) via `cfg.Server.Port`.
- `cmd/worker` via `VELOX_BROKER_URL` (fallback: `VELOX_PORT`-derived URL).
- `pkg/veloxclient` via `baseURL` argument or `VeloxClient(base_url=...)` in `scripts/velox_client.py`.
- Shell scripts (`scripts/diagnostics/marker_audit.sh`, `scripts/rotate_token.sh`, `Makefile` `doctor`/`artlist` targets) honour `API_BASE` / `VELOX_PORT` env vars.


## Documentation Map

- **This file (AGENTS.md)**: Critical rules and instructions for all agents
- **docs/api-package-boundaries.md**: Target API structure, dependency rules, size limits, migration plan
- **docs/images/GEMINI.md**: Image generation strategy and Go-Python integration
- **google-accounting/GEMINI.md**: Python automation details and image capture logic
- **docs/INTELLIGENCE_ROADMAP.md**: Roadmap for advanced AI features and Hybrid Search evolutions
- **docs/archive/sqlite-databases.md**: Complete database schema, boundaries, and migration strategy
- **README.md**: Project structure and architecture overview
- **PROJECT_GUIDE.md**: Italian language getting started guide

## Instructions

- **Non cambiare driver SQLite** (rimanere su `mattn/go-sqlite3`)
- **Non lavorare su FTS5** (il supporto dipende dal driver compilato, usare fallback LIKE)
- **Concentrarsi solo su schema boundaries, diagnostics e test**
- **Ogni database deve avere solo le tabelle necessarie** al servizio che lo usa
- **Non applicare migration generiche a più database se creano tabelle non usate da quel database.**
- Schema attuale (Unificato):
  - `data/media/media.db.sqlite`: **Unico database** — tutto in un solo file (scripts, jobs, asset_index, media_assets, harvester, pipeline_runs, voiceovers, etc.)

## Qdrant Entity Associations

PipelineGen uses Qdrant vector database to power semantic search across all
media types. Here's how entity associations work:

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
the project root. This file is the canonical architecture doc.

Key contract files:
- `internal/domain/asset/` — canonical asset types + contracts (migrating from `internal/core/`)
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

**For the full table of endpoints, schema, modes, and migration notes,
see `docs/CHANGELOG_2026-06-03.md` §0.** The detailed data flow and
pipeline diagrams live in `ARCHITECTURE.md` §3 and
`docs/SCRIPT_PIPELINE.md` §3. This file does not duplicate them.

**Rule of thumb for new integrations**: scegli l'endpoint in base al preset di flag desiderato.

| Endpoint | Handler | Job type | Preset del payload |
|----------|---------|----------|--------------------|
| `POST /api/script/generate-from-clips` | `ScriptFlowHandler.GenerateFromClips` (`handler_clip_source.go`) | `script.generate_from_clips` | Rispettano i flag del body. `generate_metadata=true` implica `extract_entities=true`. Default `sentences_per_image=10`. |
| `POST /api/script/generate-with-images` | `ScriptFlowHandler.GenerateWithImages` (`handler_generate_with_images.go`) | `script.generate_from_clips` (stesso) | **Forza** `extract_entities=false`, `generate_scene_images=true`, `generate_metadata=false`. Default `sentences_per_image=8`. |

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

### Drive Token Regeneration
If Google Drive authentication fails:
```bash
python3 scripts/generate_drive_token.py
```

---

## Legacy Directories Policy

These directories are **migration‑only**: they should disappear from the
codebase once their consumers have been moved. Each new PR that touches
one of them must be a *migration* (moving symbols outward) or a
*removal* — never an addition.

**Rule of thumb**: a directory on this list **cannot receive**:

- new files,
- new exported types,
- new repositories,
- new handlers,
- new business logic of any kind.

It **may receive**:

- edits to existing files that migrate a symbol outward,
- deletions (this is the desired direction),
- tests that cover the migration surface.

The deny‑list is enforced by `scripts/ci-architectural-checks.sh` Check 13.
A PR that tries to add a new `.go` file in any of these directories will fail
the CI guard with the migration target printed inline.

| Legacy directory | Migration target | Owner |
|---|---|---|
| `internal/core/`                  | `internal/domain/asset/` (contracts) or `internal/infrastructure/<X>/` (concrete) | TBD |
| `internal/media/<feature>/`       | `internal/domain/asset/<feature>/` (model) or `internal/application/<feature>/` (use case) | TBD |
| `internal/assets/`                | `internal/domain/asset/`                                       | TBD |
| `internal/artifacts/`             | `internal/domain/job/` (artifacts is interface‑wrap; eliminate entirely) | TBD |
| `internal/sources/{youtube,artlist}/` | `internal/application/assets/providers/<provider>/`        | TBD |
| `internal/upload/drive/`          | `internal/infrastructure/drive/`                               | TBD |
| `internal/application/scriptflow/` | `internal/application/scripts/` (flat-merge completato; directory eliminata) | Wave 6 ✅ |
| `internal/domain/media/`          | `internal/domain/asset/`                                       | TBD |
| `internal/domain/worker/`         | `internal/domain/job/`                                         | TBD |
| `internal/domain/outbox/`         | `internal/domain/lifecycle/`                                   | TBD |

Per‑directory migration manifests live in `docs/migration-maps/<dir>.md`.



## Rebase-Conflict Lesson (June 2026)

When `git pull --rebase` hits a conflict on a **test file** (or any
file where both local and remote added independent hunks), prefer
**manual merge inspection** over `git checkout --ours` / `--theirs`.

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

Safe procedure when a **test file** conflicts during rebase:

1. `git rebase --abort` and start fresh.
2. `git diff --name-only origin/<branch> HEAD` to list the conflict
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

**Anti-pattern**: a loop of
`pull --rebase, conflict, checkout --ours, commit --amend, push, non-fast-forward, ...`.
Each `commit --amend` creates a fresh commit hash that re-diverges
from `origin/<branch>` and triggers the next failure. If you find
yourself in that loop:

- **First, try the cheap exit**: stop amending, run a clean
  `git fetch && git rebase origin/<branch>`. If the resulting tree
  is a clean fast-forward over `origin/<branch>`, an ordinary
  `git push origin <branch>` will land it — no force-push needed.
- **Only if the tree is genuinely divergent** (e.g. you and
  another agent both landed on the same branch in the interim) is
  `git push --force-with-lease origin <branch>` appropriate, and
  then **only after** running
  `git fetch && git log --oneline HEAD..@{u}` to confirm no
  in-flight commit from another agent (commits in `origin/<branch>`
  that you don't have locally) is about to be clobbered. The
  reverse view, `git log --oneline @{u}..HEAD`, lists your own
  unpushed commits — useful for an audit, **not** a clobber-check.

Canonical reference: the obsolete-batch-tests disposition shipped
in commits `39071b40` + `a55e38f1` is the case where this lesson
was learned.


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


## Git-Lesson-2 (June 2026) — `--no-ff` merge vs rebase on shared branches

The choice between rebase and a `--no-ff` merge comes down to
**what kind of merge you're doing**:

- **Pulling remote updates into your local branch**:
  `git pull --rebase` is **safe**. It replays your local,
  unpushed commits on top of the remote tip and only rewrites
  **your** copy; it never touches the shared branch itself.
- **Integrating a completed feature branch into `main`**:
  use `git merge --no-ff <feature-branch>`. The non-fast-
  forward flag preserves the merge commit so the audit trail
  shows **when** and **what** was integrated even after the
  feature branch is deleted. Plain `git merge` quietly
  fast-forwards and erases that signal.
- **What is actually dangerous on shared branches**:
  `git push --force`. Force-pushing rewrites remote history
  and invalidates copies held by anyone who already pulled.
  Use `git push --force-with-lease` only as the explicit exit
  from the amend-loop anti-pattern documented in the
  [`Rebase-Conflict Lesson`](#rebase-conflict-lesson-june-2026).

**Default for PipelineGen** (Operational Readiness PR):
`main` is the active integration branch. Local work on a
topic branch → `git pull --rebase` to stay current →
ordinary `git push` of the topic branch → open PR/MR →
host squash-merge, or local `git merge --no-ff` to land.
Never `git push --force` against `origin/main`.


## Git-Lesson-3 (June 2026) — `Co-authored-by:` trailers for agent commits

Git parses **multiple `Co-authored-by:` trailers** in the body
of a commit message and credits each author in `%(trailers)`
formatting. This is the canonical way to mark a commit as an
agent amend: the trailer keeps the agent's work attributable
to the agent's identity in **local logs** (`git shortlog`,
`git log --author=<agent>`, `git log --format='%(trailers)'`).

Convention for agent commits in this repo:

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
```

**Caveat**: the `@pipelinegen.local` email is for **local-log
attribution only**. GitHub and GitLab credit contributor
avatars by **registered** email; unrecognised domains
(anything not a `noreply.github.com` or verified-domain
alias) will not render on the host's social graph. Use this
trailer for internal audit; if you also need GitHub avatars,
add the agent's verified email through the host's
collaborator UI.

**Format rule**: trailers must appear after a **blank line**
following the body. A `Co-authored-by:` line in the subject
is NOT parsed as a trailer. Verify with:

```
git log --format='%(trailers)' -1 <sha>
```

after committing. Empty output means the trailer landed in
the wrong place.


The CI check (`scripts/ci-architectural-checks.sh` Check 1) bans bare
`context.Background()` in `internal/api/` handlers. The following sites are
**intentionally exempt** per ARCHITECTURE.md §7:

| Site | Reason |
|------|--------|
| `internal/api/handlers/script/handlers/postwrite.go` | Post-write save context (30s timeout) — must survive client disconnect |
| `internal/service/gemmamemory/service.go` | Post-write save context (30s timeout) |
| `internal/service/scriptcore/write_script.go` | Post-write save context (30s timeout) |
| `internal/jobs/worker.go` (finalizationCtx, lines ~142-146) | Finalization context for job outcome persistence (30s timeout) — must survive handler timeout so the worker can still mark the job as failed/completed/dead-lettered in the DB. Detached from `jobCtx` by design; detaching from `ctx` (worker lifecycle) would lose the outcome if the worker is shut down mid-job. |
| `internal/app/init_core.go` | Top-level composition root (no parent context exists) |
| `internal/api/server.go` | `signal.NotifyContext()` — canonical Go pattern |
| `internal/api/module_base.go` (line ~105) | Rollback context for module startup failure — must survive parent cancel so Stop() can run |
| `internal/service/translations/cache.go` | Defensive fallback when parentCtx is nil |
| `internal/sources/artlist/search_cache.go` | Defensive fallback when parentCtx is nil |

---

## Migration Status (Brutal Care Plan)

### Completed (June 2026)
- ✅ Database Consolidation (all tables → `media.db.sqlite`; `media.db.sqlite` removed as unused)
- ✅ Eliminated `assetpipeline` thin wrapper
- ✅ Migrated `workflowrunner.results` → job system
- ✅ Migrated `assetdestination.Resolver` → `core/destination.Resolver`
- ✅ Migrated `mediaasset.Processor` → `core/processor.Processor`
- ✅ Consolidated `internal/core/media/` unified models
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

### Still Pending
- Remove any remaining duplicates in legacy doc folders

---

## Core Contracts

All modules must use canonical contracts:
- `internal/domain/asset/` — Asset, MediaType, Repository, Processor, Destination (migrating from `internal/core/`)
- `internal/domain/job/` — Job, Store, WorkerSession
- `internal/domain/script/` — Script, Plan, GenerationSpec
- All long-running operations must use `internal/application/jobs/` service

**Note**: `internal/core/` still exists as a legacy package. New code should prefer
`internal/domain/` contracts and `internal/application/` use cases. See
`architecture/migration.yaml` Wave 4C for the migration plan.

---

## 🧰 Utilities to prefer

Prima di scrivere custom code, **controlla se esiste già in `pkg/`**. Ogni utility è leaf-only (zero import da `internal/`); `pkg/` è dove cerchi prima di replicare logica. Regola pratica: se stai per incollare 20+ righe di helper, prima `grep` qui sotto.

| Scenario | Pacchetto | Helper chiave (preferisci questo invece di custom) |
|---|---|---|
| Default coalesce (string/int/float) | `pkg/defaults` | `String(val, fallback)`, `Int(val, fallback)`, `Float64(val, fallback)` |
| Retry con backoff esponenziale | `pkg/retry` | `Do(ctx, fn, opts)`, `DoWithValue[T](ctx, fn, opts)`, `DefaultOptions()` |
| Hash content / ID generation | `pkg/hashutil` | `SHA256String(s)`, `RandomString(n)`, `MD5File(path)`, `HashFile(path, h)` |
| Correlation ID propagation (job/script) | `pkg/corid` | `WithCorrelationID(ctx, id)`, `FromContext(ctx)` — usalo nel middleware request ID e nell'enqueue job |
| Pointer utilities | `pkg/ptrutil` | `Ptr[T](v)`, `DerefOr[T](p, fallback)` |
| Text/slug/voiceover cleanup | `pkg/textutil` | `Slugify`, `SlugifyWithMax`, `CountWords`, `Truncate`, `CleanForVoiceover`, `FirstNonEmpty(...)`, `ParseVTTTimestamp`, `SplitScriptSentences` |
| File I/O JSON + filesystem | `pkg/fileutil` | `WriteJSON(path, v, indent)`, `ReadJSON(path, v)`, `CopyFile`, `CleanFolderName(s)`, `UsableCachedClip(path)` |
| Gin HTTP helpers (handler) | `pkg/apiutil` | `BindJSON[T](c)`, `OK(c, data)`, `BadRequest(c, msg)`, `InternalError(c, err)`, `NotFound`, `Error(c, status, msg)`, `ClampLimit(v, def, max)` |
| Pagination & job utilities | `pkg/handlerutil` | `ParsePagination(defaultLimit, maxLimit)`, `AsyncJobResponse(c, job, msg)`, `EnqueueAsync`, `ParseJobStatusFilter` |
| Concurrency / errgroup+panic | `pkg/concurrent` | `WithContext(parent)`, `ParallelMap[T,U]`, `SafeGo(name, fn)` (sostituisce WaitGroup+Mutex+recover custom) |
| Slice primitives | `pkg/sliceutil` | `UniqueStrings`, `UniqueStringsCI`, `MinInt(a, b)`, `Clamp(v, lo, hi)`, `GroupSentences`, `NormalizeAndDedupe`, `MergeNormalizedLists` |
| SQL fallbacks (FTS5 bandito) | `pkg/sqlutil` | `BuildFallbackLikeConditions(tokens, cols)`, `BuildFallbackLikeConditionsOR` |
| YouTube URL / Drive link parse | `pkg/urlutil` | `ExtractVideoID(raw)`, `FileIDFromDriveLink(raw)` |
| Path/folder naming | `pkg/pathutil` | `SafeFolderName(name)`, `BuildTimestampedSlug`, `ExtractStyleFromPath(relPath)` |
| Term/name parsing + topic match | `pkg/termutil` | `SubjectMatchesTopic`, `ExtractLikelyNames`, `TermsFromText`, `TopicTokens` |
| Similarity math | `pkg/similarity` | `Jaccard(a, b)`, `TokenSet(text)`, `OverlapRatio(startA, endA, startB, endB)` |
| Matching thresholds config | `pkg/matchingconfig` | `LoadMatchingConfig(path)` — **nicho**, solo se tocchi semantic/similarity scoring |

| Test helpers | `pkg/testutil` | `MustMarshalJSON(t, v)` |
| Job HTTP client riusabile | `pkg/veloxclient` | `New(baseURL, token)` → `SubmitAsync`, `GetJobStatus`, `IsTerminal` |
| Time RFC3339 | `pkg/timeutil` | `ParseRFC3339`, `FormatNow`, `ParseRFC3339PtrString` |
| External process exec | `pkg/executil` | `Run(ctx, name, args, opts)`, `RunSimple`, `LookPath`, `CommandExists` |

**Servizi interni riusabili** (non reinventare la ruota — vietato duplicare logica):

| Scenario | Servizio | Path |
|---|---|---|
| Enqueue/poll/cancel async | `jobs.Service` | `internal/jobs/` — sempre per ogni long-running (>5s) |
| Vettori Qdrant (interfaccia canonica) | `vectorstore.Service` | `internal/media/vectorstore/` — mai HTTP diretto |
| Reranker CrossEncoder BGE-reranker-v2-m3 | `reranker.Client` | `internal/reranker/` |
| Embeddings/chat LLM (con retry + fallback) | `ollama.client.Client` | `internal/ml/ollama/client/` |
| Read media_assets | `clips.Repository` | `Removed: clips/` — GetClip, SearchByTags |
| Script generation core | `scriptcore.Engine` | `internal/service/scriptcore/` — WriteScript |
| Script→asset semantic match | `association.Service` | `internal/media/association/` |
| Real-time clip search (post-Qdrant) | `realtime.Service` | `internal/media/realtime/` |
| Topic-by-DB routing (folder risoluzione) | `voiceover.GroupsResolver` | `internal/media/voiceover/` |
| Salva con idempotency / outbox | `outbox.Dispatcher` | `Removed: outbox/` |
| Google Drive upload / Doc creation | `drive.Uploader`, `drive.DocClient` | `internal/upload/drive/` |
| Channel monitor background | `monitor.ChannelMonitor` | `internal/media/monitor/` |

> **Regola**: se l'utility corretta non è in questa tabella ma `grep` mostra un duplicato (la stessa funzione implementata in >1 posto), PRIMA estrarla in `pkg/<x>/` poi consumarla. Esempio realistico visto nel codice: 4+ implementazioni di "retry con backoff" sono state collassate in `pkg/retry` — stessa opportunità per qualsiasi altra duplicazione che trovi.

---

## ✂️ Modular edit patterns

Quando modifichi il codebase, **modularizza**: una decisione per sezione, una modifica per file, niente "monkey patch" nel posto sbagliato. Questi pattern sono osservati dalla codebase esistente e dai CHANGELOG.

### Pattern 1 — Aggiungere un HTTP handler

1. Crea `internal/api/<feature>/<file>.go` (un handler per feature, 5-8 file max).
   Per la struttura target vedi `docs/api-package-boundaries.md`.
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

**⚠️ Il flattening di Giugno 2026 ha risolto i file enormi ma ha creato un
mega-package da 153 file in `internal/api/`. La regola corretta è:**

1. **Prima dividi per capability stabile**, poi dividi i file all'interno.
2. Un package API **non deve contenere business orchestration** — solo transport HTTP.
3. `internal/api/` root deve restare sotto **15 file produttivi**.
4. Una directory con oltre **30 file produttivi** richiede architecture review.
5. Oltre **40 file produttivi** il CI deve fallire (salvo allowlist documentata).
6. Ogni feature API espone al massimo **1 Handler** principale e **1 funzione** di registrazione route.
7. Le feature API **non possono importarsi tra loro**.

**Struttura target**: vedi `docs/api-package-boundaries.md`.

Vecchia regola (deprecata — portava al mega-package):

> ~~Quando un file supera ~300-400 righe o ha >2-3 responsabilità distinte, crea un file per concetto nello stesso package.~~

Esempi già fatti (stato a Giugno 2026 — numeri verificati via `ls`): channel_monitor (11 file in `internal/media/monitor/`), extractor_process (10), handler_batch_phases (13), clipindexer (6), voiceover (11). **← valori snapshot**: se il numero è cambiato, rifai `ls <dir> | wc -l` per verificarlo; aggiorna qui quando splitti un nuovo file.

### Pattern 6 — Modificare una request o payload struct

Quando aggiungi un campo a una request API o a un job payload (caso reale: Bug A di `generate_timeline` perso silenziosamente — vedi CHANGELOG):

1. **3 posti da aggiornare**:
   - Handler request type (`types_<domain>.go`)
   - Job payload unmarshalable (`jobPayload<X> struct` o equivalente)
   - Worker struct/logica che legge il campo e agisce
2. Aggiungi sempre con `omitempty` o zero-value safe per retro-compatibilità (es. `MinQualityScore float64` con check `> 0`).
3. Test round-trip: scrivi un test `json.Marshal → json.Unmarshal` che verifica che il campo sopravvive.
4. **Mai** aggiungere un campo che il worker legge ma il handler non scrive — finisce come "perso silenziosamente" (questo è esattamente Bug A).
5. Se il campo ha impatto reale, esegui un job reale per verificare end-to-end (usa `pkg/veloxclient` o `scripts/velox_client.py` — vedi `docs/integrations/cross-worker-jobs.md`).

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

| Tu vuai... | Usa questo (vedi sezione sopra per il path completo) |
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
`internal/core/` o `internal/application/`.

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

Documento completo: `docs/api-package-boundaries.md`.

---

## File Structure (quick reference)

See `ARCHITECTURE.md` for the full diagram and `docs/api-package-boundaries.md` for
the target API structure. Quick reference:

```
.
├── cmd/server/main.go        # HTTP server + workers
├── cmd/worker/main.go        # Standalone worker
├── cmd/admin/main.go         # One-shot admin CLI
├── internal/
│   ├── core/                 # Legacy — canonical contracts (migrating to domain/asset/)
│   ├── api/                  # HTTP transport (thin — no business logic)
│   │   ├── server.go
│   │   ├── routes.go
│   │   ├── middleware/
│   │   ├── script/           # Script endpoints (32 files — target: 5-8)
│   │   ├── sources/          # Source endpoints (26 files — target: 5-8)
│   │   ├── assets/           # Merged from scraper + mediaingest
│   │   ├── content/          # Merged from books + lessons
│   │   └── <feature>/        # One dir per feature (max 30 files)
│   ├── app/                  # Composition root, wiring, migrations
│   ├── application/          # Use-case orchestration (new, growing)
│   │   ├── scripts/          # Script generation (batch, curation, scenes, documents)
│   │   ├── assets/           # Asset providers, registry
│   │   ├── jobs/             # Job broker, worker, outbox
│   │   ├── images/           # Image generation
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
│   │   ├── media/ffmpeg/     # FFmpeg wrappers
│   │   ├── ai/               # Ollama, reranker, VLM
│   │   └── process/          # External command execution
│   ├── media/                # Media pipelines (legacy — migrating to application/ + infrastructure/)
│   ├── sources/              # Artlist + YouTube providers (legacy — migrating to providers/)
│   ├── upload/               # Drive upload (legacy — migrating to infrastructure/drive/)
│   ├── assets/               # Legacy asset package (migrating to domain/asset/)
│   └── artifacts/            # Legacy artifact package (migrating to domain/job/)
├── pkg/                      # Leaf utilities only
├── config/                   # YAML configuration
├── migrations/               # SQL migrations
├── scripts/                  # Python AI scripts
├── node-scraper/             # Persistent Chromium scraper
└── docs/                     # Technical documentation
```
