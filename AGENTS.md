# PipelineGen Engineering Rules

## Mission

PipelineGen is a headless, server-side media pipeline. Keep it deterministic, CPU-friendly, modular, and suitable for CLI, workers, and backend automation. Do not turn it into a browser application or GUI editor.

## Non-negotiable architecture rules

- **State-store SSOT by domain (media cutover September 2026):**
  - **PostgreSQL + pgvector is the SOLE durable authority for the media domain** (`media_assets`, `asset_locations`, `media_asset_features`, `media_embeddings`, `asset_text_tracks`, `asset_renditions`, `media_asset_sources`, `registry_events`, `media outbox`). No new SQLite media repository, table, or write may be introduced; `percheck_media_assets_writer_canonical` enforces the single-writer gate.
  - **SQLite is the durable authority ONLY for the explicitly enumerated non-media operational domains** (jobs, delivery_log, scripts, cache, idempotency, artifacts/staging, observability). Do not introduce SQLite media tables or media writes outside migration/backfill allowlists.
  - **Qdrant is NOT a media store.** It remains only for explicitly justified non-media consumers (mediamemory, maintenance DR, admin audit tooling) and must never be used as a media read/write or projection. Qdrant media projections are retired; an empty media projection surfaces `INDEX_UNAVAILABLE/REBUILD_REQUIRED`, never a fallback collection.
  - Keep `mattn/go-sqlite3`; do not introduce FTS5 assumptions.
- Durable side effects after database commits must use the transactional outbox.
- **Media-domain SSOT cutover (September 2026, DEMOLITION COMPLETE):** PostgreSQL + pgvector is the durable authority for the media domain (`media_assets`, `asset_locations`, `media_asset_features`, `media_embeddings`). The canonical write gate is `persistence.AssetCommitter` — implemented ONLY by `PostgresMediaCommitter` (the SQLite media writer family `SQLiteAssetCommitter`/`SQLiteMediaCommitter` is REMOVED from the codebase; SQLite retains only non-media mutation primitives). Every asset commit (YouTube, Artlist, local, voiceover, images, recovery) MUST route through `AssetCommitter.CommitAndIndex` / `CommitTx`; direct SQL writes to `media_assets` outside the canonical committer are banned and enforced by the `percheck_media_assets_writer_canonical` CI gate. That gate scans `internal/` AND `cmd/` and covers `INSERT`/`REPLACE`/`UPDATE` plus `DELETE FROM media_assets`, so the ban holds for the admin CLI too; the last two `cmd/admin` media writers (`maintenance/unify_catalogs.go`, `maintenance/stock_reset.go`) were retired to fail-closed typed stubs because a SQLite media write would desynchronise the PostgreSQL SSOT. Vector search on the media plane is owned by pgvector (`internal/platform/postgres/media.MediaSearcher` implements the canonical `search.VectorStorePort`); **Qdrant media reads and writes are forbidden** — the SQLite outbox registers no media index handler in ANY mode (`registerOutboxCoreHandlers` is a deliberate no-op that only logs this invariant); the legacy Qdrant media consumer `internal/capabilities/jobs.IndexingHandler` has ZERO production callers; `platform/qdrant/indexing/clipindexer` is now a PURE PostgreSQL-outbox delegator — `IndexAsset`/`IndexClip` call ONLY `canonicalIndexRequester.RequestIndex` (`SetCanonicalIndexRequester`, wired in the composition root) and fail closed when it is unwired; the legacy SQLite→Qdrant implementation (`setIndexedAt`, `indexAsset`, `tryFastPath`, `finalizeIndex`, `indexViaAPI`, `shouldSkipByName`, `computeContentHash`) was DELETED on 2026-09-20 (MEDIA LEGACY READ-PLANE DEMOLITION) together with its six `internal/platform/qdrant/indexing/clipindexer/` inventory entries and its two `percheck_control_plane_sql_writes` allowlist entries, so the package no longer performs any media_assets SQL read or INDEXED write; the `media.reindex` job binding / `media.TypeReindex` were removed earlier. The former media Qdrant WRITE surfaces are RETIRED: `cmd/admin reindex-qdrant` (SQLite→Qdrant rebuild of the `media_assets` production collection) fails closed with a typed error, the SQLite→Qdrant media parity ticker (`projection-reconciler`) was removed together with its metric family and config key, and `CollectionManager.PrepareProductionCollection`/`ActivateProductionCollection` were deleted; the media index plane is `pgmedia.PostgresIndexWorker` (embed → pgvector upsert → INDEXED → outbox completed, all in the media SSOT) and the canonical media committer emits `asset.index.requested` into the PG outbox. The generic SQLite outbox stays for Drive/webhook/async side effects only. Derived surfaces (`media_asset_features`, `media_embeddings`) are written only by the enrichment pipeline (`MediaFeatureAnalyzer`, `VisualEmbeddingPipeline` with `VisualEmbeddingModelRegistry`) through `pgmedia.VectorSurfaceWriter`; embedding families are fail-closed gated by the `media_embedding_families` registry + DB trigger, and the production families (semantic `intfloat/multilingual-e5-base` 768d, visual `google/siglip-so400m-patch14-384` 1152d — the model identity is owned by `internal/kernel/models`, which every consumer derives from) carry real per-family HNSW ANN indexes (migration `003_media_hnsw_indexes.sql`, EXPLAIN-proven by `TestHNSW_VectorSearchPlansIndexScan`). Qdrant itself is NOT globally removed: mediamemory, maintenance DR, and admin audit tools still legitimately use it outside the media domain. Certification: the historical driver `scripts/ci/certify-media-cutover.sh` (and its `certify-storage` / `certify-data-layer` / `certify-rust-migration` siblings) was DELETED by commit `7e6965aab` ("purge 94% shell"); the six `make certify-*` targets that could only fail closed were RETIRED on 2026-09-13 (a target that cannot pass is not a gate), so `make certify-media-cutover` is now simply an unknown target. The live enforcement for this axis is: `TEST_POSTGRES_DSN=… go test ./internal/platform/postgres/media/ -count=1` plus `go run ./cmd/archcheck --strict` (hard gates `percheck_media_assets_writer_canonical` — the WRITE side, `percheck_sqlite_media_reader_ban` — the READ side, promoted on 2026-09-13 from the retired `certify-media-cutover` counter `SQLITE_MEDIA_READERS=0`, plus `percheck_asset_committer_event_ssot`, `percheck_indexed_state_writer_ssot`, `percheck_upsert_points_sole_owner`; green as of 2026-09-13). `percheck_sqlite_media_reader_ban` bans any NEW non-test **SQLite** reader of `media_assets`: each read is classified by the dialect of its enclosing SQL expression, so a PostgreSQL `$N`-bound reader is never reported as debt (the counter is `SQLITE_MEDIA_READERS=0`, not readers in general). Exempt are the PostgreSQL media SSOT (`internal/platform/postgres/`) and THREE exact-file registers (no directory is exempt as a whole any more: every legacy read plane was converted from a path prefix into an enumerated inventory on 2026-09-20) in `cmd/archcheck/scan/boundaries/percheck_sqlite_media_reader_ban.go`: `sqliteMediaReaderGrandfatheredFiles` (production split-brain debt, which ratchets to zero — migrating a consumer means deleting its entry in the same change) and `sqliteMediaReaderDegradeOnlyFiles` (documented media-disabled paths whose read is selected only when the media SSOT is closed, each entry naming its selector) and `sqliteMediaReaderInventoriedZoneFiles` (the inventory of the legacy zones CONVERTED from a path prefix — `internal/platform/sqlite/`, `internal/platform/qdrant/indexing/` and `cmd/admin/`, all on 2026-09-20, each of whose prefix left `sqliteMediaReaderGrandfatheredZones` in the same change, so a NEW file under any of them is now a violation instead of inheriting the exemption). All three registers are pinned one-directionally: a missing file, a STALE entry that has no remaining SQLite-dialect read, or an entry present in two registers fails the build; and the two ratcheted registers additionally fail on ANY entry, since both have reached their terminal empty state.
- **Data-layer unification (August 2026, non-media domains):** for domains still on SQLite, the sync direction is ALWAYS and ONLY `SQLite → Outbox → projection consumer`. Bidirectional sync is forbidden. Qdrant may remain ONLY outside the media domain; any residual Qdrant usage must be explicitly justified. An empty media projection must surface `INDEX_UNAVAILABLE/REBUILD_REQUIRED` in migration-mode deployments, never a fallback to a recovery collection. The former `recover-registry-from-qdrant` tool is **RETIRED**: neither it nor the `cmd/admin/emergency/` directory exists in the tree, and its premise (rebuilding the media registry from a Qdrant projection) is void now that Qdrant is not a media store. Media disaster recovery goes through the PostgreSQL SSOT (backup/restore) — do not describe that tool as available.
- The only target internal roots are `internal/app`, `internal/kernel`, `internal/capabilities`, and `internal/platform`.
- `internal/app` is the only composition root and owns lifecycle/wiring, not business behavior.
- `internal/capabilities` owns business capabilities and typed ports; `internal/kernel` owns genuinely shared semantic contracts.
- `internal/platform` owns concrete adapters, transport mechanics, filesystem/process access, and external systems.
- The legacy roots `internal/application`, `internal/api`, `internal/infrastructure`, and `internal/domain` NO LONGER EXIST (verified absent 2026-09-13; `architecture/policy.yaml` already declares `legacy_internal_roots` intentionally absent). Re-creating any of them is a violation, not a migration: the forward-prevention gates `percheck_legacy_root_new_code`, `percheck_legacy_root_ban`, and `percheck_api_infrastructure_imports` fail closed. New code belongs under `internal/{app,kernel,capabilities,platform}`.
- Google Drive writes from application flows must use the canonical delivery publisher.
- Never represent an unavailable backend as a successful no-op. Fail closed with typed errors or do not register the capability.
- New routing, provider selection, source policy, sampling, or resolution logic must enter a shared registry, resolver, or sampler. Do not duplicate the same decision logic across handlers.
- Do not add features to production code unless the user explicitly requested them.

## Data and migration rules

- Apply migrations only to the database that owns the affected tables.
- Prefer expand, backfill, cutover, contract for compatibility changes.
- Version Qdrant projections with content, schema, preprocessing, and model versions rather than file hash alone.
- Preserve deterministic asset IDs and idempotent job/outbox keys.

## Operational rules

- Keep services headless and CPU-first unless a GPU path is explicitly requested.
- Never commit credentials, tokens, cookies, or private keys.
- Generated API documentation must match registered routes.
- Run `make verify-main` before pushing (the pre-push hook runs `make -j4 verify-main` automatically — parallel legs, overridable with `VERIFY_MAIN_JOBS`).
- DO NOT use `git push --no-verify` to bypass the pre-push gate — bypass is reserved for unblocking CI emergencies and must be paired with a fixup! followup.
- **Agent constraints during development**: NON eseguire `make verify-main` durante le iterazioni. Gerarchia dei gate: `verify-agent` (loop agent, dopo ogni modifica) → `verify-fast` (milestone) → `verify-main` (UNA sola volta, prima del push). Dopo ogni modifica:
    1. Esegui il test del package modificato, es. `go test ./internal/capabilities/audio/... -count=1` (oppure `-run TestNome` per un singolo test).
    2. Esegui `make verify-agent` (foundation + static + verifica dei soli componenti impattati; sonda Node + architecture solo se cambiano `make/**`/hook — ~1-3 min).
    3. Esegui `make verify-fast` solo dopo una milestone significativa.
    4. Quando TUTTO il task è completo, esegui UNA SOLA VOLTA `make verify-main`, immediatamente prima del push.
    5. Se `verify-main` è già passato e working tree/commit non sono cambiati, NON ripeterlo.
    6. Non eseguire `auth-check`, `make dev` o `make run` salvo richiesta esplicita.
    7. Non scartare modifiche locali non correlate.
    Esempi concreti del loop rapido (punti 1-2), `-count=1` evita risultati in cache:
    - `go test ./internal/capabilities/scripts/... -count=1` — il solo package modificato.
    - `go test ./internal/capabilities/audio/... -run TestCompileRejectsInvalidAudioInputs -count=1` — un singolo test.
    - `go test ./internal/platform/media/rustexec/... -count=1`
    - `go test ./internal/capabilities/assets/providers/stock/... -count=1`
    - quindi `make verify-agent` — foundation + static + verifica dei soli componenti impattati (~1-3 min).

## Authentication SSOT (Velox admin token)

PipelineGen has exactly one canonical source for admin credentials. New code and scripts MUST honor this contract.

- **Canonical secret file**: `/etc/pipelinegen/pipelinegen.env` (mode `0640`, owner `root:pipelinegen-agents`).
- **Required contents**: at minimum `VELOX_ADMIN_TOKEN=<64-char-hex>`; optionally `VELOX_WORKER_TOKEN=<64-char-hex>` and `VELOX_PORT=8000`.
- **Canonical variable name**: always `VELOX_ADMIN_TOKEN`. Do not introduce `ADMIN_TOKEN`, `X-Admin-Token`, hard-coded literals (`test-admin-token-12345` is forbidden), or alternate env-file locations.
- **Loader convention**: agents never read the file directly. Use `scripts/with-velox-auth` (loads, validates `^[a-fA-F0-9]{64}$`, exports, and `exec`s) or set `TOKEN_FILE=/etc/pipelinegen/pipelinegen.env` (exported by the top-level Makefile).
- **Pre-flight gate**: `make auth-check` runs `scripts/with-velox-auth` against `/api/artlist/job-consumer` and fails closed on non-200 — no token is ever printed.
- **Live verification gates: exactly one (2026-09-13).** `make verify-live` and every member battery (`verify-artlist-live`, `verify-images-live`, `verify-script-live`, `verify-vidrush-live`, `verify-stock-live`, `verify-artlist-scale-live`, …) invoked shell drivers deleted by commit `7e6965aab`; the targets, their CI jobs (`.github/workflows/{ci,nightly,manual}.yml`) and the stale docs were removed together. A target whose only possible outcome is "No such file or directory" is not a gate. The single live gate whose driver is a tracked artifact is the **10-step pipeline E2E battery**: `make verify-pipeline-e2e-live` → `tests/operational/pipeline_live_e2e.sh` (10/10 PASS required; depends on `auth-check`; registered as `stock.live_tests` in `config/verify-components.json`). Its hermetic in-process twin is `make verify-pipeline-e2e` → `internal/platform/httpserver/server_pipeline_e2e_test.go`. Remaining end-to-end coverage is owned by tracked Go surfaces: `internal/platform/httpserver/*_e2e_test.go`, `tests/e2e/**`, and the per-provider `internal/capabilities/**` suites. Any additional authenticated HTTP surface added back as a live gate MUST route through `scripts/with-velox-auth` and depend on `auth-check`; `make auth-check` remains the canonical fail-closed credential probe.
- **Hygiene**: token values must be redacted as `REDACTED` or `<64-hex>` in any captured output; raw tokens must never appear in shell history, log files, commit messages, transcripts, or CI logs.
- **Rotation**: the historical helper `scripts/rotate_token.sh` does not exist in the tree (deleted by commit `7e6965aab`); `scripts/regenerate_token.sh` regenerates the *Google Drive* `token.json`, not the admin credential. Rotate manually, in this order: read the current PID environment, generate a fresh 64-hex value, replace `VELOX_ADMIN_TOKEN=` in `/etc/pipelinegen/pipelinegen.env` (keeping `0640 root:pipelinegen-agents`), restart the service so the systemd `EnvironmentFile` reloads, then confirm the new value is visible in the restarted PID environment and that `make auth-check` passes. Never echo the value.
- **One-off setup** (run once on the deploy host with `sudo`): `groupadd -f pipelinegen-agents`, add the running user to it, then `chown root:pipelinegen-agents /etc/pipelinegen/pipelinegen.env && chmod 0640 /etc/pipelinegen/pipelinegen.env`. Never use `0644`.

## Script Google Docs destination

All Google Docs produced by script generation use the canonical
script documents destination.

Canonical configuration:

PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID

Current production folder:

1unQMyEH_ZqtXHT5D-68dxvcV9KgKA6d4

Rules:

- Do not invent new Drive folders for script documents.
- Do not hardcode alternative folder IDs in agents or workers.
- `docs.folder_id` is an explicit caller override only.
- When `docs.enabled=true` and `docs.folder_id` is omitted,
  PipelineGen MUST resolve PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID.
- Folder resolution must have one canonical owner.

## StockRust certification boundary (three levels)

The StockRust render boundary is certified at three levels. **L1 and L3 are
currently shellless**: `tests/operational/youtube_stock_live_e2e.sh` and
`tests/operational/stockrust_live_e2e.sh` were deleted by commit `7e6965aab`
(2026-09-10, "purge 94% shell") and nothing replaced them.

- **L1 HTTP upstream** — *no current owner.* The former script covered
  discovery → transcript selection → cut → persist → download against a live
  server. The closest live HTTP coverage today is the pipeline E2E battery
  (`make verify-pipeline-e2e-live`), which drives discovery → process →
  Drive → catalog → download for YouTube AND Stock; it does **not** assert
  transcript selection, so it does not certify L1. Until a driver that
  asserts the transcript-selection leg exists, do NOT claim L1 coverage.
- **L2 Go adapter → Rust** — `internal/platform/media/rustexec/*_test.go`
  (canonical `render_plan` validate/transport, tamper hash-drift fail-closed,
  `render_stock → mux_audio_copy` final audio copy, encoder policy). This is
  the complete live certification surface today.
- **L3 Rust binary** — `cargo test --manifest-path rust/Cargo.toml` plus the Go
  e2e suites in `rustexec` (health, `render_stock` protocol, concat + full
  decode, 4-job concurrency, fail-closed, RTF).

Honest limitation: L2 is the only surface that certifies the canonical
`render_plan` path, the final audio copy and tamper hash-drift — and with L1/L3
shellless, `STOCKRUST=CERTIFIED` currently means "L2 green + Rust crate tests
green", not full three-level coverage. Canonical owner:
`docs/operations/stock-e2e-runbook.md` (§4 StockRust certification boundary).

Native stage timing + persistence: `render_stock` reports `metadata.ffmpeg_ms`
natively (like `render_audio_plan`); the perf e2e test persists
`wall_ms`/`rtf`/`ffmpeg_ms` into `performance_runs` when
`STOCKRUST_PERF_DB_PATH` points at a migrated SQLite DB.

## Git workflow

- Work directly on `main`.
- No feature branches and no pull requests for routine repository work.
- Before push: fetch and rebase on `origin/main`; never force-push.
- Push directly to `main`.
- After every push, inspect `git log -n 5 --oneline` and confirm the remote contains the intended commit.
- Keep commits focused and describe actual behavior, not planning history.

## Documentation rule

The working tree contains only current operational or machine-consumed documentation. Do not add action plans, closure journals, evidence dumps, archived snapshots, or duplicate source-of-truth documents. Git history is the archive.

Explicit exception (decided 2026-09-13): the operational E2E artifacts under `tests/operational/results/` are RETAINED, not evidence-dump debt. They are the only record of production-shaped timings (the source of the audit numbers in `docs/PIPELINE-*.md` and `docs/tickets/TICKET-PIPELINE-CRITICAL-PATH-DEPLOYMENT-2026-09-13.md`) and `status-*.json` files have been destroyed by hand before. The directory is output-only: never read by code, never a gate input. Do not delete, untrack, or gitignore it; regenerate-and-prune is a separate, deliberate operation. `tests/operational/results/MANIFEST.sha256` is the SHA-256 receipt of that retained inventory (regenerate with `(cd tests/operational/results && find . -type f ! -name MANIFEST.sha256 -print0 | sort -z | xargs -0 sha256sum) > MANIFEST.sha256`, verify with `sha256sum -c MANIFEST.sha256`). Like the directory it indexes it is output-only evidence: it is NOT a gate input and no check may fail on manifest drift.

Second explicit exception (decided 2026-09-13): the four dated audit snapshots at `docs/` root — `CLIP-RENDER-DEMOLITION-2026-09-12.md`, `MUDA-CODEBASE-AUDIT-2026-09-12.md`, `PIPELINE-ORCHESTRATION-AUDIT-2026-09-12.md`, `PIPELINE-WASTE-AUDIT-2026-09-12.md` — are RETAINED while the tickets they feed are open (`docs/tickets/*`, `architecture/catalog.yaml`). They are the reasoning layer and evidence index those tickets cite (`PIPELINE-WASTE` carries a dated status banner pointing at §17 and its successor tickets; `MUDA` cites the other three as prior art). Deletion trigger: once those tickets close, remove the snapshots and their parent links in the same change — git history is the archive from then on. Do not add new dated snapshot docs; append a status section to the existing one instead.

See `CANONICAL.md` for the authoritative source map.
