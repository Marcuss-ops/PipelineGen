# QDRANT Pre-flight Operator Checklist — Execution Plan (2026-07-04)

> **Authoritative doc surface**: this file (canonical narrative + per-test commands + forward-pointers) + `architecture/current.yaml#QDRANT-PREFLIGHT-EXECUTION-2026-07-04` (wave-tracker anchor) + `architecture/action-plans/2026-07-04-qdrant-verification-chain.md#section-4` (source 11-test definition) + `AGENTS.md §Recent cross-cutting closures` (agent-facing fast-reference mirror) + `CHANGELOG.md ## Unreleased → ### Documentation` (closure meta-entry). Per godlike/06 SSOT 3-surface lockstep discipline.

## 1. Context

The QDRANT-CHAIN-VERIFY-2026-07-04 audit (companion action plan: `2026-07-04-qdrant-verification-chain.md §4`) defined 11 SQL/curl sanity probes to verify the Qdrant end-to-end indexing chain (media_assets → outbox_events `asset.index.requested` → `IndexingHandler` → `clipindexer.IndexClip` → QdrantRuntime `media_assets_current` → `SearchAdapter/HybridSearch`). The pre-diagnosis on 2026-07-04 (before this plan landed) found the live stack fully DOWN:

| Surface | Expected | Actual |
|---|---|---|
| HTTP API (`VELOX_PORT`, default `:8080`) | listening | **DOWN** — connection refused |
| Qdrant gRPC+HTTP (`:6333`) | listening | **DOWN** — connection refused |
| `data/media/media.db.sqlite` | exists with seeded assets | **MISSING** — `data/media/` directory does not exist |
| PipelineGen process (`cmd/server`) | running | **NOT RUNNING** — `pgrep pipelinegen` returns 0 hits |
| Qdrant test container (`docker-compose.test-qdrant.yml`, port 16333→6333) | optional bring-up | **NOT RUNNING** — `docker ps` shows only `velox-worker` container |
| Qdrant production container (`docker-compose.yml`) | optional bring-up | **NOT RUNNING** — same `docker ps` excludes it |

The user (2026-07-04) selected recovery path **A** from the 4-option proposal: bring up the ephemeral test-qdrant stack via `docker-compose.test-qdrant.yml` + run Test 1 (health) + Test 2 (schema v3) only; Tests 3-11 remain BLOCKED pending the full stack + seeded data + an active outbox consumer. This file is the formal action plan for that selection.

## 2. Scope

### Phase 1A — Test-Qdrant Stack + Tests 1+2 (deadline 2026-07-15, ~30s)

Bring up the project's standard ephemeral Qdrant test container (the canonical test-stack convention from `docker-compose.test-qdrant.yml`):

```bash
docker-compose -f docker-compose.test-qdrant.yml up -d
# Qdrant now listening on:
#   HTTP 0.0.0.0:16333 → 6333 (REST API + health)
#   gRPC 0.0.0.0:16334 → 6334 (cluster peers)
# No DB / no worker / no PipelineGen server — test-only Qdrant surface.
```

#### Test 1 — Qdrant health + collections list
- **Command**:
  ```bash
  curl -s http://localhost:16333/health | python3 -m json.tool
  curl -s http://localhost:16333/collections | python3 -m json.tool
  ```
- **Expected**: `{"status":"ok","title":"qdrant","version":"<semver>"}` + `{"result":{"collections":[]}}` for a fresh stack (the test stack has no seeded collections — a separate `PUT /collections/media_assets_current` is the Test-2 prerequisite below).
- **Status on test stack**: VIABLE (no seed required, no DB required).

#### Test 2 — Schema v3 (5 named vectors)
- **Command**:
  ```bash
  # Prerequisite — create the collection with the canonical v3 schema
  curl -X PUT http://localhost:16333/collections/media_assets_current \
       -H 'Content-Type: application/json' \
       -d @architecture/qdrant/v3-schema.json
  # Then verify
  curl -s http://localhost:16333/collections/media_assets_current \
       | python3 -c 'import sys,json; d=json.load(sys.stdin)["result"]["config"]["params"]; print("vectors:", sorted(d["vectors"].keys())); assert "text" in d["vectors"] and d["vectors"]["text"]["size"]==768; assert "transcript" in d["vectors"] and d["vectors"]["transcript"]["size"]==768; assert "visual" in d["vectors"] and d["vectors"]["visual"]["size"]==512; assert "audio" in d["vectors"] and d["vectors"]["audio"]["size"]==512; assert "bm25_text" in d.get("sparse_vectors",{})'
  ```
- **Expected**: 4 named dense vectors (text:768, transcript:768, visual:512, audio:512) + 1 named sparse vector (`bm25_text`). The v3 schema file lives at `architecture/qdrant/v3-schema.json` and is the canonical cross-team contract from the Configservice team.
- **Status on test stack**: VIABLE (collection create + verify, no DB / no worker / no seed required).

### Phase 1B — Full Stack + Tests 3-11 (deadline 2026-08-01, ~5-10 minutes)

Bring up the production stack (covers Qdrant with persistent volume + PipelineGen server + worker) + seed via a real asset URL via the canonical `/api/script/...` endpoints (or via `cmd/admin/seed_channels.go` for YouTube category). Tests 3-11 then run against the seeded + indexed data.

```bash
# Bring up the production stack (per docker-compose.yml)
docker-compose up -d                                       # Qdrant + server + worker + named volumes
sleep 30                                                   # wait for worker boot + first heartbeat

# Seed via a canonical script (or via the HTTP API)
# Option A: real YouTube URL via the script generation endpoint
curl -X POST http://localhost:8080/api/script/generate-from-clips \
     -H 'Authorization: Bearer ${VELOX_ADMIN_TOKEN}' \
     -H 'Content-Type: application/json' \
     -d '{"clips":[{"video_id":"<seed-youtube-id>","start_sec":0,"end_sec":120}]}'
# Option B: artlist asset via /api/artlist/run
curl -X POST http://localhost:8080/api/artlist/run \
     -H 'Authorization: Bearer ${VELOX_ADMIN_TOKEN}' \
     -d '{"query":"<seed-query>"}'

# Wait for the indexing chain to complete (the worker logs will surface progress)
sleep 60
```

#### Tests 3-11 — BLOCKED on Phase 1B
- **Test 3** (`outbox_events asset.index.requested` row creation): requires worker + source asset
- **Test 4** (same row → `status=completed`): same prerequisites
- **Test 5** (`media_assets.index_state=INDEXED + lifecycle_state=ACTIVE`): same prerequisites
- **Test 6** (Qdrant scroll per asset_id): same prerequisites
- **Test 7** (`POST /internal/v1/media/search` hybrid mode → score > 0.5): same prerequisites
- **Test 8** (supersede gate via 2 events with different `source_version`): same prerequisites + a real superseding write
- **Test 9** (Qdrant down → retry recovery → resolved on Qdrant restart): SRE-only operation (see §3)
- **Test 10** (delete tombstone: media_assets lifecycle_state=DELETED + Qdrant point removed): destructive test on a live asset (see §3)
- **Test 11** (voiceover 5-stage pipeline → outbox → Qdrant populated): long-tail (see §3)

Each Test 3-11 has its own `PR-QDRANT-PREFLIGHT-TEST-N-...` linked_issue in `architecture/current.yaml#QDRANT-PREFLIGHT-EXECUTION-2026-07-04` per the godlike/06 slim-schema ratchet.

## 3. Honest scope-lock (per godlike/07)

Some Tests are out-of-band for a single CLI invocation:

- **Test 9** (`Retry recovery`): requires manual `docker stop pipelinegen-qdrant` + observation of the retry loop in `internal/application/jobs/outbox/pool.go` + `docker start pipelinegen-qdrant` + verify the same event row moves `pending → processing → completed`. **SRE-only**, gated to a maintenance window. Forward-pointer: `PR-QDRANT-CHAOS-DAY-2026-08-01` (scheduled chaos day after Phase 1B is complete).
- **Test 10** (`Delete tombstone`): destructive on a live asset. Requires (a) a sandbox asset (separate seed), (b) `DELETE /api/assets/<id>` API hit, (c) verify `media_assets.lifecycle_state=DELETED` + Qdrant point removed via scroll. Forward-pointer: `PR-QDRANT-DELETE-SANDBOX` (a separate sandbox-mode seed that the operator is willing to delete).
- **Test 11** (`Voiceover → Qdrant`): long-tail because the voiceover 5-stage pipeline takes ~3 minutes per voiceover (TTS + audio post-processing + Drive upload + atomic commit + outbox emit + Qdrant upsert). Forward-pointer: piggy-back on the next voiceover smoke run via `pkg/veloxclient.SubmitAsync` (`script_type=voiceover`).

These are not blockers for Phase 1A (which is fully viable). They are forward-pointers for the wave-tracker exit gate per godlike/07 §"no fake availability".

## 4. Per-file execution checklist (per AGENTS.md Pattern 6)

Each item is a separate per-PR commit on `main` (auto-sufficient granularity per AGENTS.md Git-Lesson-2; per-file rollup is the canonical wave pattern). Co-authored-by trailer preserved (Git-Lesson-3). Race-handled push (Git-Lesson-4/5).

1. **`PR-QDRANT-TEST-QDRANT-STARTUP`** — `docker-compose.test-qdrant.yml up -d` + verify `:16333/health` returns OK. (deadline 2026-07-15)
2. **`PR-QDRANT-PREFLIGHT-TEST-1`** — execute Test 1 commands + record expected output. (deadline 2026-07-15)
3. **`PR-QDRANT-PREFLIGHT-TEST-2`** — `PUT /collections/media_assets_current` + verify schema. (deadline 2026-07-15)
4. **`PR-QDRANT-PREFLIGHT-SCHEMA-V3-CREATE`** — ship the canonical `architecture/qdrant/v3-schema.json` if not present (forward-pointer to Configservice). (deadline 2026-07-25)
5. **`PR-QDRANT-FULL-STACK-BRINGUP`** — `docker-compose up -d` + verify all 3 services up + healthchecks green. (deadline 2026-07-25)
6. **`PR-QDRANT-PREFLIGHT-DATA-SEED`** — ship a seed CLI or seed script that ingests 1 YouTube URL + 1 artlist query + 1 voiceover → indexes all 3 assets. (deadline 2026-07-25)
7. **`PR-QDRANT-PREFLIGHT-TEST-3`** .. **`PR-QDRANT-PREFLIGHT-TEST-11`** — execute Tests 3-11 in sequence, each as its own per-PR commit per AGENTS.md Pattern 6. (deadlines 2026-07-25 .. 2026-08-01)
8. **`PR-QDRANT-PREFLIGHT-CLOSURE`** — flip `architecture/current.yaml#QDRANT-PREFLIGHT-EXECUTION-2026-07-04` → `status: done / exit_signal: true` once all 11 tests have green reports. (deadline 2026-08-01)

## 5. Forward pointers (godlike/06 SSOT)

- **`PR-QDRANT-CHAOS-DAY-2026-08-01`** — schedule Test 9 retry recovery + future chaos tests in a scheduled maintenance window.
- **`PR-QDRANT-DELETE-SANDBOX`** — sandbox-mode seed for Test 10 destructive delete tombstone verification.
- **`PR-QDRANT-PREFLIGHT-CLOSURE`** — final wave-tracker flip + CHANGELOG + AGENTS.md 3-surface lockstep when all 11 tests are green.
- **`PR-QDRANT-FULL-STACK-AUTOMATED`** — future hardener: ship a single `cmd/admin/preflight` binary that runs all 11 tests as TDD + exits 0 only when ALL pass (the canonical CI integration of this checklist).

## 6. Operator runbook (for the on-call operator)

If you are the on-call operator reading this on a fresh shift:

1. `cd /home/pierone/Pyt/PipelineGen && git pull origin main` — get the latest plan.
2. Pick your band (Phase 1A vs 1B) and start the corresponding stack.
3. Run the band-scoped Tests (1+2 vs 3-11).
4. For Test 9 / 10 / 11 — coordinate with SRE on outage window / sandbox / voiceover smoke run (forward-pointers §3).
5. Once all 11 tests are green, file `PR-QDRANT-PREFLIGHT-CLOSURE` per §4 step 8.

If you find a Test assertion failure: file a targeted PR against the wave-tracker slot (`PR-QDRANT-PREFLIGHT-TEST-N` flipped to `status: blocked` + ship_via the diagnosis + ship_date the day of investigation). Do NOT file a flat "PREFLIGHT-FAILED" — the per-test slot is the canonical contract surface per godlike/06 SSOT one-owner-per-fact.

## 7. Cross-references (per godlike/06 SSOT 3-surface lockstep)

- `architecture/current.yaml#QDRANT-PREFLIGHT-EXECUTION-2026-07-04` — wave-tracker anchor (slim-schema: id, status, exit_signal, owner_capability, deadline, description, linked_issues)
- `architecture/action-plans/2026-07-04-qdrant-verification-chain.md#section-4` — source 11-test definition (the audit that produced this checklist)
- `architecture/action-plans/2026-07-04-qdrant-verification-chain.md#section-6` — external pre-flight checklist (11 SQL/curl sanity probes — the SAME 11 tests but pre-FASE-6)
- `AGENTS.md ### Recent cross-cutting closures` — agent-facing fast-reference mirror entry
- `CHANGELOG.md ## Unreleased → ### Documentation` — closure meta-entry
- `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` — umbrella wave-tracker entry that spawned this Pre-flight execution plan

## 8. Honest-limitation disclosure

- **Phase 1A** is *fully viable today* (the test-qdrant stack + schema v3 create + health + schema verify are scriptable from a single shell script in ~30s).
- **Phase 1B** requires the *full stack bring-up + seed* which is operator-driven (NOT a single CLI invocation — the seed requires a real YouTube/artlist URL or a sandbox asset). The 9 Tests (3-11) are *individually* scriptable from `data/media/media.db.sqlite` + a running Qdrant, but the SEED step is the blocker.
- **Test 9, 10, 11** are SRE-only / destructive / long-tail respectively (see §3). They cannot be executed in a single bash session — they require coordination with chaos windows / sandbox setup / voiceover smoke runs.
- The *closure* of this wave (PR-QDRANT-PREFLIGHT-CLOSURE flipping the wave-tracker to `done / exit_signal: true`) is gated on ALL 11 tests being green — not just the Phase 1A tests. The slim-schema ratchet (`status:` can flip from `pending` → `in_progress` → `done` but cannot skip intermediates) enforces the discipline.

Per AGENTS.md + godlike/07: this plan deliberately does NOT fake any green check. Operators running this checklist should expect some tests to surface real issues — that's the value of the operator pre-flight (catch problems before declaring "Qdrant end-to-end funziona" in production).
