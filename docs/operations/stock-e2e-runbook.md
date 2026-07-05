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

Per godlike/07 NO-FAKE-AVAILABILITY (canonical at `docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md`): a closure that marks "stock works" without a probe that **actually runs the surface** is invalid. The 9 phases A → I below are the canonical diagnostic surface; each phase is the receipt; each FAIL signal maps to a canonical `PR-STOCK-*` forward-pointer.

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

# 4. Run phase H aggregator (verifier-only audit-pin pattern; default mode).
bash tests/operational/stock_e2e_full_battery.sh

# 5. If H reports 14/14 PASS and you want to flip the parent wave + exit_signal:
WRAPPER_BOOKKEEPING=1 bash tests/operational/stock_e2e_full_battery.sh
# (default mode without WRAPPER_BOOKKEEPING=1 prints the canonical 6-step recipe
#  for the operator to copy-paste — per godlike/07 minimum-blast-radius discipline)

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
| `SUCCEEDED` but `media_assets` empty | `PR-STOCK-FINALIZE-PROJECTION` | `internal/application/assets/providers/stock/stockpipeline/finalizer_gates.go` | Finalizer/projection asset incomplete |
| `media_assets` OK but search empty | `PR-STOCK-OUTBOX-QDRANT-INDEX` | `internal/application/jobs/outbox/delivery.go` | Outbox delivery / Qdrant indexing best-effort silent-fail |
| `outbox_events.status='failed'` (transient retry-able) | `PR-STOCK-OUTBOX-RETRY-EXHAUSTED` | `internal/infrastructure/database/sqlite/outboxevents/repository.go::MarkFailed` (line 252) | Pre-condition side: `attempt_count >= max_attempts` check + `RequeueExpiredLeases` scheduling |
| `outbox_events.status='dead_lettered'` | `PR-STOCK-OUTBOX-DEAD-LETTERED` | `internal/infrastructure/database/sqlite/outboxevents/repository.go` (line 252 + 321) | Canonical owner writes `SET status = 'dead_letter'` — investigate retry loop exhaustion |
| `outbox_events.last_error` non-empty | `PR-STOCK-OUTBOX-LAST-ERROR` | `internal/infrastructure/database/sqlite/outboxevents/repository.go` (lines 252, 266, 321, 367) | `last_error` write seam — inspect the surface error to identify the upstream cause |
| `download` 404 (no `/api/media/stock/clips/<id>/download` route) | `PR-STOCK-DOWNLOAD-ROUTE-REGISTRATION` | `internal/api/assets/stock/handler.go` (lines 39-40 = existing canonical r.POST calls) | Add the missing `r.POST(/api/media/stock/clips/:id/download, h.DownloadClip)` route + handler delegate to `StockRenderWriteStep` |
| `download` zero-size (real route, surface broken) | `PR-STOCK-DOWNLOAD-ZERO-SIZE` | `internal/application/assets/providers/stock/stockpipeline/step_compose_chunks.go::StockComposeChunksStep.Run` | Canonical stitch + write seam |
| `download` ffprobe failed (no video stream OR duration 0) | `PR-STOCK-CUTTER` | `internal/infrastructure/media/render/cutter.go` | ffmpeg cutter / mp4 muxer diagnostic |

**Decision dispatch**:

1. Run Phase H aggregator: `bash tests/operational/stock_e2e_full_battery.sh`
2. If exit 0 → all 14 points PASS → Phase H labelled PASS → wave-flip ready (operator invokes `WRAPPER_BOOKKEEPING=1` per the recipe in §1 step 5)
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
4. **Phase H aggregator**: `bash tests/operational/stock_e2e_full_battery.sh` → expect 14/14 PASS verdict.
5. **Conditional wave-flip** (only if Phase H reports 14/14 PASS): `WRAPPER_BOOKKEEPING=1 bash tests/operational/stock_e2e_full_battery.sh` → flips `architecture/current.yaml#STOCK-E2E-BATTERY-2026-07-05` to `status: shipped + exit_signal: true`.
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
