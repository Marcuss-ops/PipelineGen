# Stock E2E Battery — Action Plan (2026-07-05)

**Wave anchor**: `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05`
**Status**: pending (wave-tracker entry registered 2026-07-05; per-probe execution lands incrementally on main)
**Owner capability**: `tests/operational` (8 hermetic shell smokes + 1 aggregator)
**Lockstep surfaces**: `architecture/current.yaml` (wave-tracker) ≡ `CHANGELOG.md ## Unreleased > ### Added` ≡ `AGENTS.md ## Recent cross-cutting closures` ≡ this action plan
**Deadline**: 2026-07-29 (wave-flip ancestor only when ALL 14 checklist points PASS via `tests/operational/stock_e2e_full_battery.sh`)

---

## §0 — Context

The stock pipeline (search/direct URL → stage → cut → render → Drive → media_assets → outbox/Qdrant → search → download MP4) is the canonical "provider" alongside Artlist and YouTube channel-monitor. A rewrite of the surface landed earlier; this battery is the operator-facing receipt that the rewrite is end-to-end functional against a live PipelineGen server.

Per AGENTS.md godlike/07 NO-FAKE-AVAILABILITY: a closure entry that marks "stock works" without a probe that **actually runs the surface** is a godlike/07 violation. The 8 smoke scripts in `tests/operational/` are the canonical diagnostic surface; each `PASS` is the receipt; each `FAIL` is the canonical PR entry that needs to ship BEFORE the wave flips to `status: shipped`.

## §1 — Scope (8 probes + 1 aggregator)

| Probe | Script | What it asserts | Fail signal |
|-------|--------|-----------------|-------------|
| **STK-E2E-A** | `stock_e2e_route_aliveness_smoke.sh` | `POST /api/stock-pipeline/run` with empty `{}` returns **HTTP 400** (NOT 404) | 404 = registry/feature-flag bug → `PR-STOCK-ROUTE-REGISTRATION` |
| **STK-E2E-B** | `stock_e2e_search_and_run_loop_smoke.sh` | iterates 9 Drive folder IDs with `search-and-run` payload, polls `/api/jobs/{job_id}/full` every 3s for 60 iter, succeeds on `SUCCEEDED/INDEX_PENDING` | `404` route / `SUCCEEDED` unreachable / `FAILED` error → multi-PR mapping |
| **STK-E2E-C** | `stock_e2e_direct_url_smoke.sh` | exercises `direct_urls` path on 1 of 9 folders (scope-limit) | if `direct_urls` route is broken → `PR-STOCK-DIRECT-URLS-FLOW` |
| **STK-E2E-D** | `stock_e2e_media_assets_query_smoke.sh` | SQL query on `media_assets WHERE LIKE '%stock%' OR LIKE 'Stock E2E%'`: `source=stock`, `media_type=video`, `file_hash`, `drive_file_id`, `drive_link` non-empty | asset not committed to DB → `PR-STOCK-FINALIZER-COMPLETE` |
| **STK-E2E-E** | `stock_e2e_outbox_query_smoke.sh` | SQL on `outbox_events WHERE event_type='asset.index.requested'`: `status ∈ {pending, completed}`, `error` vuoto, NO `dead_letter` | dead-lettered outbox → `PR-STOCK-DELIVERY-RETRY` |
| **STK-E2E-F** | `stock_e2e_unified_search_smoke.sh` | `POST /api/media/search mode=hybrid sources=["stock"]` returns ≥1 hit with `source=stock` + `score` + downloadable `id` | empty search → `PR-STOCK-OUTBOX-QDRANT-INDEX` |
| **STK-E2E-G** | `stock_e2e_download_smoke.sh` | `POST /api/media/stock/clips/$ID/download` returns HTTP 200 + MP4 > 100KB; ffprobe confirms stream video + duration > 0 | download 404 → `PR-STOCK-DOWNLOAD-RESOLVER` |
| **STK-E2E-H** | `stock_e2e_full_battery.sh` | runs A→G sequentially, asserts the 14-point checklist, exits 0 only if ALL 14 PASS | any FAIL short-circuits to typed-grep failure mapping |

Each script is hermetic (auto-sufficient, no shared infra dependency); bash `-n` syntax-clean; idempotent (re-runnable without side-effects on the system).

## §2 — Honest scope-lock

The probes verify the **observable contract** of the stock pipeline. They are NOT a substitute for:
- unit tests on `stockpipeline/orchestrator.go` / `finalizer_gates.go` / each `step_*.go`
- integration tests on the `delivery.Publisher` → Drive adapter boundary
- property-based tests on the deterministic `clipID` derivation (`yt_<videoID>_<startSec>_<endSec>_v1`)

Pre-existing carry-forward (NOT regressions of this wave):
- `FIX-IMAGES-ROUTING-CYCLE` (deadline 2026-08-01) blocks `go build ./internal/application/images/...`
- `FIX-APP-WIRE-SCRIPT-SYNTAX` retired (re-attributed to workerruntime)
- 6-item voiceover build-issue carry-forward unchanged per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`

Backward-compat: the 9 Drive folder IDs in the `FOLDERS=(...)` array are scope-limit fixtures inherited from the user's pasted E2E battery; they are NOT canonical SSOT for "the folders stock can write to" — that authority lives at `internal/infrastructure/drive/folders/registry.go`.

## §3 — Per-probe execution checklist (godlike/06 slim-shape contract)

Per probe commit lands on `main` directly (NO branches, NO PR, NO `--force`) per AGENTS.md Git-Lesson-2. Each commit:
- adds 1 shell smoke under `tests/operational/stock_e2e_<probe>_smoke.sh` (or `stock_e2e_full_battery.sh` for H)
- adds 1 slot flip in `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05.linked_issues[STK-E2E-<X>]` (status: pending → shipped + `ship_sha` + `ship_date: 2026-07-05`)
- adds 1 closure bullet under `CHANGELOG.md ## Unreleased > ### Added`
- adds 1 mirror under `AGENTS.md ## Recent cross-cutting closures`
- ends with trailer `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`
- passes `bash -n` on the smoke file + `python3 -c 'import yaml; yaml.safe_load(open("architecture/current.yaml"))'`

Per-probe order (A→H) is the canonical migration sequence: A registers the wave + smoke (this commit); B is the heavy-lift search-and-run loop; C is a scope-limited direct_url path; D/E are read-side verification; F is the user-facing search contract; G is the closing download contract; H runs A→G + 14-point checklist + wave-flip.

## §4 — Failure diagnosis table (operator-facing)

| Failure pattern | Diagnostic | Canonical PR (godlike/06 SSOT owner) |
|-----------------|-----------|------------------------------------|
| `/api/stock-pipeline/*` returns 404 | StockPipeline module NOT mounted OR feature-flag off | `PR-STOCK-ROUTE-REGISTRATION` (registry mount seam) |
| empty payload → 200 (NOT 400) | handler BindJSON guard missing OR JwtAuth bypasses | `PR-STOCK-PREFLIGHT-VALIDATION` |
| `503` on valid payload | `jobs.Service` not wired in composition root | `PR-ARTLIST-*` precedent → `PR-STOCK-COMPOSITION-WIRE` |
| `FAILED stock.stage_sources` | `SourceStager` adapter not stager-bound | `PR-STOCK-STAGER-BOUND` |
| `FAILED stock.extract_clips` | `VideoCutter`/FFmpeg/path-finder issue | `PR-STOCK-CUTTER` |
| `FAILED stock.compose_chunks` | `StockRenderer`/FFmpeg compose | `PR-STOCK-RENDERER` |
| `FAILED stock.finalize` (production gate) | `Publisher` ↔ `Finalizer` race / out-of-order wire | `PR-STOCK-FINALIZER-PUBLISHER-RACE` |
| `SUCCEEDED` but `media_assets` empty | finalizer/projection asset incomplete | `PR-STOCK-FINALIZER-COMPLETE` |
| `media_assets` OK but search empty | outbox delivery / Qdrant indexing best-effort silent-fail | `PR-STOCK-OUTBOX-QDRANT-INDEX` |
| download 404 | Drive fallback path / resolver id collision | `PR-STOCK-DOWNLOAD-RESOLVER` |

Each row maps to a specific PR entry. A probe FAIL with one of these signatures is the canonical signal that the wave-flip is blocked until the PR ships.

## §5 — Verification gates

Per probe (Bash gates):
- `bash -n tests/operational/stock_e2e_<probe>_smoke.sh` exit 0
- runtime: requires a live PipelineGen server on `:8000` (or `:8081`) + `VELOX_ADMIN_TOKEN` matching the `test-admin-token-12345` fixture + `data/media/media.db.sqlite` writable
- environment prerequisites: `yt-dlp`, `ffmpeg`, `ffprobe` on PATH

Per wave-flip (Aggregator H gates):
- `tests/operational/stock_e2e_full_battery.sh` exit 0 with all 14 checklist points PASS
- exit 1 on ANY fail with the canonical failure-table entry as the diagnostic
- exit 2 on environment-prerequisite missing (server down / token wrong / DB unwritable / tools missing)

Wave-tracker gating: the wave flips `status: pending → status: shipped + exit_signal: true` ONLY when H passes on `origin/main` with the canonical 14-point receipt.

## §6 — Forward pointers (PR-STOCK-* future wave)

Some failures uncovered by these probes WILL surface `PR-STOCK-*` entries that don't yet exist. Per AGENTS.md "Pre-existing build issue carry-forward convention" + slim-schema ratchet:
- each new `PR-STOCK-*` opens a sibling slot in this wave's `linked_issues`
- each closes via the same lockstep (current.yaml + CHANGELOG + AGENTS)
- each flips `status: pending → shipped` BEFORE the wave-flip commit lands

The wave-deck is intentionally **bounded** — 14 checklist points + 8 probes are all that this battery asserts. Anything beyond (e.g. multi-locale voiceover integration, Qdrant schema-evolution migration, production-CI hardening) lives in the dedicated forward-pointer waves, not in the aggregator.

## §7 — Cross-references (godlike/06 SSOT umbrella)

- `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` — canonical wave-tracker (status: pending, 8 slim `linked_issues`)
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — 6-item voiceover + app build-issue carry-forward (NOT regressions of this wave)
- `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` — sister E2E chain verification wave (different capability; same hermetic-shell-smoke pattern)
- `architecture/current.yaml#GODOBJ-2026-07-03` — stockpipeline decomposition wave (P0 #1 P0 absolute; done pre-battery)
- `architecture/action-plans/2026-07-04-qdrant-verification-chain.md` — sister action-plan template (the 9-section structure this mirrors)
- `internal/api/assets/stock/handler.go` — the canonical HTTP surface (`POST /api/stock-pipeline/run` + `/search-and-run`)
- `internal/application/assets/providers/stock/stockpipeline/orchestrator.go` — canonical 6-step orchestrator under test
- AGENTS.md Git-Lesson-2 + 3 + 4 + 5 (direct-to-main workflow, Co-authored-by trailers, race-protect, byte-equivalent-replay recovery)

## §8 — Honest-limitation disclosure (godlike/07)

- The battery is **forward-pointer-bounded**: it asserts the 14-point observable contract. It does NOT catch deeply nested Go-level regressions in the orchestrator (use the canonical unit/integration tests for that).
- The 9 Drive folder IDs in the `FOLDERS=(...)` array are operator-supplied fixtures; the battery does NOT verify those folders are still writable from the host (assumed pre-flight verified).
- A probe PASS is the operator-facing receipt ONLY if the live server is on a known-clean baseline. A probe FAIL during a parallel agent window may itself be a race artifact (per AGENTS.md Git-Lesson-5 byte-equivalent-replay recovery).
- `bash -n` validation is syntax-only; runtime requires a live server. The wave-flip is gated on H passing on `origin/main` runtime, not on the probe syntax alone.
- Per godlike/07, a probe that pretends to green while the underlying pipeline is broken IS REJECTED at the closure wave — verified by canonical subsystem-level integration tests cross-validating the surface.

## §9 — Closure discipline (godlike/07)

- **EXPAND → BACKFILL → CUTOVER → CONTRACT** migration sequence (per AGENTS.md Pattern discipline):
  - **EXPAND**: each per-probe creation lands as a hermetic smoke with the canonical wave-tracker slot flip
  - **BACKFILL**: H aggregator runs against the live server; each FAIL maps to a forward-pointer PR
  - **CUTOVER**: wave-flip `status: pending → shipped + exit_signal: true` ONLY when ALL 14 PASS
  - **CONTRACT**: physical git-prune of dead-code surfaces if any are surfaced during the wave (`PR-STOCK-*` forward pointers)
- **3-surface lockstep** (per CANONICAL.md §1):
  - every probe commit lands lockstep on `architecture/current.yaml` (slot flip) + `CHANGELOG.md` (closure bullet) + `AGENTS.md` (mirror entry)
  - mirror entry format: `**[STK-E2E-<X> closure (commit <SHA>, 2026-07-05)]** ... + Co-authored-by trailer per AGENTS.md Git-Lesson-3`
- **Race protection** (per AGENTS.md Git-Lesson-2/4/5):
  - pre-commit: `git fetch origin && git log --oneline HEAD..@{u}` (must be empty for safe ff-push)
  - post-commit: race detection on the canonical wave-tracker slot (replay SHA on origin/main = canonical, local SHA superseded)
  - byte-equivalence check on the 4 surfaces if a parallel-agent commit was re-applied

## §10 — Per-probe commit checklist (canonical pattern)

```
# Per-probe commit:
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    add tests/operational/stock_e2e_<probe>_smoke.sh \
           architecture/current.yaml \
           CHANGELOG.md \
           AGENTS.md

git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit -m 'test(e2e): STOCK-E2E-<X> smoke probe closure

[body describing the probe + failure mapping + godlike/06/07 discipline]

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'

git fetch origin
git log --oneline HEAD..@{u}   # must be empty
git push origin main          # direct-to-main, NO branches
```

This pattern is the canonical per-probe closure; it is repeated for A → H in commit order.
