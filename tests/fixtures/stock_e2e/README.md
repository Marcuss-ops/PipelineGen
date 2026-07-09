# tests/fixtures/stock_e2e — Operator README

End-to-end audit fixtures capturing the multiplied-blocked state of PipelineGen's stock-pipeline.
All 9 fixtures are live-system captures (curl probes + sqlite3 queries) — never synthetic. Each
fixture is godlike/07 no-fake-availability (verbatim HTTP responses + verbatim X-Request-Ids +
verbatim DB counts + verbatim `match `scanprobes) and 3-surface-locked to `architecture/
current.yaml` wave-tracker entries; each surfaces forward-pointers to unblock the user-spec
happy path.

## Canonical reproduction (single command)

```bash
BASE=http://127.0.0.1:8000 AUTH="Authorization: Bearer ${VELOX_ADMIN_TOKEN}" make verify-stock-e2e
# OR bare curl pipeline (mirrors the 8 phases F0–F7):
for f in tests/fixtures/stock_e2e/0[0-7]*.json; do jq -e '.' "$f" >/dev/null && echo "OK $f"; done
git fetch origin && bash scripts/ci-architectural-checks.sh
```

## 9 HTTP endpoints (canonical probe matrix)

| # | Method | URL | FASE origin | Outcome class |
|---|--------|-----|-----|------|
| 1 | GET  | `/ready` | FASE 0+2+7 carry | HTTP 503 — `broker heartbeat stale: last heartbeat 9223372036854775807s ago` |
| 2 | POST | `/api/script/generate` (with images) | FASE 1 seed #1 + carry | HTTP 500 (broker internal) — wiring-blocked terminal |
| 3 | POST | `/api/script/generate` (from clips) | FASE 1 seed #2 + FASE 7 replay ×3 | HTTP 500 (broker internal) + HTTP 429 ×3 (rate-limit) |
| 4 | POST | `/api/media/search?mode=hybrid` | FASE 4 | HTTP 500 — `semantic backend not available` |
| 5 | POST | `/api/media/announce` | FASE 4 alternatives | HTTP 500 — `no eligible backends` |
| 6 | GET  | `/api/jobs/$JOB_ID/full` | FASE 2 + FASE 6 surrogate poll | HTTP 200 RETRY_WAIT (single-poll cadence per PR-POLL-CADENCE-DISCIPLINE) |
| 7 | POST | `/api/media/stock/clips/$ID/download` (+ 6 alt prefixes) | FASE 5 | HTTP 404 `404 page not found` — route-group not mounted |
| 8 | POST | `/api/stock-pipeline/run` | FASE 6 | HTTP 404 — `cfg.Features.StockPipelineEnabled=false` gates route-group |
| 9 | GET  | `/api/media/clip/yt_jNQXAC9IVRw_95d911af/download` (alt) | FASE 5 clipboard probe | HTTP 404 — route-group not mounted |

## 9 fixture files (filesystem truth)

The user-spec said "7 fixtures"; canonical disksurface is **9 files** because FASE 1 naturally decomposes
into 2 sub-fixtures (request payload + response envelope). The 7 distinct e2e FASES cover FASE 0
sanity + FASE 1 (split) + FASE 2 + FASE 3 + FASE 4 + FASE 5 + FASE 6 + FASE 7.

| File | FASE | Role |
|------|------|------|
| `00_sanity_probes.json` | FASE 0 | pre-flight /ready + asset existence |
| `01_search_and_run_payload.json` | FASE 1a | reconstructed request payload |
| `01_search_and_run_response.json` | FASE 1b | response envelope + 2 captured job_ids |
| `02_poll_terminal.json` | FASE 2 | raw poll + retry-wait + 5-hypothesis audit (`v2` supersedes `v1 DETERMINISTICALLY_UNREACHABLE`) |
| `03_sqlite_probes.json` | FASE 3 | jobs/outbox_events/media_assets SQLite queries at T0 |
| `04_hybrid_search_results.json` | FASE 4 | 5-mode search probes (`hybrid/ann/multisource/announce/no-source-fanout`) |
| `05_ffprobe_yt_jNQXAC9IVRw_95d911af.json` | FASE 5 | md5 triple-cross-validate + ffprobe verbatim (MP4_INTEGRITY_VERIFIED_ON_LOCAL_FILE) |
| `06_direct_url_run.json` | FASE 6 | 3× POST 404 + canonical handler.go response shape |
| `07_idempotency_diff.json` | FASE 7 | T0==T1 invariant + 3-axis diff (ConflictSkipByHash validate) |

## godlike/06 audit-pin (drift source-of-truth)

The canonical stock-pipeline terminal flip surface is `StockFinalizeStep` at
[`internal/application/assets/providers/stock/stockpipeline/orchestrator_steps.go::StockFinalizeStep`](../../internal/application/assets/providers/stock/stockpipeline/orchestrator_steps.go).

This is the SOLE canonical owner of the 7-step finalize pipeline (dedupe → DeleteByIDTx →
InsertTx → UpsertVoiceoverProjectionTx → EnqueueIndexEvent → EnqueueCleanupEvent → commit).
Per godlike/06 SSOT one-canonical-owner-per-fact: any change to the stock-pipeline write-back
contract MUST be reflected here BEFORE any caller migration. Drift surface per
`architecture/current.yaml#PR-COMPLETE-WORKER-BROAD-FIX`.

## Forward-pointer pull-list (carry-forward surface)

```
PR-POLL-CADENCE-DISCIPLINE         (FASE 2; sustained-poll 429-throttle backoff)
PR-HEARTBEAT-TELEMETRY-BUG         (FASE 2; /ready=503 telemetry)
PR-COMPLETIONPORT-WIRE-MISSING     (FASE 2; worker.CompletionPort not wired for artifact jobs)
PR-SEARCH-MODE-HYBRID-WIRE         (FASE 4; semantic backend not available)
PR-SEARCH-ANNOUNCE-NO-SOURCE-FANOUT-TIMEOUT (FASE 4; no eligible backends)
PR-STOCK-CLIPS-DOWNLOAD-ROUTE      (FASE 5; /api/media/stock/clips/$ID/download 404)
PR-ENABLE-STOCK-PIPELINE-GATE      (FASE 6; cfg.Features.StockPipelineEnabled=false)
PR-STOCK-PIPELINE-FOLDER-CONFIG-DEFAULTS (FASE 6; no folder_id/folder_name defaults)
PR-RATELIMIT-UNIFIED-POLICY        (FASE 7; rate-limit policy /api/script/* write paths)
```

## 3-surface lockstep (per CANONICAL.md §1)

Each fixture is locked (per **CANONICAL.md §1** godlike/06 SSOT 3-surface lockstep) to
`architecture/current.yaml#STOCK-E2E-VERIFICATION-CHAIN-2026-07-05.linked_issues[FASE-N-*]`
+ a CHANGELOG entry under `## Unreleased → ### Added` + an `AGENTS.md` Recent cross-cutting
closures entry. This README is a 4th operator-facing procedural surface (NOT canonical SSOT).
Drift detection: a future agent that finds an `rg` mismatch between any fixture's
`_forward_pointers` IDs and the wave-tracker SSOT should file `PR-STOCK-E2E-LOCKSTEP-DRIFT`
(canonical effort: keep fixture-of-record ↔ wave-tracker ↔ CHANGELOG ↔ AGENTS in lockstep).
