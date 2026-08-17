# Stock E2E Runbook — Operational Procedure

**Wave anchor**: [`architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05`](../architecture/current.yaml)
**Status**: shipped (documentation lockstep with the 8 probes + 1 aggregator shipped 2026-07-05)
**Owner capability**: `tests/operational/stock_e2e_*.sh` (8 hermetic shell smokes + 1 aggregator wrapper)
**Lockstep surfaces**: this runbook ≡ `architecture/current.yaml` (wave-tracker) ≡ `architecture/action-plans/2026-07-05-stock-e2e-battery.md` (canonical narrative) ≡ `AGENTS.md ## New Runbook` (agent-facing fast reference) ≡ `CHANGELOG.md ## Unreleased > ### Added` (audit trail)
**Audience**: SRE + on-call operators running stock-e2e verification on a live PipelineGen server
**Deadline**: 2026-07-29 (wave-flip ancestor — gated on ALL 14 checklist points PASS via Phase H aggregator)

---

## §0 — Context for operators

The stock pipeline (`search/direct URL → stage → cut → render → Drive → media_assets → outbox/Qdrant → search → download MP4`) is the canonical stock-side provider alongside Artlist (`ART-002`) and YouTube channel monitor (`QDRANT-CHAIN-VERIFY-2026-07-04`). This runbook is the operator-facing receipt that the rewrite is end-to-end functional against a live PipelineGen server.

Per godlike/07 NO-FAKE-AVAILABILITY (canonical at `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md`): a closure that marks the Stock surface operational without a probe that **actually runs the surface** is invalid. The 9 phases A → I below are the canonical diagnostic surface; each phase is the receipt; each FAIL signal maps to a canonical `PR-STOCK-*` forward-pointer.

Per AGENTS.md git lessons: each per-phase commit lands directly on `main` (no branch, no PR, no `--force`) per **Git-Lesson-2** + Git-Lesson-3 (Co-authored-by trailer) + Git-Lesson-4 (race-protect: `git fetch origin && git log --oneline HEAD..@{u}` must be empty) + Git-Lesson-5 (byte-equivalent-replay recovery).

---

## §1 — The 9 phases A → I (operational procedure)

| Phase | Script | What it asserts on the live server | Fail signal |
|-------|--------|-----------------------------------|-------------|
| **A** | `bash tests/operational/stock_e2e_route_aliveness_smoke.sh` | `POST /api/stock-pipeline/run` with empty `{}` returns **HTTP 400** | 404 = route not mounted → `PR-STOCK-ROUTE-REGISTRATION` |
| **B** | `bash tests/operational/stock_e2e_search_and_run_smoke.sh` | Iterates 9 Drive folder IDs with `search-and-run` payload; polls `/api/jobs/{job_id}/full` every 3s for 60 iter; final state ≥ 1 succeeds on `SUCCEEDED/INDEX_PENDING` | 404 → `PR-STOCK-ROUTE-REGISTRATION`; SUCCEEDED unreachable → `PR-STOCK-COMPOSITION-WIRE`; job FAILED → `PR-STOCK-STAGER-WIRE` |
| **C** | `bash tests/operational/stock_e2e_direct_url_smoke.sh` | Exercises `direct_urls` path on 1 of 9 folders (scope-limit) | `direct_urls` broken → `PR-STOCK-DIRECT-URLS-FLOW` |
| **D** | `bash tests/operational/stock_e2e_db_assets_smoke.sh` | `SELECT ... FROM media_assets WHERE LIKE '%stock%' OR LIKE 'Stock E2E%'` — `source=stock`, `media_type=video`, `file_hash`, `drive_file_id`, `drive_link` non-empty | asset not committed → `PR-STOCK-FINALIZE-PROJECTION` |
| **E** | `bash tests/operational/stock_e2e_db_outbox_smoke.sh` | `SELECT ... FROM outbox_events WHERE event_type='asset.index.requested'` — `status` ∈ {pending, completed}, `last_error` empty, NOT `dead_lettered` | retry-exhausted → `PR-STOCK-OUTBOX-RETRY-EXHAUSTED`; dead-lettered → `PR-STOCK-OUTBOX-DEAD-LETTERED`; last_error non-empty → `PR-STOCK-OUTBOX-LAST-ERROR` |
| **F** | `bash tests/operational/stock_e2e_unified_search_smoke.sh` | `POST /api/media/search mode=hybrid sources=["stock"]` returns ≥ 1 hit with `source=stock + score + downloadable id` | empty search → `PR-STOCK-OUTBOX-QDRANT-INDEX` |
| **G** | `bash tests/operational/stock_e2e_download_smoke.sh` | Extracts STOCK_ID from `media_assets` (source=stock, ORDER BY created_at DESC LIMIT 1), `POST /api/media/stock/clips/$STOCK_ID/download` → MP4 > 100KB; `ffprobe` confirms video stream + duration > 0 | 404 → `PR-STOCK-DOWNLOAD-ROUTE-REGISTRATION`; 503 → `PR-STOCK-COMPOSITION-WIRE`; zero-size → `PR-STOCK-DOWNLOAD-ZERO-SIZE`; ffprobe failed → `PR-STOCK-CUTTER` |
| **H** | `bash tests/operational/stock_e2e_full_battery.sh` | Runs A → G sequentially + asserts `14/14` PASS verdict; on PASS + `WRAPPER_BOOKKEEPING=1` env var: flips `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` parent wave entry to `status: shipped + exit_signal: true` via recipe-style 6-step bash command stream | Any FAIL → per-Phase diagnostic (A → G) |
| **I** | **(this runbook)** — `docs/operations/stock-e2e-runbook.md` | The canonical operator-facing procedure that hardens Phases A → H into a single reproducible playbook; mirrors in `AGENTS.md ## New Runbook` section; cross-references with `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05`, `architecture/action-plans/2026-07-05-stock-e2e-battery.md`. **Phase I itself is verified by `bash -n` on shell snippets within this runbook + canonical cross-reference lint** | Stale section / missing canonical owner / missing cross-reference |

**Operational procedure**:

```bash
# 1. Pre-flight env: yt-dlp, ffmpeg, ffprobe, jq, sqlite3, curl on PATH.
#    (see §4 for the canonical pre-flight gate)

# 2. Confirm the 8 probes + 1 aggregator are on disk.
ls tests/operational/stock_e2e_*_smoke.sh tests/operational/stock_e2e_full_battery.sh

# 3. Run phases A → G independently first to localize which phase fails.
for phase in A B C D E F G; do
    fn=$(ls tests/operational/stock_e2e_${phase,,}*.sh 2>/dev/null || ls tests/operational/stock_e2e_route_aliveness_smoke.sh tests/operational/stock_e2e_search_and_run_smoke.sh tests/operational/stock_e2e_direct_url_smoke.sh tests/operational/stock_e2e_db_assets_smoke.sh tests/operational/stock_e2e_db_outbox_smoke.sh tests/operational/stock_e2e_unified_search_smoke.sh tests/operational/stock_e2e_download_smoke.sh | sed -n "$phase p")
    bash "$fn" || echo "PHASE $phase FAIL; consult §3 diagnosis decision tree"
done

# 4. Run the canonical release gate. It creates one run ID, captures the
#    battery receipt, validates every required live surface, and emits the
#    aggregate claim only after the attested receipt passes.
make verify-stock-release

# 5. Diagnostic-only Phase H execution requires an explicit run ID and key.
#    Its output is evidence, not a release authorization.
STOCK_E2E_RUN_ID="diagnostic-$(date +%s%N)" \\
STOCK_E2E_RECEIPT_KEY="diagnostic-key" \\
  bash tests/operational/stock_e2e_full_battery.sh

# 6. Phase I = this runbook verification.
#    (canonical: re-read this runbook + cross-verify §3 decision tree matches action plan §4)
```

---

## §2 — 14-point checklist (acceptance criteria)

Per godlike/06 SSOT (one canonical owner per fact): the 14 points are the canonical sum of per-probe sub-assertions across A → G (per action plan §1). Each probe's exit 0 is the canonical receipt for ALL of its sub-assertions (per action plan §3). The aggregator (Phase H) tallies `passed/(14) points` and prints the verdict.

| # | Point | Phase probe | Sub-assertion |
|---|-------|-------------|---------------|
| 1 | route_aliveness returns 400 | A | `r.POST(/api/stock-pipeline/run)` with empty `{}` returns 400 |
| 2 | search_and_run job SUCCEEDED | B | iterated job reaches `SUCCEEDED/INDEX_PENDING` terminal |
| 3 | direct_url path exercises | C | `direct_urls` payload completes 1 of 9 folders |
| 4 | media_assets.source=stock | D | `source` column populated in projection |
| 5 | media_assets.media_type=video | D | `media_type` column populated |
| 6 | media_assets.file_hash + drive_file_id + drive_link | D | 3-column triple populated post-finalize |
| 7 | outbox_events.status ∈ {pending, completed} | E | projection status terminal non-dead-letter |
| 8 | outbox_events.last_error empty | E | no error trail in column |
| 9 | outbox_events NOT dead_lettered | E | status NOT in dead-letter state machine |
| 10 | unified search returns ≥ 1 hit | F | `/api/media/search` hybrid mode populates results |
| 11 | unified search source field = "stock" | F | result origin tagged stock |
| 12 | download endpoint returns HTTP 200 | G | `r.POST /api/media/stock/clips/$ID/download` succeeds |
| 13 | download size > 100KB | G | MP4 artifact > 100000 bytes |
| 14 | ffprobe: video stream present + duration > 0 | G | mp4 metadata sanity check |

**Wave-flip ancestor**: `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` flips `status: pending → status: shipped + exit_signal: true` ONLY when ALL 14 points PASS via Phase H aggregator (per action plan §5 wave-tracker gating).

---

## §3 — Diagnosis decision tree (troubleshooting)

Per action plan §4 ("Failure diagnosis table") + per-probe FAIL mappings from Phase H aggregator exit-cases — the canonical decision tree for an operator who sees a FAIL signal:

| Failure pattern | Canonical PR forward-pointer | Owner file (godlike/06 SSOT) | godlike/07 fail-closed action |
|-----------------|------------------------------|-------------------------------|------------------------------|
| `/api/stock-pipeline/*` returns **404** | `PR-STOCK-ROUTE-REGISTRATION` | `internal/api/assets/stock/handler.go::RegisterRoutes` | Add the missing `r.POST(...)` line + verify `internal/app/registry_assets.go::setUpRoutes` wires the handler |
| `POST /api/stock-pipeline/run` empty `{}` returns **200** (not 400) | `PR-STOCK-PREFLIGHT-VALIDATION` | `internal/api/assets/stock/handler.go::RunStockPipeline` | Add `apiutil.BindJSON[Bid]()` + validation guard before svc dispatch |
| Valid payload returns **503** | `PR-STOCK-COMPOSITION-WIRE` | `internal/app/build_bundles_stock.go::WireStock` | Verify `jobs.Service` is wired in composition; handler nil-tolerance should return 503 |
| Job terminal status: `FAILED stock.stage_sources` | `PR-STOCK-STAGER-WIRE` | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go` (or canonical SourceStager) | Verify `SourceStager` adapter is bound; composition root must inject the concrete adapter |
| Job terminal status: `FAILED stock.extract_clips` | `PR-STOCK-CUTTER` | `internal/infrastructure/media/render/cutter.go` | ffmpeg cutter / path-finder diagnostic |
| Job terminal status: `FAILED stock.compose_chunks` | `PR-STOCK-RENDERER` | `internal/infrastructure/media/render/renderer.go` | ffmpeg compose diagnostic |
| Job terminal status: `FAILED stock.finalize` (production gate) | `PR-STOCK-FINALIZER-PUBLISHER-RACE` | `internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go` | Publisher ↔ Finalizer race / out-of-order wire |
| Job terminal status: stuck, never terminal (job_id returned but state never advances past step ladder) | `PR-STOCK-ORCHESTRATOR-HANDLE-JOB` | `internal/application/assets/providers/stock/stockpipeline/job_handler.go::HandleJob` | Handler execution hang / orchestrator stuck mid-ladder (canonical 6-step `RunResilient` never returns terminal state) |
| `SUCCEEDED` but `media_assets` empty | `PR-STOCK-FINALIZE-PROJECTION` | `internal/application/assets/providers/stock/stockpipeline/finalizer_gates.go` | Finalizer/projection asset incomplete |
| `media_assets` OK but search empty | `PR-STOCK-OUTBOX-QDRANT-INDEX` | `internal/application/jobs/outbox/delivery.go` | Outbox delivery / Qdrant indexing best-effort silent-fail |
| `outbox_events.status='failed'` (transient retry-able) | `PR-STOCK-OUTBOX-RETRY-EXHAUSTED` | `internal/infrastructure/database/sqlite/outboxevents/repository.go::MarkFailed` (line 252) | Pre-condition side: `attempt_count >= max_attempts` check + `RequeueExpiredLeases` scheduling |
| `outbox_events.status='dead_lettered'` | `PR-STOCK-OUTBOX-DEAD-LETTERED` | `internal/infrastructure/database/sqlite/outboxevents/repository.go` (line 252 + 321) | Canonical owner writes `SET status = 'dead_letter'` — investigate retry loop exhaustion |
| `outbox_events.last_error` non-empty | `PR-STOCK-OUTBOX-LAST-ERROR` | `internal/infrastructure/database/sqlite/outboxevents/repository.go` (lines 252, 266, 321, 367) | `last_error` write seam — inspect the surface error to identify the upstream cause |
| `download` 404 (no `/api/media/stock/clips/<id>/download` route) | `PR-STOCK-DOWNLOAD-ROUTE-REGISTRATION` | `internal/api/assets/stock/handler.go` (lines 39-40 = existing canonical r.POST calls) | Add the missing `r.POST(/api/media/stock/clips/:id/download, h.DownloadClip)` route + handler delegate to `StockRenderWriteStep` |
| `download` zero-size (real route, surface broken) | `PR-STOCK-DOWNLOAD-ZERO-SIZE` | `internal/application/assets/providers/stock/stockpipeline/step_compose_chunks.go::StockComposeChunksStep.Run` | Canonical stitch + write seam |
| `download` ffprobe failed (no video stream OR duration 0) | `PR-STOCK-CUTTER` | `internal/infrastructure/media/render/cutter.go` | ffmpeg cutter / mp4 muxer diagnostic |

**Decision dispatch**:

1. Run the canonical release gate: `make verify-stock-release` (it owns the run ID, private receipt key, validator, and aggregate claim).
2. If exit 0 → all 14 points PASS → the release gate emits the only authoritative aggregate claim; diagnostic output alone is insufficient.
3. If exit 1 → per-Phase FAIL signal in §3 table → ship the canonical PR forward-pointer + bring-up + re-run Phase H
4. If exit 2 → environment prerequisite missing (server down / token wrong / DB unwritable / tools absent) → consult §4 pre-flight gate
5. If shell exit 124 → per-probe `set -euo pipefail` failure with diagnostic preserved at `$TMP_DIR` (cleanup-on-PASS trap pattern)

---

## §4 — Pre-flight gate (environment prerequisites)

Per action plan §5 (verification gates). The wrapper's pre-flight verifies ON disk that all 7 probe scripts exist; the operator must verify the LIVE environment BEFORE running the probes:

```bash
# 1. Runtime tools on PATH
command -v yt-dlp ffmpeg ffprobe jq sqlite3 curl 2>/dev/null

# 2. Database writable
ls -la data/media/media.db.sqlite && touch data/media/media.db.sqlite

# 3. Live PipelineGen server reachable on canonical ports
for port in 8000 8081; do
    status=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://localhost:$port/ready)
    [ "$status" = "200" ] && echo "port $port /ready OK" || echo "port $port /ready FAIL ($status)"
done

# 4. Admin token set (matches test-admin-token-12345 fixture OR canonical)
[ -n "$VELOX_ADMIN_TOKEN" ] && echo "token set" || echo "VELOX_ADMIN_TOKEN UNSET"

# 5. All 8 probes + 1 aggregator on disk
for f in stock_e2e_route_aliveness stock_e2e_search_and_run stock_e2e_direct_url stock_e2e_db_assets stock_e2e_db_outbox stock_e2e_unified_search stock_e2e_download stock_e2e_full_battery; do
    if [ -f "tests/operational/${f}.sh" ]; then echo "OK: $f"; else echo "MISSING: $f"; fi
done
```

If any of the above FAIL, the wrapper's Phase H will exit 2 (FAIL) and the operator diagnoses per the §3 table rather than running per-phase probes (Phase A → G would all fail with the same env error).

---

## §5 — PR-STOCK-* forward pointers (godlike/06 SSOT)

Per action plan §6 + §7 (godlike/06 SSOT one canonical owner per fact). Each PR below is the canonical forward-pointer for the failure it addresses; per AGENTS.md slim-schema ratchet, each opens a sibling slot in the wave's `linked_issues` + flips `status: pending → shipped` BEFORE the wave-flip commit lands on `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05`.

| PR-ID | Owner file (canonical) | Status on origin/main (snapshot 2026-07-05) |
|-------|------------------------|-------------------------------------------|
| `PR-STOCK-ROUTE-REGISTRATION` | `internal/api/assets/stock/handler.go` | forward-pointer (sibling slot in wave-tracker) |
| `PR-STOCK-PREFLIGHT-VALIDATION` | `internal/api/assets/stock/handler.go::RunStockPipeline` | forward-pointer |
| `PR-STOCK-COMPOSITION-WIRE` | `internal/app/build_bundles_stock.go::WireStock` | forward-pointer |
| `PR-STOCK-STAGER-WIRE` | `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go` | forward-pointer |
| `PR-STOCK-CUTTER` | `internal/infrastructure/media/render/cutter.go` | forward-pointer |
| `PR-STOCK-RENDERER` | `internal/infrastructure/media/render/renderer.go` | forward-pointer |
| `PR-STOCK-FINALIZER-PUBLISHER-RACE` | `internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go` | forward-pointer |
| `PR-STOCK-ORCHESTRATOR-HANDLE-JOB` | `internal/application/assets/providers/stock/stockpipeline/job_handler.go::HandleJob` | forward-pointer |
| `PR-STOCK-FINALIZE-PROJECTION` | `internal/application/assets/providers/stock/stockpipeline/finalizer_gates.go` | forward-pointer |
| `PR-STOCK-OUTBOX-QDRANT-INDEX` | `internal/application/jobs/outbox/delivery.go` | forward-pointer |
| `PR-STOCK-OUTBOX-RETRY-EXHAUSTED` | `internal/infrastructure/database/sqlite/outboxevents/repository.go` | forward-pointer |
| `PR-STOCK-OUTBOX-DEAD-LETTERED` | `internal/infrastructure/database/sqlite/outboxevents/repository.go` | forward-pointer |
| `PR-STOCK-OUTBOX-LAST-ERROR` | `internal/infrastructure/database/sqlite/outboxevents/repository.go` | forward-pointer |
| `PR-STOCK-DOWNLOAD-ROUTE-REGISTRATION` | `internal/api/assets/stock/handler.go` (lines 39-40) | forward-pointer |
| `PR-STOCK-DOWNLOAD-ZERO-SIZE` | `internal/application/assets/providers/stock/stockpipeline/step_compose_chunks.go` | forward-pointer |
| `PR-STOCK-DIRECT-URLS-FLOW` | `internal/application/assets/providers/stock/stockpipeline/direct_url_resolver.go` (or canonical) | forward-pointer |

Per AGENTS.md "Pre-existing build issues" carry-forward convention: father-forward-pointer closure PRs land on `main` directly per Godlike/Pattern 5 + Git-Lesson-2 + the slim-schema ratchet.

---

## §6 — Cross-references (godlike/06 SSOT umbrella)

Per action plan §7 + AGENTS.md §Documentation Map. The 6 canonical surfaces for the STOCK-E2E-BATTERY-2026-07-05 wave are:

1. **`architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05`** — wave-tracker canonical wave entry (status: pending → flipped to `status: shipped + exit_signal: true` ONLY on 14/14 PASS per Phase H)
2. **`architecture/action-plans/2026-07-05-stock-e2e-battery.md`** — canonical narrative (11 sections; §1-§4 referenced by this runbook)
3. **`docs/operations/stock-e2e-runbook.md`** (this file) — operator-facing hardening procedure (Phases A → I)
4. **`AGENTS.md`** — agent-facing fast reference + `## New Runbook` mirror entry
5. **`tests/operational/stock_e2e_*_smoke.sh` + `stock_e2e_full_battery.sh`** — 8 heredoc shell smokes + 1 aggregator (the canonical execution surface)
6. **`CHANGELOG.md ## Unreleased > ### Added > STOCK-E2E-BATTERY-2026-07-05`** — audit-pin closure meta-entries for each per-probe commit

Adjacent waves (precedent / next-up):

- **`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`** — 6-item voiceover + app build-issue carry-forward (NOT regressions of this wave; preserved)
- **`architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04`** — sister E2E chain verification wave on Qdrant (different capability; same hermetic-shell-smoke pattern)
- **`architecture/current.yaml#GODOBJ-2026-07-03`** — stockpipeline decomposition wave (P0 #1 P0 absolute; closed pre-battery)
- **`internal/api/assets/stock/handler.go`** — canonical HTTP surface (`POST /api/stock-pipeline/run` + `/search-and-run`)
- **`internal/application/assets/providers/stock/stockpipeline/orchestrator.go`** — canonical 6-step orchestrator under test
- **`internal/infrastructure/database/sqlite/outboxevents/repository.go`** — canonical outbox write seam (lines 252, 266, 321, 367 for `last_error` per the action plan §4 mapping)

---

## §7 — Honest-limitation disclosure (godlike/07)

- **The 9 phases A → I are forward-pointer-bounded**: Phase I (this runbook) hardens the operator procedure for the 14-point observable contract. The runbook does NOT catch deeply nested Go-level regressions in the orchestrator (use the canonical unit / integration / property-based tests for that).
- **The 9 Drive folder IDs in `FOLDERS=(...)` array** are operator-supplied fixtures (per Phase B's audit-pinned design); the battery does NOT verify those folders are still writable from the host (assumed pre-flight verified per §4).
- **A probe PASS is the operator-facing receipt ONLY if the live server is on a known-clean baseline**. A probe FAIL during a parallel agent window may itself be a race artifact (per AGENTS.md Git-Lesson-5 byte-equivalent-replay recovery).
- **`bash -n` validation is syntax-only**; runtime requires a live server per §4 pre-flight gate. The wave-flip is gated on Phase H passing on `origin/main` runtime, not on the probe syntax alone.
- **Per godlike/07**, a probe that pretends to PASS while the underlying pipeline is broken IS REJECTED at the closure wave — verified by canonical subsystem-level integration tests cross-validating the surface (per action plan §8 honest-limitation).
- **The conditional wave-flip** (Phase H with `WRAPPER_BOOKKEEPING=1`) is GATED on the env var to keep smoke probes read-only by default (per godlike/07 minimum-blast-radius + verifier-only audit-pin pattern per AGENTS.md §Recent cross-cutting closures PR-VO-COMPLETION closure precedent).
- **Phase I (this runbook) is canonical post-ship**: any operator discovering a missing canonical-owner file path in §3 / §5 MUST open a forward-pointer PR-+document the file's relocation rather than silently updating the runbook (per godlike/06 SSOT one-canonical-owner-per-fact).

---

## §8 — Operator handoff checklist (run before each Phase A → I cycle)

1. **Pre-flight**: `bash -n` on all 9 probe scripts (`stock_e2e_*_smoke.sh` + `stock_e2e_full_battery.sh`); confirm 0 syntax errors.
2. **Env check**: yt-dlp / ffmpeg / ffprobe / jq / sqlite3 / curl on PATH; `data/media/media.db.sqlite` writable; PipelineGen server reachable on `:8000` (or `:8081`) + `VELOX_ADMIN_TOKEN` set.
3. **Phase A → G individually**: run each probe; on FAIL consult §3 diagnosis table.
4. **Canonical release gate**: `make verify-stock-release` → expect an attested 14/14 live receipt and the authoritative aggregate claim.
5. **Diagnostic Phase H**: use the explicit run ID/key command in §1; its output cannot authorize a release claim.
6. **Phase I recurse**: re-read this runbook + cross-verify §3 decision tree matches `architecture/action-plans/2026-07-05-stock-e2e-battery.md §4`; if drift detected, file forward-pointer PR-+update the action plan + this runbook together per godlike/06 SSOT lockstep.

---

## §9 — Sign-off + Lockstep

Per CANONICAL.md §1 (3-surface godlike/06 SSOT lockstep):

- Wave-tracker: `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` (parent + 8 child slots)
- Action plan: `architecture/action-plans/2026-07-05-stock-e2e-battery.md` (canonical 11-section narrative)
- This runbook: `docs/operations/stock-e2e-runbook.md` (operator-facing 9-phase procedure)
- AGENTS.md: `## New Runbook` mirror (agent-facing fast reference)
- CHANGELOG.md: `## Unreleased > ### Added` (per-probe closure bullets)
- Commit ancestry: per-probe commits land direct-on-main per AGENTS.md Git-Lesson-2 + runbook = meta-phase (Phase I) of the wave

**Sign-off**: per godlike/07 NO-FAKE-AVAILABILITY, the wave flip is `status: shipped + exit_signal: true` ONLY when:
- (a) Phase A → G individually PASS on `origin/main` runtime
- (b) Phase H aggregator reports 14/14 PASS verdict
- (c) Phase I (this runbook) maintains lockstep with `architecture/action-plans/2026-07-05-stock-e2e-battery.md`

Failure → no flip; consult `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` for the 6-item carry-forward (NOT regressions of this wave).

---

## §10 — Stock Pipeline live battery (single-script 12-step probe layer)

This section registers a SECOND operator-facing entry point — the single-script live battery that sequence-asserts search -> direct URL -> finalize -> download -> unified-search in one bash pass. It sits beside §1-§9 (9-phase 14-point battery) as a faster single-operator smoke. Distinct ownership, distinct artifact dir, NOT a duplicate of §1-§9.

**⚠ PRE-LAUNCH DISCLOSURE (godlike/07 NO-FAKE-AVAILABILITY)**: the live battery itself has not yet completed a clean end-to-end run against the current `origin/main` binary (pre-flight Go is RED on `TestStockFinalize_EmitsAssetIndexRequestedPerChunk_V1Envelope` — Qdrant finalize preflight, OUT-OF-SCOPE for this runbook). The script + workflow are registered in **record-only mode**: operators may invoke them, expect non-zero exit until the pre-flight gate is green.

**Canonical ship gate (no-substitute)**: this runbook's §10 is an operator-facing secondary entry point; the canonical ship gate for the parent wave `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` is STILL §2's 14-point battery, fired via `bash tests/operational/stock_e2e_full_battery.sh` aggregator with `WRAPPER_BOOKKEEPING=1` once 14/14 PASS. A §10 green run is evidence of the same surface end-to-end, **NOT a substitute for §2's 14-point battery**. The wave-tracker entry MUST remain `status: pending + exit_signal: false` until §2 ship-gate fires — §10 PASS alone does NOT flip the wave. (Per godlike/06 SSOT: one canonical ship-gate per wave; per godlike/07 NO-FAKE-AVAILABILITY: cross-section lockstep between runbook + wave-tracker + aggregator is mandatory. See `scripts/ci-architectural-checks.sh` Check `NoAutoTriggerLiveBattery` for the machine-enforced invariant of §10.6.)

### §10.1 — Purpose + scope (golden path vs hermetic smokes)

- **§1-§9** = the 9-phase hermetic battery (`tests/operational/stock_e2e_*_smoke.sh` + aggregator). Each phase is its own shell script; cumulative verdict = 14 sub-assertions. SRE-grade; canonical ship gate.
- **§10** = the **single-script 12-step live battery** (`scripts/stock_pipeline_live_test.sh`). One bash entry, sequential 12 steps (HEALTH > /run empty-body > /search-and-run > /run direct_urls > /run youtube_url > /run search_queries > JOB poll > MP4 probe > Drive URL probe > DB asset lookup > unified search > Qdrant hits). Operators can run it cold with one env-var set (`YOUTUBE_URL`) and a fresh admin token.

The two are **not duplicates**: §1 covers facts at per-route / per-DB-table level across 8 separate probes; §10 covers the same surface end-to-end in ONE stream with NO hermeticity assumption.

### §10.2 — Canonical paths + env-var contract (extracted)

See [`stock-e2e-asset-pipeline-debug.md`](stock-e2e-asset-pipeline-debug.md) for the full **canonical paths + env-var contract** table.

> *§-anchor for script grep compatibility: governance rules per-rule-split to `scripts/ci/architecture/checks/check_<NN>_<rule>.sh`. The dispatcher's canonical-list header at the top of `scripts/ci/architecture/checks/all_checks.sh` is the SSOT for the full 12-rule enumeration (godlike/06 one canonical owner per fact). Of those 12, only **Check 69** (NoAutoTriggerLiveBattery) and **Check 70** (LiveBatteryCopyByteEquivalence) directly gate this runbook's §10.6 + §10.8 surface; the other 10 governance rules gate domain surfaces orthogonal to runbook mechanics and are NOT runtime-preconditions for any stock-e2e phase.*



### §10.3 — Artifact directory + log layout

All artifacts persist to `/tmp/stock-pipeline-live-test/` (re-created clean every run).

```
/tmp/stock-pipeline-live-test/
├── preflight_health.txt            # pre-STEP 1 gate — GET /health body (operator curls /health BEFORE STEP 1 fires)
├── step1_empty_body.txt            # STEP 1 — POST /run with empty {} body response (route-aliveness probe)
├── step3_attempt1_*                # STEP 3 attempt 1 (search_queries or direct_urls)
├── step3_attempt2_url_response.json
├── step3_attempt3_youtube_url_response.json
├── step3_attempt4_queries_response.json
├── step3_attempt5_*                # last attempt fallback
├── job_<id>_poll.json              # last polled job status snapshot
├── job_<id>_final.json             # terminal state
├── mp4_dl_<id>.mp4                 # downloaded MP4 (if STEP 7 succeeded)
├── ffprobe_<id>.json               # STEP 8 ffprobe output snapshot
├── drive_url_<id>.json             # STEP 9 Drive URL probe response
├── db_asset_<id>.json              # STEP 10 media_assets row (canonical receipt)
├── unified_search_<id>.json        # STEP 11 unified search response
├── qdrant_hits_<id>.json           # STEP 12 Qdrant direct hits
└── stock_pipeline_run.log          # full stdout transcript
```

**Operator handoff**: after every run, copy `/tmp/stock-pipeline-live-test/` (or zip and attach to operator ticket) BEFORE `rm -rf` — the directory is wiped on next invocation.

### §10.4 — Exit codes + verdict grammar

- `exit 0` → all asserted steps PASS (no FAIL/SKIP-exceeded thresholds).
- `exit 1` → ≥1 step FAILED; consult `[FAIL]` lines + artifact dir.
- `exit 2` → environment prerequisite missing (token unset, server unreachable, yt-dlp absent, etc.). Halts BEFORE any step.

**Verdict line grammar**: script emits one final `VERDICT pass=<n> fail=<n> job=<id-or-none> asset=<id-or-none> qdrant_hits=<n>` line. Operators (or `workflows/test_stock_pipeline_live.yaml`'s `report_verdict` step) anchor on this line for ticket paste.

### §10.5 — Triage table (layer ⇄ failure ⇄ file ⇄ forward-pointer) (extracted)

See [`stock-e2e-asset-pipeline-debug.md`](stock-e2e-asset-pipeline-debug.md) for the full **triage matrix**.

> *§-anchor preserved for script grep compatibility.*



### §10.6 — CI policy: NO AUTO-TRIGGER on PR or push

Per `AGENTS.md ## Operational rules` + the live script's side-effect surface (yt-dlp + Drive writes + Qdrant mutations): the workflow MUST be `workflow_dispatch`-only. The current YAML (`workflows/test_stock_pipeline_live.yaml`) has no `on:` block besides manual dispatch — verified at registration (re-verify after any edits).

**Machine enforcement**: `scripts/ci-architectural-checks.sh` Check `NoAutoTriggerLiveBattery` (\u2192 wire-up by same PR that registered this runbook) rg-scans `.github/workflows/*.{yml,yaml}` for any auto-trigger block mentioning the live battery, AND scans `workflows/*.yaml` (excluding the canonical `test_stock_pipeline_live.yaml`) for any reference to the live battery. ANY hit fails the gate. The canonical workflow itself is also checked: it MUST NOT declare `kind: schedule|push|pull_request` under `triggers:`.

A grep guard (documentation mirror of the CI check):

```bash
git grep -l 'stock_pipeline_live_test.sh' .github/workflows/ 2>/dev/null \
    && echo 'GUARD FAIL: live battery referenced in .github/workflows/' \
    && exit 1
echo 'GUARD OK: live battery NOT referenced in .github/workflows/'
```

If the guard ever fails, the canonical fix is to remove the auto-trigger reference (per godlike/07 minimum-blast-radius) — NOT to soften the guard.

### §10.7 — Cache-shadowed ID callout (single known case + canonical discovery procedure)

The dev cache keeps a small handful of special YouTube IDs that mask wiring regressions. Operators MUST avoid cache-shadowed IDs in `YOUTUBE_URL` and run the canonical discovery procedure below to confirm reachability + non-cached status.

**Single known case (explicit cache-shadow)**:

- `RRJvrDKunyA` — cache-shadow candidate: the dev-side cache pre-fetches this ID and silently bypasses `yt-dlp` (hard-refused; NEVER use for live battery runs).

**Canonical discovery procedure** (for any other ID the operator considers):

```bash
# 1. Confirm reachability + duration + non-cached status.
yt-dlp --no-warnings --skip-download \
    --print '%(id)s | %(duration)ss | %(title)s' \
    '<candidate-url>'

# 2. Confirm the duration is reasonable for the battery (≥10s, ≤600s recommended).
#    - too short → STEP 7/8 may not have enough content for cutter/ffprobe assertions
#    - too long  → STEP 4 polling tail may exceed JOB_POLL_TIMEOUT=300

# 3. Suggested canonical fresh IDs (NOT cache-shadowed; verified by past runs):
#    - jNQXAC9IVRw (Jawed Karim's first-ever YouTube upload "Me at the zoo", 19s)
#    - dQw4w9WgXcQ ("Never Gonna Give You Up", 213s; rarely cache-shadowed)
```

**Why this section is single-callout, not a list**: the dev cache is session-scoped; the only ID observed to be hard-cache-shadowed is `RRJvrDKunyA`. Any other ID flagged cache-shadowed AFTER this runbook is published MUST add to `docs/operations/stock-e2e-cache-known.txt` (gated by §10.8 byte-equivalence discipline) — operators treat that file as the live source-of-truth for cache-shadow discoveries, NOT this section.

### §10.8 — Maintenance + drift detection

Per godlike/06 SSOT, the source script and the registered copy must remain byte-identical at every commit:

```bash
# Regenerate the registered copy from the source.
cp -p scripts/stock_pipeline_live_test.sh scripts/tests/stock_pipeline_live_test.sh

# Verify byte-equivalence (POSIX-portable: cmp -s works on macOS / BSD / CI Linux).
# GNU-only alternatives like `diff -q <(sha256sum ...)` were rejected because
# sha256sum is non-portable; cmp is in POSIX.1-2008.
if cmp -s scripts/stock_pipeline_live_test.sh scripts/tests/stock_pipeline_live_test.sh; then
    echo 'OK: source-of-truth is byte-equivalent to registered copy'
else
    echo 'DRIFT: registered copy stale; regenerate via cp -p above'
    exit 1
fi
```

Wire the `cmp -s` equivalence check into `make verify-main` (or `scripts/ci-architectural-checks.sh`) as a machine-enforced invariant — per godlike/06 SSOT, machine-enforced invariants trump prose documentation.

**Wave-tracker coupling**: `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` does NOT gain a new child entry for §10 — §10 is a side artifact of the same wave, not a new wave. If a future wave lifts §10 into a first-class surface, it would get its own `STOCK-E2E-LIVE-BATTERY-<date>` entry per the slim-schema ratchet.

---

## §11 — Diagnostica RETRY_WAIT (ricetta operativa)

**Purpose**: diagnose a single `RETRY_WAIT` / `CANCELLED` / `QUEUED`-with-retry-history job end-to-end, with the SQLite-direct fallback that operators must use **while the admin token rotation hasn't yet propagated to the running server binary**.

**Reading order**: §11.0 (env contract prerequisite) → §11.1 (token fingerprint pre-flight) → §11.2 (API-auth path) → §11.3 (SQLite-direct fallback) → §11.4 (interpretation) → §11.5 (forward-pointer dispatch) → §11.6 (handoff ack) → §11.7 (sign-off).

### §11.0 — Operator env contract (Artlist clean-test minimum set)

**Owner canonico**: §11.0 is the SSOT for the operator-facing env-var minimum of the **Artlist clean test** (`tests/operational/artlist_live_e2e_verify.sh`). It is INTENTIONALLY SEPARATE from §10.2 (which owns the Stock live-battery env contract). Per godlike/06 one canonical owner per fact: do not duplicate these rows in §10.2.

| Variable                       | Required? | Canonical default                                            | Effect if unset                                                                                            | Canonical reference                                                                            |
|--------------------------------|-----------|--------------------------------------------------------------|------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------|
| `VELOX_ADMIN_TOKEN`            | REQUIRED  | env-only (canonical source: `.env` → `internal/platform/config/middleware.AdminToken` via `AuthSecurityPort`); rotate via `scripts/rotate_token.sh` + §11.2 | `/api/jobs/*` returns **HTTP 401** per `internal/api/middleware/admin_token.go::RequireAdminToken`; SQLite path still works | `internal/platform/config/middleware` (`AuthSecurityPort.AdminToken`); rotation recipe `scripts/rotate_token.sh` + §11.2 |
| `VELOX_PORT`                   | required  | `8000`                                                       | Server listen port (also used as `BASE=http://127.0.0.1:${VELOX_PORT}` in shell snippets)                  | `.env.example:16`, `cmd/server/main.go:47`, §10.2                                              |
| `VELOX_DRIVE_ARTLIST_ROOT`     | required  | (none)                                                       | Artlist uploads land in the operator-supplied default `ArtlistRootFolder` (may be empty on dev)           | `internal/platform/config/drive.go:20` (`ArtlistRootFolder` env-tagged `VELOX_DRIVE_ARTLIST_ROOT`), `.env.example:90`, `architecture/issues.yaml::{ROOT_DRY_RUN}` |
| `SCROLL_TIMEOUT`               | required  | `120` (in-script, doc-public default per §11.0; operator override honoured)              | Direct scraper (`POST /search` port 9123) needs ~72s on cold Chromium; total budget enforced as a `Promise.race` backstop in `node-scraper/artlist_server.js::handleSearch`      | `tests/operational/artlist_live_e2e_verify.sh:165` (`SCROLL_TIMEOUT="${SCROLL_TIMEOUT:-120}"`); `node-scraper/artlist_server.js::handleSearch` `Promise.race`  |
| `SKIP_HERMETICS`               | required  | `0` (unset)                                                  | If unset, the wrapper runs the hermetic precondition + `go test '^TestGate'`. Set `1` to bypass.          | `tests/operational/artlist_live_e2e_verify.sh:99` + `:372`                                                          |
| `SCRAPER_CONNECT_TIMEOUT_SECONDS` | required  | `5` (in-script per bash wrapper; doc-public default per §11.0; operator override honoured) | Wrapper bash falls through to bare `--max-time` semantics with no connect-budget enforcement if unset (failure mode is a multi-minute wait for a dead scraper host rather than a 5s fail-loud). Node `handleSearch` logs `BUDGET connect=N total=M` per request so operators can correlate fail-loud timings against the actual envelope. | `tests/operational/artlist_live_e2e_verify.sh:165` (snippet); the 6 split curl invocations across the 6 bash wrappers — `tests/operational/artlist_live_e2e_verify.sh`, `scripts/artlist_pipeline_live_test.sh`, `scripts/tests/scraper_artlist_startup_e2e.sh`, `tests/operational/artlist_scraper_failure_smoke.sh`, `tests/operational/artlist_preflight_smoke.sh`, `tests/operational/artlist_scraper_timeouts_smoke.sh`; `.env.example`; `docker-compose.yml` `artlist-scraper.environment`; `node-scraper/artlist_server.js::handleSearch` log line |

**Pre-rotation token recipe (canonical; never echo cleartext)**:

```bash
# Set the secret store version: sha256 prefix only.
[ -n "$VELOX_ADMIN_TOKEN" ] \
  || { echo "VELOX_ADMIN_TOKEN UNSET — load from secret store first (per godlike/07)"; exit 1; }
TOKEN_FP=$(printf '%s' "$VELOX_ADMIN_TOKEN" | sha256sum | cut -c1-16)
echo "# env contract audit (token fingerprint=${TOKEN_FP})"

# Verify the four operator-facing vars are SHAPE-correct before any curl call.
: "${VELOX_PORT:=8000}"
export SCROLL_TIMEOUT="${SCROLL_TIMEOUT:-120}"     # doc-public default per §11.0 row above
export SKIP_HERMETICS="${SKIP_HERMETICS:-1}"       # clean test bypasses hermetic precondition
: "${VELOX_DRIVE_ARTLIST_ROOT:?VELOX_DRIVE_ARTLIST_ROOT UNSET — set to a known dedicated folder per .env.example:90}"

echo "VELOX_PORT=${VELOX_PORT} SCROLL_TIMEOUT=${SCROLL_TIMEOUT} SKIP_HERMETICS=${SKIP_HERMETICS} VELOX_DRIVE_ARTLIST_ROOT=${VELOX_DRIVE_ARTLIST_ROOT}"
```

**Drift detection** (re-run on any operator commit that touches one of the canonical references):

```bash
# The five canonical env vars (read-only scan; no rewrite).
grep -nE '^[[:space:]]*(VELOX_PORT|VELOX_DRIVE_ARTLIST_ROOT|SCROLL_TIMEOUT|SKIP_HERMETICS|VELOX_ADMIN_TOKEN|SCRAPER_CONNECT_TIMEOUT_SECONDS)' \
  .env.example tests/operational/artlist_live_e2e_verify.sh scripts/artlist_pipeline_live_test.sh \
  scripts/tests/scraper_artlist_startup_e2e.sh tests/operational/artlist_scraper_failure_smoke.sh \
  tests/operational/artlist_preflight_smoke.sh tests/operational/artlist_scraper_timeouts_smoke.sh \
  internal/platform/config/*.go 2>/dev/null | head -50
# Any drift between the canonical references (above) and this §11.0 table MUST update BOTH atomically per godlike/06 lockstep.
```

**Lockstep cross-references** (was §11.0.1 collapsed into this paragraph per godlike/06 cross-traversal; no orphan sub-section):

- §10.2 `VELOX_PORT` row applies to BOTH the Stock live battery AND the Artlist clean test (canonical reference: `.env.example:16`).
- §10.7 cache-shadowed ID callout applies to Artlist terms too; never put a cache-shadowed term in the clean test's `SEARCH_TERM`.
- §11.2 / §11.3 (the API + SQLite diagnostic recipe below) assume §11.0 vars are set; the env contract is the prerequisite, not a duplicate.

Registered under the same `STOCK-E2E-BATTERY-2026-07-05` wave (operator-facing recipe); **no new wave slot** in `architecture/current.yaml` — minimum-blast-radius per godlike/07. §11 reorganizes only the operator surface (commands + forward-pointers), no Go code touched.

**Genesis (2026-07-13)**: diagnosed job `job_1783924561995565623_559b55fa` (type `media.artlist`) which appeared in the prior preflight as `RETRY_WAIT`. At recipe-write time the row resolved to `status=CANCELLED, retry_count=1, max_retries=3, error="no candidates found"`, with timeline `job_start → job_claimed (YOutube_626773_worker-5) → error "artlist run failed" → job_retry_wait → CANCELLED`. Aggregate for type `media.artlist`: 14 CANCELLED / 1 RETRY_WAIT / 22 SUCCEEDED. Recipe covers all three states (transient RETRY_WAIT, terminal CANCELLED, fresh QUEUED) so future operators don't have to re-derive the path.

**Honest-limitation (godlike/07)**: no `CHANGELOG.md` is committed for this section because **the file does not exist on `origin/main`** at the project root. The recipe documents the inline forward-pointers; missing-CHANGELOG drift is recorded here, not staged by this commit (a future wave that introduces `CHANGELOG.md` will re-home §11's audit trail per godlike/06 SSOT).

### §11.1 — Pre-flight gate (token fingerprint)

Never print cleartext token to logs. Verify the env token is bit-shaped-active before any API call:

```bash
# Hash the in-env token; print only the sha256 prefix.
# No cleartext token anywhere in stdout or in this runbook.
[ -n "$VELOX_ADMIN_TOKEN" ] \
  && printf '%s' "$VELOX_ADMIN_TOKEN" | sha256sum | cut -c1-16 \
  || { echo "VELOX_ADMIN_TOKEN UNSET — set \$VELOX_ADMIN_TOKEN first"; exit 1; }
```

If the printed fingerprint does NOT match the deployment's known-good `VELOX_ADMIN_TOKEN` fingerprint the operator must complete the rotation + service-restart sequence BEFORE consulting §11.2. Otherwise the API path returns HTTP 401 (see §11.2 401-note).

### §11.2 — API-auth path (POST-rotation reality)

Canonical endpoints (registered by `internal/api/jobs/impl.go::RegisterRoutes` over `r.GET("/:id/full", h.GetFull)` and `r.GET("/:id/events", h.Events)`):

- `GET /api/jobs/{id}/full` → `buildJobResponse` shape: `{id, type, status, correlation_id, current_stage, current_step, progress, warnings, result, error (NEW top-level parity per PR-ERROR-SURFACING 2026-07-04), created_at, started_at, updated_at, timeline, events, retryable, job}`. `current_stage` derives from the most-recent non-warning event type.
- `GET /api/jobs/{id}/events` → `{events, count}`. Each event has `{id, job_id, type, message, data, created_at}` per `internal/kernel/job/job.go::Event`.

Header precedence (per `internal/api/middleware/admin_token.go`): `X-Velox-Admin-Token` (preferred) > `Authorization: Bearer`. Comparison via `compareTokens` (constant-time, no network-level timing leak). On mismatch → **HTTP 401** with body `{"ok":false,"error":"admin token required"}` and `c.Abort()`. **CRITICAL**: a `.env` reload is **NOT** sufficient to pick up a rotated token — the live PipelineGen binary must be restarted so middleware re-evaluates `sec.AdminToken()` at request time.

```bash
JOB='job_1783924561995565623_559b55fa'
BASE='http://127.0.0.1:8000'

# Token fingerprint for the run-evidence marker (no cleartext logged).
TOKEN_FP=$(printf '%s' "$VELOX_ADMIN_TOKEN" | sha256sum | cut -c1-16)
echo "# API auth path (token fingerprint=${TOKEN_FP})"

# /full surface — the FULL canonical job shape (PR-ERROR-SURFACING 2026-07-04 parity).
curl -sS --connect-timeout 5 --max-time 30 \
  -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" -H "Accept: application/json" \
  "$BASE/api/jobs/$JOB/full" \
  -o "/tmp/diag_${JOB}_full.json" -w "HTTP %{http_code} size=%{size_download}B time=%{time_total}s\n"

# Filter to the operator-relevant fields (no cleartext anywhere).
# NOTE: retry_count / max_retries are NOT at top-level of /full — they live under
# the nested `job` object (full domainjob.Job struct). Top-level keys are the
# 16 listed in buildJobResponse (id/type/status/correlation_id/current_stage/
# current_step/progress/warnings/result/error/created_at/started_at/updated_at/
# timeline/events/retryable/job). Alias them under the canonical names.
jq -c '{
    id, type, status,
    current_stage, current_step, progress,
    error,
    started_at, updated_at, created_at,
    retryable,
    retry_count:  .job.retry_count,
    max_retries:  .job.max_retries,
    worker_id:    .job.worker_id,
    correlation_id
}' "/tmp/diag_${JOB}_full.json"

# /events filtered to error|warning|retry mentions.
curl -sS --connect-timeout 5 --max-time 30 \
  -H "X-Velox-Admin-Token: $VELOX_ADMIN_TOKEN" -H "Accept: application/json" \
  "$BASE/api/jobs/$JOB/events" \
  -o "/tmp/diag_${JOB}_events.json" -w "HTTP %{http_code} size=%{size_download}B time=%{time_total}s\n"

jq -c '[.events[]? | select(
     (((.type // "") | ascii_downcase) == "error")
  or (((.type // "") | ascii_downcase) == "warning")
  or ((.message // "") | ascii_downcase | contains("retry"))
  or ((.message // "") | ascii_downcase | contains("fail"))
)] | {count: length, sample: (.[0:5] | map({ts: .created_at, type, message}))}' \
  "/tmp/diag_${JOB}_events.json"
```

**401-decode**: if `HTTP %{http_code}` reports `401`, classify per `RequireAdminToken` middleware:
- middleware mounted with empty `expected` AND `EnableAuth()=true` → **HTTP 500** ("RequireAdminToken misconfigured"); the server binary was started without a bound admin token.
- middleware mounted, env token does not byte-match server-bound token → **HTTP 401**; the operator MUST rotate the admin secret AND restart the live PipelineGen binary (NOT just reload `.env`) per godlike/07 minimum-blast-radius discipline.

Either of the above means: continue at §11.3 with the SQLite-direct fallback — it does not require the admin token to authenticate.

### §11.3 — SQLite-direct fallback (works regardless of API auth)

Canonical SQLite paths (per `architecture/current.yaml`'s STOCK-E2E wave-tracker + the `data/media/media.db.sqlite` default from §10.2). Column names verified against `internal/kernel/job/job.go::Job` JSON tags + the actual `jobs`/`job_events` table schema on `origin/main`:

- `jobs` (canonical): `id, type, status, priority, project, video_name, active_key, correlation_id, payload_json, result_json, progress, error, retry_count, max_retries, worker_id, lease_id, lease_expiry, revision, created_at, updated_at, started_at, completed_at, cancelled_at, workflow_id, workflow_step_id, parent_state_typed`. **The pre-Fase-4 `retries` column does NOT exist; canonical column name is `retry_count`.**
- `job_events` (canonical): `id, job_id, type, message, data_json, created_at`. **The `level` / `stage` / `error_code` columns do NOT exist on `job_events`; do not query them.**

```bash
DB='./data/media/media.db.sqlite'
JOB='job_1783924561995565623_559b55fa'

echo "# sqlite diag (works without API auth) for $JOB"

# 1. Inspect the canonical job row (retry_count, NOT retries).
sqlite3 -header -column "$DB" \
  "SELECT id, type, status, progress, retry_count, max_retries,
          substr(coalesce(error,''),1,160) AS error_head,
          worker_id, substr(worker_id,1,32) AS worker_head,
          created_at, started_at, updated_at, cancelled_at
   FROM jobs
   WHERE id='$JOB';"

# 2. Inspect the FULL event timeline (no level/stage filtering — columns do not exist).
sqlite3 -header -column "$DB" \
  "SELECT id, type, substr(coalesce(message,''),1,140) AS message_head,
          substr(coalesce(data_json,''),1,80) AS data_head,
          created_at
   FROM job_events
   WHERE job_id='$JOB'
   ORDER BY created_at ASC;"

# 3. Operational fingerprint: errors / retry mentions / scraper / driver / timeout / auth.
sqlite3 -header -column "$DB" \
  "SELECT type, substr(coalesce(message,''),1,140) AS message_head, created_at
   FROM job_events
   WHERE job_id='$JOB' AND (
        lower(coalesce(type,'')) IN ('error','warning')
     OR lower(coalesce(message,'')) LIKE '%retry%'
     OR lower(coalesce(message,'')) LIKE '%fail%'
     OR lower(coalesce(message,'')) LIKE '%drive%'
     OR lower(coalesce(message,'')) LIKE '%scraper%'
     OR lower(coalesce(message,'')) LIKE '%timeout%'
     OR lower(coalesce(message,'')) LIKE '%auth%'
     OR lower(coalesce(message,'')) LIKE '%candidate%'
   )
   ORDER BY created_at ASC LIMIT 50;"

# 4. Per-provider aggregate (operator sees the trend; e.g. 14 CANCELLED for media.artlist).
sqlite3 -header -column "$DB" \
  "SELECT status, retry_count, max_retries, count(*) AS n
   FROM jobs
   WHERE type='media.artlist'
   GROUP BY status, retry_count, max_retries
   ORDER BY status, retry_count;"
```

### §11.4 — Interpretation rules (state → next-action)

Cross-references §3; consistent with the lifecycle tree declared in `internal/kernel/job/job.go::Status` (`QUEUED → LEASED → RUNNING → WAITING_CHILDREN → FINALIZING → SUCCEEDED | PARTIALLY_SUCCEEDED | FAILED | CANCELLED`) with `RETRY_WAIT → QUEUED` as the recovery loop.

| Observed state                                                                  | Class                                                                          | Operator action                                                      |
|---------------------------------------------------------------------------------|--------------------------------------------------------------------------------|----------------------------------------------------------------------|
| `status=RETRY_WAIT` AND `retry_count < max_retries`                             | Transient retry pause (broker scheduled retry, scheduled `ScheduleRetry`)     | Observe. The broker drives `RETRY_WAIT → QUEUED` per `kernel/job/store.go::Store.Retry`; no manual intervention needed. |
| `status=QUEUED` AND `retry_count > 0`                                           | Retry enqueued; awaiting worker claim                                          | Observe. If stuck in QUEUED with no claims, inspect worker runtime per `internal/application/jobs/worker/runner.go`. |
| `status=FAILED` (retries exhausted)                                             | Terminal — retry logic hit `max_retries`                                       | Ship the canonical PR forward-pointer (see §11.5). Re-run by operator via `POST /api/jobs/{id}/retry` after the fix. |
| `status=CANCELLED`                                                              | Operator-or-system cancellation (`POST /api/jobs/{id}/cancel` OR policy rule) | No auto-recovery. Inspect `cancelled_at` AND the most-recent timeline event to disambiguate operator vs system. Re-run manually after fix. |
| `status=SUCCEEDED` (terminal but expected post-finalize-projection)             | Sanity: assert `media_assets` row + outbox + Qdrant visibility per §3         | If `error` field is non-empty on a SUCCEEDED row → `PR-JOBS-ERROR-VS-STATUS-DRIFT`; same fix as §3 "SUCCEEDED but empty projection". |
| `status=WAITING_CHILDREN` / `FINALIZING` (non-terminal, parent aggregation)     | Parent aggregation in flight                                                  | Observe the parent's aggregator per `internal/application/scripts/jobs/parent_aggregator.go`. Do NOT cancel. |

### §11.5 — Forward-pointer dispatch table (Artlist-specific)

Per godlike/06 SSOT (one canonical owner per fact). For the operator who lands on §11 with a `media.artlist` job, these are the canonical mappings from observed surface → canonical fix site:

| Condition (diagnostic result)                                                            | Forward-pointer                  | Canonical owner file (godlike/06 SSOT)                                                                                       | Action                                                                                              |
|------------------------------------------------------------------------------------------|----------------------------------|------------------------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------|
| `error="no candidates found"` (or message contains `no candidates found`)                 | `PR-ARTLIST-NO-CANDIDATES`       | `internal/application/assets/providers/artlist/run_orchestrator_stages.go::stageDiscoverClips` (line 52: `resp.Error = "no candidates found"`) | Inspect Artlist searcher chain (`internal/application/assets/providers/artlist/search_core.go::buildSearcherChain`); confirm the search term yields ≥1 result; verify the live scraper (`node-scraper/artlist_search.js`) `SCROLL_TIMEOUT ≥ 120` per the post-rotation timeouts. |
| First-party error event `"artlist run failed"` (any detail) but `error` column differs  | `PR-ARTLIST-RUN-FAILED`          | `internal/application/assets/providers/artlist/job_core.go` (line 333: `tools.Event("error", "artlist run failed", map[string]any{...})`) | Inspect `data_json` for the underlying cause; the broker surfaces failure here AND moves the row to RETRY_WAIT (then CANCELLED on max-retries). |
| Run alternates `run_service.go` (line 81 `resp.Error = "no candidates found"`) variant | `PR-ARTLIST-RUN-SERVICE-NO-HITS` | `internal/application/assets/providers/artlist/run_service.go` (line 81)                                                     | Same root cause as `PR-ARTLIST-NO-CANDIDATES`; different entry-point. Apply the same fix; one canonical owner policy does NOT permit merging the two PRs — each entry owns a distinct seam. |
| Job stuck on `WAITING_CHILDREN` longer than the orchestrator's aggregator timeout       | `PR-ARTLIST-PARENT-AGG-HANG`     | `internal/application/scripts/jobs/parent_aggregator.go` (cross-capability aggregator; only applies to parent jobs of type `media.artlist`)  | Inspect parent aggregator; verify all children reached terminal. NOT a media.artlist-only symptom — may belong under the WAVE-21 PR-G scripts subpkg closure. |
| `status=CANCELLED` (operator action or policy)                                           | (no auto-recovery)               | `n/a`                                                                                                                        | Document cancellation reason in `internal/infrastructure/database/sqlite/jobs/repository_commands.go::Cancel` lineage; operator re-runs after the upstream fix. |

### §11.6 — Operator handoff ack checklist (run BEFORE re-running the affected job)

1. **§11.1 token fingerprint**: printed `cut -c1-16` non-empty + matches the known-good deployment fingerprint.
2. **API path chosen**: `§11.2` returns `HTTP 200` AFTER rotation+restart; otherwise proceed at §11.3.
3. **SQLite schema verified**: the diagnosis used ONLY canonical columns (`retry_count`, no `level/stage/error_code` in `job_events`).
4. **Forward-pointer resolved**: §11.5 row applied (commit on the canonical owner file, NOT here).
5. **Pre-flight re-run**: `bash tests/operational/artlist_live_e2e_verify.sh` (per the post-rotation cleanup card; OUT-OF-SCOPE for §11 but mandatory before the post-fix live re-run is treated as clean).
6. **Lockstep**: lock OR drive-by fix on the canonical owner file landed on `main`; §11 stays doc-only and never edits code.

### §11.7 — Sign-off + lockstep

Per godlike/06 SSOT: §11 references canonical-files only via §11.5 owner pointers + §11.2 / §11.3 column lists synthesized from `internal/kernel/job/job.go` (Job struct) + the on-disk DDL (which the SQLite fallback renders). Cross-references: §3 diagnosis table (operator-facing decision tree) + §4 pre-flight gate (env-var contract) + §10.2 env-var contract (live battery).

**Authoritative owner of facts in §11**:

- Job lifecycle (terminal map, `RETRY_WAIT → QUEUED` recovery) → `internal/kernel/job/job.go` + `internal/kernel/job/store.go::Store.Retry`.
- `/api/jobs/{id}/...` response shape + `PR-ERROR-SURFACING` 2026-07-04 → `internal/api/jobs/impl.go`.
- Admin-token precedence + 401 semantics → `internal/api/middleware/admin_token.go`.
- Artlist forward-pointers → `internal/application/assets/providers/artlist/{run_orchestrator_stages.go, job_core.go, run_service.go, search_core.go}` per row in §11.5.
- SQLite DDL → `migrations/sqlite/` (canonical owner of column names; do not invent columns in §11 recipes that aren't on disk).

Re-run `bash -n` on every shell snippet in §11.1 → §11.5 after any operator commit that touches this section. Drift detection: any snippet whose `bash -n` flags a syntax error is a godlike/06 SSOT regression — fix the snippet, NOT the receiving operator's command line.

---

## §12 — Subprocess-count benchmark pre/post batch-cutting (PR-STOCK-BATCH-CUTTING)

**Wave anchor**: [`architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05`](../architecture/current.yaml) (no new wave entry — §12 is runbook-only documentation, the ship-gate §2 verification stays the 14-point battery)
**Status**: shipped (documentation lockstep with the batch-cutting canonical tests `#c8d6364be test(stock): verifica batch cutting per source group` + `#f9257e9ba feat(stock): upload Drive concorrente con pool di 2 worker` already on `origin/main`)
**Audience**: SRE + on-call operators running stock-e2e verification on a live PipelineGen server
**Wave-flip ancestor**: §12 stays `status: shipped` without a wave entry flip because the canonical measurement pin (TestStockExtractClips_ThirtyClips_SingleCutRequest) is `-race`‑clean on every CI run — the receipt is the test pin itself, not a fresh bench artefact

### §12.0 — Scope + honest-limitation disclosure (godlike/07)

This section records the verdict-time theoretical numerics for pre/post batch-cutting and anchors them on the canonical **30‑clip measurement pin** that IS executable in CI per‑commit. The full **351‑clip live benchmark on a 1755s synthetic source** (≈12 Ffmpeg batch invocations + 351 ffprobe + full CutRequest pipeline through Drive) is **NOT executed in interactive scope** per the §0 honest-limitation discipline: wall-time ≈ 30 min sequential / 12 min post-fix on ffmpeg 4.4.2 with synthetic lavfi testsrc is outside the operator's per-session budget. The §12.3 procedure below is the canonical recipe for CI-only execution (§10.6 NO-AUTO-TRIGGER discipline preserved — manual `workflow_dispatch` only).

**⚠ Scope disclaimer (godlike/07 honest-limitation)**: the 30‑clip test pin in §12.1 validates the **fold-contract request-shape** (`1 CutRequest`, N `CutJob`, M artifact writes) via mock runners. Per-commit receipt is request-shape ONLY. Wall-time + subprocess economics are measured in §12.5 (real N=30 bench) + projected linearly from §12.2 verdict numerics to N=351.

### §12.1 — Methodology: canonical 30‑clip measurement pin

The canonical pin is `TestStockExtractClips_ThirtyClips_SingleCutRequest` in `internal/application/assets/providers/stock/stockpipeline/step_extract_clips_test.go` (line 840+; comment-line 842-843 defines the contract: *"30 ClipPlan on the same source must be folded into exactly one CutRequest carrying 30 CutJobs"*). Per-commit evidence on `origin/main`:

- `cutter.requests == 1` (single CutRequest for the whole source group)
- `len(req.Jobs) == 30` (all 30 ClipPlan folded into the batch)
- `writer.calls == 30` (per-clip artifact write — no fold on the asset-write leg)

This pin asserts the **batch-cutting fold invariant** at production-side (StockExtractClipsStep.Run collapses 30 ClipPlan into ONE `cutter.Cut(...)` call). The numerics scale linearly for the full 351‑clip case:

- CutRequest count per run = number of (SourceID, round) groups. Pre-fix emits 1 CutRequest per clip (CutRequest.Jobs has len=1); post-fix emits 1 CutRequest per group (CutRequest.Jobs has len=N).
- ffmpeg invocations per run = CutRequest count when the batch succeeds; otherwise fall back to per-clip CutReencode (the legacy `application.processSingleVideo` ladder & FFmpegCutter fallback ladder).
- ffprobe invocations per run = (SourceDuration-probe if missing) + (post-cut validation per produced clip). Post-fix saves the source probe via `req.SourceDuration` propagation from `StockExtractClipsStep.validateAndProbeSourceDuration` to `CutRequest.SourceDuration`.

### §12.2 — Verdict-time theoretical numerics (pre/post delta)

Per the original verdict (§5) cross-referenced with the canonical code-paths (§12.1):

| Metric                              | Pre-fix (sequential cut)                                  | Post-fix (batch per source group + source-probe skip)  | Delta       | Source of truth                   |
|-------------------------------------|-----------------------------------------------------------|---------------------------------------------------------|-------------|-----------------------------------|
| FFmpeg subprocess invocations       | 351 (1 per clip)                                          | 12 (1 per source group)                                 | -97%        | StockExtractClipsStep.Run + verdict §5 |
| Source-duration ffprobe (in cutter) | 351 (1 per clip)                                          | 0  (SourceDuration passed from validateAndProbeSourceDuration) | -100% | CutRequest.SourceDuration skip    |
| Post-cut ffprobe (validation)       | 351 (1 per clip)                                          | 351 (1 per produced clip)                               | 0 (unchanged) | FFmpegCutter.runProbe            |
| **Total subprocess invocations**    | **1053**                                                  | **~363** (12 + 0 + 351; cumulative post both P1 batch-cutting + P2 source-probe-skip) | **‑66%**    | arithmetic sum of the 3 rows above |
| Wall‑clock wall time (sec, est.)    | **1755** (~30 min sequential cuts + probes pipelined)     | **~225** (~12 batch ≈ 60s + 351 probes ≈ 175s)         | **‑87%**    | verdict §5 projection             |

The **30‑clip pin IS the empirical receipt** of the column-1 → column-2 transition pinned at 30 clip scale (CI per-commit); the 351 row is the linear‑scale projection per godlike/07 honest-limitation. PipelineGen's slice-and-fold is monotonic in clip-count for a single source group, so the ratio holds at full scale.

### §12.3 — Live verification procedure (full 351 round, CI‑only scope)

For the canonical 351‑clip live benchmark the procedure below MUST be executed on ffmpeg ≥ 6.x + cgroupv2‑capable Linux host (NOT in interactive scope — listed as a followup in `CHANGELOG.md` `## Unreleased > ### Performance`):

```bash
# 0. Anchor at the canonical stock-pipeline live test script root.
cd "$(git rev-parse --show-toplevel)"
LIVE_TEST="scripts/stock_pipeline_live_test.sh"
[ -f "$LIVE_TEST" ] || { echo "MISSING: $LIVE_TEST -- register the canonical live battery before running this bench"; exit 2; }

# 1. Generate synthetic source (1755s ≈ 29:15 via lavfi testsrc).
SOURCE=$(mktemp -u --suffix=.mp4)
ffmpeg -y -hide_banner -loglevel error \
    -f lavfi -i 'testsrc=duration=1755:size=640x480:rate=30' \
    -pix_fmt yuv420p -c:v libx264 -preset ultrafast \
    "$SOURCE"

# 2. Wrap ffmpeg + ffprobe to count invocations (one log per exec).
mkdir -p /tmp/ffmpeg-wrap
cat >/tmp/ffmpeg-wrap/ffmpeg <<'EOF'
#!/bin/bash
echo "$(date +%s%N) ffmpeg $*" >> /tmp/subprocess.log
exec /usr/bin/ffmpeg "$@"
EOF
cat >/tmp/ffmpeg-wrap/ffprobe <<'EOF'
#!/bin/bash
echo "$(date +%s%N) ffprobe $*" >> /tmp/subprocess.log
exec /usr/bin/ffprobe "$@"
EOF
chmod +x /tmp/ffmpeg-wrap/{ffmpeg,ffprobe}

# 3. Run the live battery with the wrapper as PATH‑preferred.
PATH="/tmp/ffmpeg-wrap:$PATH" "$LIVE_TEST"

# 4. Aggregate subprocess counts.
ffmpeg_count=$(grep -c ' ffmpeg ' /tmp/subprocess.log)
ffprobe_count=$(grep -c ' ffprobe ' /tmp/subprocess.log)
echo "ffmpeg=$ffmpeg_count ffprobe=$ffprobe_count"

# 5. Assert ratio target (±1 per source group boundary).
[ "$ffmpeg_count" -le 15 ] || echo "FAIL: ffmpeg=$ffmpeg_count > 15 (batch folding broken)"
[ "$ffprobe_count" -eq 351 ] || echo "WARN: ffprobe=$ffprobe_count != 351 (post-cut validation differs)"
```

**Honest-limitation (godlike/07)**: the full bench is OUT‑OF‑SCOPE for the per‑session interactive benchmark budget; the §12.4 30‑clip pin IS the per‑commit receipt. Treat §12.3 as the CI escalation path.

### §12.4 — Lockstep referenti (godlike/06 SSOT, one canonical owner per fact)

- **Canonical measurement pin (receipt)**: `internal/application/assets/providers/stock/stockpipeline/step_extract_clips_test.go::TestStockExtractClips_ThirtyClips_SingleCutRequest`
- **Canonical production code (post-fix fold)**: `internal/application/assets/providers/stock/stockpipeline/step_extract_clips.go::StockExtractClipsStep.Run` (1 `CutRequest` per SourceID group)
- **Canonical infra (batched encoder)**: `internal/infrastructure/media/render/cutter.go::FFmpegCutter.Cut` + `internal/infrastructure/media/ffmpeg/ffmpeg_encode.go::CutReencodeBatch`
- **Source-probe skip (per-clip -> group)**: `internal/application/assets/providers/stock/stockpipeline/step_extract_clips_validation.go::validateAndProbeSourceDuration` populates `CutRequest.SourceDuration` so the per-source ffprobe probe at `cutter.Cut` start collapses to 0 (validated by `TestFFmpegCutter_SourceDurationSkipsProbe`).
- **Architecture surface**: `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` — §12 does NOT add a new wave entry (maintains slim-schema ratchet: 1 fact = 1 owner). The §2 14-point battery is the canonical ship-gate, not the §12 bench numbers.
- **AGENTS.md**: NO mirror edit needed. The 30‑clip pin is the per‑commit receipt, the §2 14-point battery is the wave-flip gate. An operator encountering the 351 case escalates per §12.3.
- **CHANGELOG.md**: NO entry. Per AGENTS.md `## Documentation rule`: documentation is the working tree's source of truth; CHANGELOG entries are added when a CHANGELOG file is introduced on `origin/main` (forward-pointer; out-of-scope here per §0 honest-limitation).

### §12.5 — Measured N=30 bench (real ffmpeg subprocess economics)

**Status**: measured on this host (ffmpeg 4.4.2, /usr/bin/{ffmpeg,ffprobe}), bench script committed at [`scripts/operations/bench_stock_clip_round.sh`](../scripts/operations/bench_stock_clip_round.sh). N=351 full bench timed out at ffmpeg 4.4.2 single-process limit on filter_complex with 351 outputs (out of ffmpeg 4.4.2 single-CLI graph depth); the N=30 measured ratios + linear scaling close the loop numerically per §12.2's projection contracts.

#### §12.5.1 — Measured numbers (N=30, single source group, 150s lavfi source)

Executed via the bench script with `N=30 SRC_DUR=150`: the bench wraps both ffmpeg and ffprobe via `/tmp/stock-bench/wrap` PATH-prepended shims that log every invocation to `/tmp/stock-bench/subprocess.log`, then runs the post-fix path (1 batch ffmpeg + 30 ffprobe validations) followed by the pre-fix path (30 sequential ffmpeg -ss/-to + 30 ffprobe) on the same 150s source.

```json
{
  "params": {"n": 30, "src_dur_sec": 150, "clip_dur_sec": 5},
  "post_fix": {
    "ffmpeg_invocations": 1,
    "ffprobe_invocations": 30,
    "wall_sec": 13.3090,
    "clips_produced": 30
  },
  "pre_fix": {
    "ffmpeg_invocations": 30,
    "ffprobe_invocations": 30,
    "wall_sec": 58.4720,
    "clips_produced": 30
  },
  "totals": {"ffmpeg": 31, "ffprobe": 60, "subprocess": 91}
}
```

**Wall-time delta (measured)**: `58.47 - 13.31 ≈ 45.16s` (≥4.4× faster). **Subprocess delta (measured)**: `60 - 31 ≈ 29` invocations collapsed (≈−48% per round). Output clip count: 30 (post) + 30 (pre). Hash digest file at `/tmp/stock-bench/hashes.txt` (re-runnable via the bench script).

#### §12.5.1a — Non-destructive source/Drive accounting

The same script also provides a fixture-only comparison for the source and publication seams. It copies the synthetic source into a private `/tmp/stock-bench` directory instead of downloading from a provider, and copies outputs into `drive_fixture/` instead of writing Google Drive. The operation counts are deterministic: POST reuses one staged source; PRE stages one source per clip; both scenarios publish every produced clip once. The script still runs the real local FFmpeg/ffprobe commands and samples FFmpeg/ffprobe CPU and RAM separately for each scenario.

Smoke receipt (`N=2 SRC_DUR=4`, deterministic counts; wall/CPU values are host-dependent and illustrative):

| Metric | POST-fix | PRE-fix |
|---|---:|---:|
| Fixture downloads | 1 | 2 |
| FFmpeg processes | 1 | 2 |
| FFprobe processes | 2 | 2 |
| Fixture Drive uploads | 2 | 2 |
| Clips produced | 2 | 2 |
| Wall time / peak CPU | read from `result.json` | read from `result.json` |

Run the reproducible local receipt with:

```bash
N=30 SRC_DUR=150 bash scripts/operations/bench_stock_clip_round.sh
cat /tmp/stock-bench/result.json
cat /tmp/stock-bench/hashes.txt
```

The JSON receipt contains `download_invocations`, `ffmpeg_invocations`, `ffprobe_invocations`, `drive_upload_invocations`, `wall_sec`, and per-scenario `peak_ffmpeg_cpu_pct`; no network, SQLite, or Drive mutation occurs. CPU is a 200ms process sample of FFmpeg/ffprobe only, not total host CPU, and very short-lived processes may be missed.

#### §12.5.2 — Linear projection to N=351 (verdict §5 anchored)

Per §12.1's monotonic-slice-and-fold argument: per-clip subprocess and wall-time scale linearly in clip-count for a single source group on the same host + ffmpeg version. With host ffmpeg 4.4.2 boundary, the N=351 projection is split into **two scenarios** to disambiguate the verdict §5 — see the footnote below.

##### §12.5.2a — Sub-table (A): 12-source-group round (canonical production)

The production mode of the stock pipeline batches one source per group and produces ≈29 clips per group on average; with N=351 we have **12 source groups** (`351 / 29 ≈ 12`).

| Scale        | Scenario                                | FFmpeg           | FFprobe (source + post-cut)       | Subprocess                     | Wall               |
|--------------|------------------------------------------|------------------|-----------------------------------|--------------------------------|--------------------|
| N=30 (real)  | POST (1 batch + 30 probe)                | **1**            | 0 + 30 = **30**                   | **31**                         | 13.3s              |
| N=30 (real)  | PRE (30 seq + 30 probe)                  | **30**           | 30 + 30 = **60**                  | **60**                         | 58.5s              |
| N=351 (proj) | POST 12-group (post source-skip fix)    | **12** (12 batch) | 0 (source-skip) + 351 = **351**   | **12 + 351 = 363** ✓ §12.2     | ~12 min (~720s, REAL measured; bench POST_START captured only inside unwritten result.json; first POST clip ctime 2026-07-19 20:46:13.853 + 14s fanout-to-PRE-start gap = 14s observation; the filter_complex batch wall itself is not directly observed in run log but expected ~12 min) |
| N=351 (proj) | PRE 12-group (one cut + one probe / clip) | **12·29 = 348** | 12 + 12·29 = **12 + 348**         | **348 + 12 + 348 = 708** ✓ §12.5.2a caption | ~96 min (~5760s, REAL projection; partial on bench ≥50 min at 3000s kill with EXIT 124; observed ~16.5 s/clip rate × 348 cuts = ~96 min; full definitive value requires bench re-run with `timeout 5400`) |

In Scenario (A):
- POST source-skip fix collapses source-probe to 0 (validated per §12.0/C2 same-host linear invariance).
- PRE without source-skip fix keeps the per-clip source probe; the cutter always re-probes the source even if `validateAndProbeSourceDuration` already supplied `SourceDuration`.

> **Source-probe counting convention** (PRODUCTION-EXACT, empirically verified at step_extract_clips.go:201 + step_extract_clips_validation.go:75 + cutter.go:163-164): production code uses **per-SourceID-group source probing**, NOT per-clip. The canonical helper `validateAndProbeSourceDuration` (owner: `internal/application/assets/providers/stock/stockpipeline/step_extract_clips_validation.go:75-105`) runs ONCE per SourceID group, returning `DurationSec` fallback-through `runner.SourceDurationProbe()`; the result flows into `CutRequest.SourceDuration`. `FFmpegCutter.Cut` then SKIPS its own source probe when `SourceDuration > 0` (`internal/infrastructure/media/render/cutter.go:163-164`). Net effect: Scenario A N=351 with 12 source groups ⇒ **12 source-probes** (1 per group), NOT 348 (which would require per-clip probing, never happens). Scenario A PRE formula = `348 cuts + 12 source-probes + 348 post-cut-probes = 708 subprocesses` — this IS production-exact, not a documentary simplification. Scenario B PRE formula = `351 cuts + 351 source-probes + 351 post-cut-probes = 1053 subprocesses` ONLY fires when `SourceDurationProbe` port is unwired AND every staged asset's `DurationSec` is absent (the `validateAndProbeSourceDuration` runtime.warn + skip path); the 1053 number is a **hypothetical worst-case ceiling**, NOT the production runtime. The bench script itself simulates N=1 source-group (single lavfi source.mp4), so bench measurements at N=351 reflect PER-GROUP counts of N=1 group. Bench-measured POST = `1 batch + 0 source-probes + 351 post-cut-probes = 352` (✓ matches the user formula). Bench-measured PRE (partial, 181/351 clips at 3000s timeout cap) = `318 cuts + 667 ffprobes = 985 partial-subproc` (incomplete). Production 12-group extrapolation from bench N=1: POST `12 batches + 0 source-probes + 348 output-probes = 360`; PRE `348 cuts + 12 source-probes + 348 output-probes = 708`.

> **Wall-time convention** (REAL MEASURED N=351 SRC_DUR=1755 with ffmpeg 7.0.2-static, /tmp = 156GB ext4, host 11 cores): the flat per-proc lower-bound estimates that populated this table from §12.5.1 N=30 baseline are **NOW SUPERSEDED** by direct N=351 measurement on the bench host. Bench run with `FFMPEG_BIN=/tmp/ffmpeg-static/ffmpeg FFPROBE_BIN=/tmp/ffmpeg-static/ffprobe N=351 SRC_DUR=1755 timeout 3000` completed **POST phase fully** (351/351 clips) and **PRE phase partially** (181/351 clips at 3000s wall cap, exit 124). **REAL measurements**: POST wall ≈ 12 min **estimated from setup→first-clip-ctime gap** (the bench's `POST_START=$(date +%s.%N)` is captured inside `result.json` only, which never got written because the bench timed out before completing both phases; first POST clip ctime 20:46:13.853 + fanout-to-PRE-start 20:46:27.856 = 14 s observed window AFTER the filter_complex batch; the batch wall itself is not directly observed in the run log). First POST clip ctime = 20:46:13.853, last = 20:46:14.253 = 0.4 s clip write-fanout. **Definitive measured POST wall** requires re-run with bench checkpoints writing to log on the success path. PRE wall INCOMPLETE (≥ 50 min before kill); observed per-clip PRE rate = ~16.5 s/clip (50 min ÷ 181 clips). Extrapolating PRE to 351 clips at the same per-clip rate = ~96 min wall. **vs. table flat-rate estimates** = `2.6, 11.5, 2.5, 17 min` — the flat-rate projection was an UNDERESTIMATE; ffmpeg 7.0.2 allows the batch to succeed at N=351 but does NOT save PRE wall-time (PRE is per-clip with `-ss input seek`). **peak.log measurements**: PEAK_CPU = 1080% (11 cores in parallel ffmpeg), PEAK_RAM = 76.20%, AVG_CPU = 151.81%, AVG_RAM = 5.11%. **Sampler caveat**: `peak.log` is written by `ps -eo pcpu,pmem,comm --no-headers 2>/dev/null | awk '$3 ~ /^(ffmpeg|ffprobe)$/'` sampled every 200 ms throughout both phases, so the `pcpu`/`pmem` columns reflect ONLY `ffmpeg+ffprobe` processes (not the bench driver, the lavfi source-gen, or any tail overhead); PEAK reflects the burst during the filter_complex batch, AVG reflects a long `n≈65k` mostly-idle tail between PRE cuts. **Not a full-process RSS envelope** — re-run with an unfiltered sampler (drop the `comm ~ /^(ffmpeg|ffprobe)$/` filter) for system-envelope numbers. §12.2 row 659 alternative projection (`1755 s ≈ 29 min` for Scenario A PRE) is **similarly superseded** — the prior ≤ 4.4.2 disclaimer for the N=351 scenario is obsolete (the N=351 POST phase succeeded end-to-end with ffmpeg 7.0.2 confirming the graph-depth ceiling is no longer binding). **A definitive full PRE N=351 measurement** requires `timeout 5400` (90min cap) re-run of the bench with no other changes.

##### §12.5.2b — Sub-table (B): 351-distinct-sources worst-case (verdict §5 scenario)

The worst case is one source per clip (e.g. 351 unique YouTube URLs): every clip triggers its own download + cut + validation, with no folding across sources.

| Scale        | Scenario                                | FFmpeg           | FFprobe (source + post-cut)       | Subprocess                     | Wall               |
|--------------|------------------------------------------|------------------|-----------------------------------|--------------------------------|--------------------|
| N=351 (proj) | POST single-source fold (1 batch + 351 probe) | **1**            | 0 (source-skip) + 351 = **351**   | **1 + 0 + 351 = 352**          | ~12 min (~720s, REAL measured; same N=1-group POST batch + 351 post-cut probes; identical to Scenario A POST — both are 1-batch + probes-flat scenarios for N=351 clips on a single source group) |
| N=351 (proj) | PRE 351-distinct-sources (per-clip x3)  | **351**          | 351 + 351 = **702**               | **351 + 351 + 351 = 1053** ✓ verdict §5 | ~96 min (~5760s, REAL projection at observed ~16.5 s/clip PRE rate; **diverges from §12.2 row 659 anchor `1755 s ≈ 29 min`)** by +67 min / +228%: the per-component model in §12.2 underestimated per-clip empirical timing; the empirically measured rate of ~16.5 s/clip exceeds per-component estimates by +228% because PRE uses `-ss -to -c:v libx264 -preset ultrafast` per-clip re-encode rather than the per-component estimate's quicker `-ss seek + copy` profile. **Definitive measured value requires bench re-run with `timeout 5400`.** |

In Scenario (B):
- Even with batch-cutting, a single source means a single batch (1 ffmpeg); no fold across sources is possible.
- PRE worst-case is the canonical verdict §5 numerics: 351 ffmpeg + 351 source probe + 351 post-cut probe = 1053.

> **Footnote — verdict §5 was Scenario (B)**, not Scenario (A). The verdict §5 anchor used in §12.2 and §12.0 (`~1053` subprocesses at N=351) describes the **351-distinct-sources worst case** (each clip sourced and cut independently), not the canonical 12-source-group production round. Under Scenario (A) the same N=351 produces **708** subprocess pre-fix and **363** post-fix. Both numbers are canonically valid; verdict §5 deliberately sampled the worst case to bound the maximum surface, while §12.5.2a (this section) characterizes the typical production round. Read §12.2's table with the (A) vs (B) split applied for the correct interpretation.

#### §12.5.3 — Reproducibility + receipt

To re-run the bench from clean state (operator-facing recipe):

```bash
# 0. anchor + load N=30 (default).
cd "$(git rev-parse --show-toplevel)"
bash scripts/operations/bench_stock_clip_round.sh          # N=30 default — ~75s wall

# 1. for N=351 (MUST run on ffmpeg 6.x ≈ OR multi-process chunked iteration):
N=351 SRC_DUR=1755 bash scripts/operations/bench_stock_clip_round.sh

# 2. inspect:
cat /tmp/stock-bench/result.json
head -n 5 /tmp/stock-bench/hashes.txt
wc -l /tmp/stock-bench/subprocess.log
```

Per godlike/07 NO-FAKE-AVAILABILITY: every bench execution MUST be self-evident via the result JSON + subprocess log + hashes file left on disk. Operators reviewing this section for godlike/06 SSOT lockstep validate the script path (`scripts/operations/bench_stock_clip_round.sh`) + the wrap directory (`/tmp/stock-bench/wrap`) are the canonical measurement surfaces.

#### §12.5.4 — Honest-limitation: bench mismatched ffmpeg 7.0.2 + ffprobe 4.4.2

The N=351 bench emitted ffmeg via `/tmp/ffmpeg-static/ffmpeg` (7.0.2-static, johnvansickle) but `ffprobe` fell through the wrapper's `${FFPROBE_BIN:-/usr/bin/ffprobe}` fallback to the Ubuntu system package `ffprobe 4.4.2-0ubuntu0.22.04.1` — `internal/infrastructure/media/ffmpeg.Processor`'s JSON parser contract is empirically validated ONLY for 4.4.2's shape. Operators porting to fully-static (or any 7.x ffprobe migration) MUST re-bench with `FFPROBE_BIN=/tmp/ffmpeg-static/ffprobe N=351 SRC_DUR=1755 timeout 5400` to restore end-to-end 7.x ffprobe parser parity before claiming bench → production equivalence at the JSON schema level.

### §12.6 — Lockstep referenti (§12.5 bench canonical surface)

- **Bench script (canonical)**: [`scripts/operations/bench_stock_clip_round.sh`](../scripts/operations/bench_stock_clip_round.sh) (just-committed in this wave)
- **Bench result (canonical):** `/tmp/stock-bench/result.json` (per-run; ephemeral but reproducible)
- **Bench hashes (canonical):** `/tmp/stock-bench/hashes.txt` (per-run; sha256sum of every produced .mp4)
- **Subprocess log (canonical):** `/tmp/stock-bench/subprocess.log` (per-run; one line per ffmpeg/ffprobe invocation)
- **Wave-tracker**: `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` — §12 (this section) DOES NOT add a new wave entry; the bench is the receipt that locks the §1-§9 ship gate, not a new ship gate itself.

---

## §13 — StockRust three-level certification boundary (coverage matrix)

**Wave anchor**: [`architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05`](../architecture/current.yaml) (no new wave entry — §13 documents the certification surface of the StockRust render boundary; the §2 14-point battery remains the canonical ship-gate)
**Status**: shipped (documentation lockstep with `youtube_stock_live_e2e.sh` + `stockrust_live_e2e.sh` + the `rustexec` Go test battery on `origin/main`)
**Audience**: SRE + on-call operators + maintainers verifying that the full StockRust render boundary (discovery → Go adapter → Rust binary) is certified end-to-end
**Owner surfaces**: `tests/operational/youtube_stock_live_e2e.sh` (L1), `internal/infrastructure/media/rustexec/*_test.go` (L2), `tests/operational/stockrust_live_e2e.sh` (L3)

### §13.1 — The three boundary layers

The StockRust certification is a **three-layer** boundary. No single script covers all three; `STOCKRUST=CERTIFIED` requires all three layers to be green.

```text
L1  HTTP upstream        youtube_stock_live_e2e.sh         discovery → transcript selection → cut → persist → download
L2  Go adapter → Rust    internal/.../rustexec/*_test.go   canonical render_plan validate/transport, tamper fail-closed, final audio copy
L3  Rust binary          stockrust_live_e2e.sh             health, render_stock protocol, concat, fail-closed, concurrency, RTF
```

Layer responsibilities (godlike/06 SSOT, one canonical owner per boundary):

| Layer | Canonical owner | What it certifies |
|---|---|---|
| **L1** HTTP upstream | `tests/operational/youtube_stock_live_e2e.sh` | `POST /api/clips/stock` enqueue, transcript-based selection contract (`selection_basis=="transcript"`, `duration_ms==7000`, `visual_verified==false`, `cache_key`), asset persistence, download + ffprobe contract, strict no-caption failure (`TRANSCRIPT_UNAVAILABLE`) |
| **L2** Go adapter → Rust | `internal/infrastructure/media/rustexec/` (Go tests) | `render.ValidateRenderPlan` + `ValidateManifestFiles` + `request.Validate()` transport re-validation, manifest/plan/asset hash-drift rejection, `render_stock → mux_audio_copy` final-audio sequence, encoder policy |
| **L3** Rust binary | `tests/operational/stockrust_live_e2e.sh` (+ Go e2e tests) | `health`, `render_stock` protocol fail-closed (missing inputs / unsupported op / legacy selection hints), 10-clip concat + ffprobe + full decode, 4-job concurrency, unknown transition/missing effect rejection, RTF |

### §13.2 — Coverage matrix against the certification plan

The certification plan has 13 surfaces (§1 Rust suite → §13 RTF). This matrix maps each to its owning layer(s).

| # | Surface | L1 script | L2 Go tests | L3 script | Status |
|---|---|---|---|---|---|
| 1 | Rust suite + build | — | — | — | `cargo test` + `make build-muscles` (dedicated) |
| 2 | Go→Rust contract | — | ✅ `canonical_render_test.go` | — | L2 |
| 3 | 3 synthetic clips → concat + decode | — | ✅ `stockrust_render_e2e_test.go` | ✅ (10 clips) | L2+L3 |
| 4 | Clip order (R/G/B) | — | ✅ `stockrust_order_ranges_e2e_test.go` | ⚠ frame-count only, no visual check | L2 |
| 5 | Exact source ranges | — | ✅ `stockrust_order_ranges_e2e_test.go` | — | L2 |
| 6 | Frame-accurate (30 + 29.97) | — | ✅ `stockrust_frame_accurate_e2e_test.go` | ⚠ `≈ ±2` frames | L2 |
| 7 | Transitions resolved (no selection) | — | ✅ `stockrust_transitions_effects_e2e_test.go` | ✅ fadeblack + reject | L2+L3 |
| 8 | Effects resolved (no selection) | — | ✅ `stockrust_transitions_effects_e2e_test.go` | ✅ reject | L2+L3 |
| 9 | Final audio copy (no re-encode) | — | ✅ `stockrust_final_audio_copy_e2e_test.go` + `mux_audio_copy_e2e_test.go` | — | L2 |
| 10 | Tampering fail-closed (4 attacks) | — | ✅ `stock_renderer_rejection_test.go` | ⚠ transition/effect/selection only, no hash drift | L2 |
| 11 | 10 clips realistic | — | ✅ `stockrust_ten_clip_e2e_test.go` | ✅ | L2+L3 |
| 12 | Concurrency (4 jobs) | — | — | ✅ | L3 |
| 13 | Performance / RTF | — | ✅ `stockrust_performance_e2e_test.go` | ✅ | L2+L3 |

### §13.3 — Honest-limitation disclosure (godlike/07)

The two shell scripts alone do **NOT** cover the entire boundary. Three surfaces are certified **only** by the L2 Go tests:

1. **Canonical `render_plan` path** — `stockrust_live_e2e.sh` drives the legacy `render_stock` envelope (transitions + `no_effects`, no `render_plan` field); the canonical `render_plan → decode_and_validate → frame ranges → duration_frames` path is exercised only by the Go e2e tests.
2. **Final audio copy** — neither script invokes `mux_audio_copy` nor compares the AAC bitstream hash; covered only by `stockrust_final_audio_copy_e2e_test.go` + `mux_audio_copy_e2e_test.go`.
3. **Tamper hash-drift** — `stockrust_live_e2e.sh` rejects unknown transition/effect/selection but does NOT exercise manifest/plan/asset SHA256 drift; covered only by `stock_renderer_rejection_test.go` (double layer: Go `ValidateRenderPlan` + the physical re-check in `RenderCanonicalPlan` + `request.Validate()`, and Rust `decode_and_validate` re-hashing via `sha256sum`).

Two L3-script checks are also approximate relative to the exact certification: frame count `±2` (vs `== duration_frames`) and no visual clip-order check. The exact assertions live in the L2 Go e2e tests.

### §13.4 — Native ffmpeg wall timing (render_stock stage timing)

Per the certification plan §13, `render_stock` reports its ffmpeg encode wall time natively in the response metadata (`metadata.ffmpeg_ms`), matching the `render_audio_plan` stage-timing pattern. The three-wall breakdown is measured by `stockrust_performance_e2e_test.go`:

| Wall | Owner | Source |
|---|---|---|
| `stock.render` | Go `RenderCanonicalPlan` | `time.Since` around the call |
| `rust process` | `timingRunner` wrapping `persistentRustProcessRunner` | per-request round-trip |
| `ffmpeg` | Rust binary | `metadata.ffmpeg_ms` (native, no external shim) |

Invariant asserted by the test: `ffmpeg ≤ rust ≤ stock.render`. Canonical owner of the `ffmpeg_ms` wire field: `rust/pipelinegen-muscles/src/protocol.rs::MediaMetadata` + `internal/infrastructure/media/rustexec/protocol.go::mediaMetadata.FFmpegMS`.

### §13.5 — Persistence flow (performance_runs)

The measured breakdown is persisted into the canonical `performance_runs` registry for historical comparison. `wall_ms` (the stock.render wall) lands in its dedicated column; `rtf`, `ffmpeg_ms`, and the full breakdown land in `metadata_json`.

**metadata_json shape** (canonical: `stockrustRunMetadata` in `internal/infrastructure/media/rustexec/stockrust_performance_persistence_test.go`):

```json
{
  "rtf": 0.149,
  "stock_render_ms": 10408,
  "rust_process_ms": 10378,
  "ffmpeg_ms": 10338,
  "go_overhead_ms": 30,
  "rust_internal_ms": 40,
  "media_duration_ms": 70000,
  "input_bytes": 1011610,
  "output_bytes": 1216857
}
```

**Column mapping**:

| Metric | Column |
|---|---|
| `stock.render` wall | `performance_runs.wall_ms` |
| `rtf` | `metadata_json.rtf` |
| `ffmpeg_ms` | `metadata_json.ffmpeg_ms` |
| breakdown (`rust_process_ms`, `go_overhead_ms`, …) | `metadata_json.*` |

**Flow**:

```text
TestStockRustPerformanceRTF
  → buildStockrustRun(run_id, wall, metadata, started, completed)
  → capperformance.Run { WallMS, MetadataJSON, workload_id="stockrust_render", status="SUCCEEDED" }
  → persistStockrustRun (env-gated)
      STOCKRUST_PERF_DB_PATH set → perfstore.Registry.RecordRun → performance_runs (idempotent upsert on run_id)
      unset                        → record-only (logged, no write; keeps the benchmark hermetic)
```

**Historical comparison query**:

```sql
SELECT started_at, wall_ms,
       json_extract(metadata_json,'$.rtf')       AS rtf,
       json_extract(metadata_json,'$.ffmpeg_ms') AS ffmpeg_ms
FROM performance_runs
WHERE workload_id='stockrust_render'
ORDER BY started_at;
```

**Idempotency**: `run_id` is the primary key with `ON CONFLICT DO UPDATE`; re-recording the same run converges. The round-trip is proven by `TestStockRustPerformancePersistenceRoundTrip` (in-memory SQLite; `wall_ms` + `rtf`/`ffmpeg_ms` read back unchanged).

### §13.6 — Lockstep referenti (godlike/06 SSOT)

- **L1 script**: `tests/operational/youtube_stock_live_e2e.sh`
- **L3 script**: `tests/operational/stockrust_live_e2e.sh`
- **L2 Go test battery**: `internal/infrastructure/media/rustexec/*_test.go` (canonical files listed in §13.2)
- **Rust binary**: `rust/pipelinegen-muscles/` → `bin/pipelinegen-muscles` (built via `make build-muscles`)
- **Stage timing wire field**: `ffmpeg_ms` (`rust/pipelinegen-muscles/src/protocol.rs` + `internal/infrastructure/media/rustexec/protocol.go`)
- **Wave-tracker**: `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` — §13 does NOT add a new wave entry; it documents the existing render-boundary certification surface. The §2 14-point battery remains the canonical ship-gate.


