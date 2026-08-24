# Stock E2E — Asset Pipeline Debug

**Source**: extracted from [`docs/operations/stock-e2e-runbook.md`](stock-e2e-runbook.md) §10.2 + §10.5.
**Wave anchor**: [`architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05`](../../architecture/current.yaml)
**Status**: shipped (was inline in the runbook prior to this extraction).
**Owner capability**: `tests/operational/stock_e2e_*.sh`

Extracted verbatim. The §-anchors (§10.2, §10.5) are preserved as stub headers in the runbook for script-grep compatibility.

---

### §10.2 — Canonical paths + env-var contract

**Canonical paths**:
- **Source (one source of truth)**: `scripts/stock_pipeline_live_test.sh` — byte-identical, edited ONLY here per godlike/06 SSOT.
- **Registered copy**: `scripts/tests/stock_pipeline_live_test.sh` — `cp -p` mirror registered under `scripts/tests/`; regenerated from source via `cp -p` (preserves mode + mtime). SHA256-equal at every commit (verified by `cmp -s` per §10.8).
- **Workflow canonical**: `workflows/test_stock_pipeline_live.yaml` — `workflow_dispatch`-only, no `on: pull_request` / no `on: push` so CI never auto-runs the live battery.
- **Runbook canonical**: `docs/operations/stock-e2e-runbook.md#§10` (this section).

**Env-var contract** (defaults shown — override at invocation):

| Var | Default | Purpose |
|-----|---------|---------|
| `BASE` | `http://127.0.0.1:8000` | PipelineGen server base URL |
| `VELOX_PORT` | `8000` | Health-check port (used by workflow pre-flight) |
| `DB_PATH` | `data/media/media.db.sqlite` | SQLite canonical store (used by workflow pre-flight) |
| `QDRANT_URL` | `http://127.0.0.1:6333` | Vector store (used by STEP 11 unified search) |
| `QDRANT_COLLECTION` | `media_assets_current` | Collection name |
| `YOUTUBE_URL` | _(REQUIRED, no default)_ | Operator-supplied; MUST be a fresh URL the dev cache has NOT pre-fetched. NEVER use `RRJvrDKunyA` (cache-shadow). Suggested fresh IDs: `jNQXAC9IVRw` ("Me at the zoo", 19s). |
| `QUERY` | _(empty)_ | ytsearch term (only used in RUN_SEARCH=1 mode) |
| `RUN_SEARCH` | `0` | `1` = ytsearch branch (STEP 2 enabled); `0` = skip |
| `RUN_DIRECT` | `1` | `1` = direct URL branch (STEP 3-4 enabled); `0` = skip |
| `REQUIRE_QDRANT` | `1` | `1` = STEP 11 must find ≥1 hit; `0` = best-effort |
| `MIN_MP4_BYTES` | `65536` | Pass threshold for STEP 7 (MP4 probe) |
| `JOB_POLL_TIMEOUT` | `300` | Total seconds waiting for job terminal state |
| `JOB_POLL_INTERVAL` | `10` | Seconds between `/jobs/<id>/full` polls |
| `STOCK_DRIVE_FOLDER_ID` | _(unset — server uses canonical Drive upload folder from `internal/infrastructure/remote/drive` config)_ | Optional folder override; if unset, the server reads the canonical Drive folder from its bound config (typically `STOCK_DRIVE_FOLDER_ID` env or `config/routing.yaml`'s `stock.drive_folder_id`). |
| `VELOX_ADMIN_TOKEN` | _(REQUIRED)_ | Bearer token for `/api/stock-pipeline/*` + `/api/jobs/*` |

**Defaults grep-verified 2026-07-12**: every default in this table was confirmed against `scripts/stock_pipeline_live_test.sh` defaults block (lines 42-53) via `grep -nE '^:? *(QDRANT_URL|QDRANT_COLLECTION|BASE|VELOX_PORT|DB_PATH|YOUTUBE_URL|QUERY|RUN_SEARCH|RUN_DIRECT|REQUIRE_QDRANT|MIN_MP4_BYTES|JOB_POLL_TIMEOUT|JOB_POLL_INTERVAL|STOCK_DRIVE_FOLDER_ID)=' scripts/stock_pipeline_live_test.sh`. Drift detection: re-run this grep on any operator commit that touches the script's defaults block. SSOT regression here MUST update BOTH the script and this runbook atomically (per godlike/06 lockstep).

---

### §10.5 — Triage table (layer ⇄ failure ⇄ file ⇄ forward-pointer)

When the battery emits `[FAIL]`, the canonical owner file is the diagnostic target. Per godlike/06 SSOT, edit ONLY that file (NOT this runbook, NOT the script).

| Layer | [FAIL] observed on STEP | Canonical owner file | Forward-pointer |
|-------|--------------------------|----------------------|-----------------|
| Route | STEP 1 / STEP 2 / STEP 3 | `internal/api/assets/stock/handler.go::RegisterRoutes` | `PR-STOCK-ROUTE-REGISTRATION` |
| Validation (key shape) | STEP 3 attempts 2-4 (HTTP 400 "invalid key") | `internal/api/assets/stock/handler_run.go::RunStockPipeline` | `PR-STOCK-PREFLIGHT-VALIDATION` |
| Composition / nil-tolerance | STEP 3 attempt 1 ⇄ HTTP 503 | `internal/app/build_bundles_stock.go::WireStock` | `PR-STOCK-COMPOSITION-WIRE` |
| Job / broker Enqueue (hang at submit) | STEP 3 attempt-N HTTP 000 / "no job_id" — handler accepted the request but never returned a job_id | `internal/platform/sqlite/jobs/repository.go::Create` (+ `internal/application/jobs/enqueue_service.go`) | `PR-STOCK-COMPOSITION-WIRE` (envelope) |
| Job / handler HandleJob (hang at execute) | STEP 4 polling timeout AFTER job_id was returned — terminal state (`SUCCEEDED` / `FAILED`) never reached | `internal/capabilities/assets/providers/stock/stockpipeline/job_handler.go::HandleJob` (+ `orchestrator_run.go::RunResilient`) | `PR-STOCK-ORCHESTRATOR-HANDLE-JOB` |
| Source staging | STEP 5 final state FAILED at `stock.stage_sources` | `internal/capabilities/assets/providers/stock/stockpipeline/stager_adapter.go` | `PR-STOCK-STAGER-WIRE` |
| Cutter / ffmpeg | STEP 7 zero-size OR STEP 8 ffprobe failed | `internal/infrastructure/media/render/cutter.go` | `PR-STOCK-CUTTER` |
| Renderer / ffmpeg compose | STEP 5 final state FAILED at `stock.compose_chunks` | `internal/infrastructure/media/render/renderer.go` | `PR-STOCK-RENDERER` |
| Finalize + Publisher | STEP 5 final state FAILED at `stock.finalize` | `internal/capabilities/assets/providers/stock/stockpipeline/upload_orchestration.go` | `PR-STOCK-FINALIZER-PUBLISHER-RACE` |
| Asset projection (DB) | STEP 10 "asset not found" | `internal/capabilities/assets/providers/stock/stockpipeline/finalizer_gates.go` | `PR-STOCK-FINALIZE-PROJECTION` |
| Outbox / Qdrant index | STEP 11 ≥1 hit expected but empty | `internal/application/jobs/outbox/delivery.go` | `PR-STOCK-OUTBOX-QDRANT-INDEX` |
| Unified search | STEP 11 source field != stock | `internal/platform/qdrant/search/indexing.go::IndexAsset` | `PR-STOCK-OUTBOX-QDRANT-INDEX` |
| Download handler | STEP 7 HTTP 404 | `internal/api/assets/stock/handler.go::DownloadClip` | `PR-STOCK-DOWNLOAD-ROUTE-REGISTRATION` |
| Qdrant hits (direct) | STEP 12 `REQUIRE_QDRANT=1` ⇄ 0 hits | `internal/platform/qdrant/projection/port.go` | `PR-STOCK-OUTBOX-QDRANT-INDEX` |

The two distinct JOB-layer rows encode TWO separate failure modes:
- **(hang at submit)** = broker/Enqueue never returned a job_id (downstream of STEP 3 attempt-1 with valid key shape).
- **(hang at execute)** = broker returned a job_id BUT the orchestrator/handle-job never reached a terminal state (downstream of STEP 4 polling).

These are distinct sub-surfaces with distinct owner files and distinct forward-pointers. Lockstep with §3 + §5 row additions of `PR-STOCK-ORCHESTRATOR-HANDLE-JOB`.
