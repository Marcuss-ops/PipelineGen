# chaos_day_2026_08_01_report.md (PR-QDRANT-CHAOS-DAY-2026-08-01 — Gate 10 Failure mode Qdrant spento)

> === SRE-ONLY EXECUTION ===
> This runbook is registered with `wave-tracker status: scheduled` per
> godlike/07 NO-FAKE-AVAILABILITY + the canonical SRE-coordination
> requirement documented in `architecture/action-plans/2026-07-04-qdrant-preflight-execution.md`
> section 3 (Test 9: "retry recovery ... SRE-only, gated to a maintenance
> window" + "Forward-pointer: PR-QDRANT-CHAOS-DAY-2026-08-01").
>
> The agent that registered this slot DOES NOT execute the 7-step chaos
> sequence — the agent cannot coordinate with the on-call SRE, cannot
> touch the docker container running on production infrastructure, and
> cannot observe the retry-loop in `internal/application/jobs/outbox/pool.go`
> over a real 60s + 30s window. Status flips to `shipped` ONLY when the
> on-call SRE executes the sequence + records verdicts in §Results below
> + commits the report to origin/main.

## 0. Coordination requirements

- **On-call SRE** MUST be paged before the maintenance window starts.
- **Maintenance window** MUST be in a low-traffic slot (Sunday 02:00–04:00 UTC preferred
  per the canonical PipelineGen change-management calendar; cross-check with
  the on-call rotation on `#pipelinegen-sre`).
- **Outage tolerance**: workers stay RUNNING throughout the window; only `pipelinegen-qdrant`
  is stopped (per step 1). The retry-pending state validates the outbox-decoupling contract
  (per `tests/operational/voiceover_c4_outbox_decoupling_smoke.sh` precedent — independent
  workers + retry-loop architecture surface).
- **Observability prerequisite**: Pipedream/Promtail OR `journalctl -u pipelinegen`
  MUST be reachable for the SRE to observe `outbox_events.status=retry_pending`
  + `last_error` surfacing within 60s.
- **Rollback path**: if step 7 (recovery verification) fails after step 6
  (`docker start pipelinegen-qdrant`) and 30s have elapsed, the SRE MUST
  page the secondary on-call + NOT close the maintenance window until
  the retry-pending queue is fully drained or manually marked dead_lettered.

## 1. 7-step chaos sequence (verbatim from user spec, 2026-07-08)

### Step 1 — `docker stop pipelinegen-qdrant`

**Operator command**:
```bash
docker stop pipelinegen-qdrant
```

**Expected**:
- Container stops within 5s.
- `curl -s http://localhost:6333/health` returns connection-refused.

**Verification**: TBD on execution day.

> godlike/06 SSOT: `architecture/qdrant/README.md` + `docker-compose.yml`
> are the canonical owners of the `pipelinegen-qdrant` container spec.

### Step 2 — enqueue a new seed asset via `/api/script/generate-from-clips`

**Operator command**:
```bash
TOKEN="$(grep VELOX_ADMIN_TOKEN /etc/pipelinegen/.env | cut -d= -f2)"
curl -X POST http://localhost:8080/api/script/generate-from-clips \
     -H "Authorization: Bearer ${TOKEN}" \
     -H "Content-Type: application/json" \
     -d '{"clips":[{"video_id":"<seed-youtube-id>","start_sec":0,"end_sec":120}]}'
```

**Expected**: HTTP 200 + async job_id enqueued. The Qdrant-pending indexer
on the worker side will attempt to dispatch the event but fail (Step 1's
container is down).

**Verification**: TBD on execution day.

> godlike/06 SSOT: the canonical `script.generate_from_clips` job type
> is registered at `internal/application/jobs/registry_extraction.go::registry["script.generate_from_clips"]`
> (re-exported from `internal/domain/job/job.go::TypeScriptGenerate`). The
> `/api/script/generate-from-clips` endpoint is the sole canonical SOLE
> owner of the wire shape.

### Step 3 — wait 60s + verify `outbox_events.status='retry_pending'`

**SRE observation window**: 60 seconds wall-clock after Step 2 submission.

**Operator SQL probe**:
```bash
sqlite3 data/media/media.db.sqlite \
  "SELECT id, event_type, aggregate_id, status, last_error, attempt_count
   FROM outbox_events
   WHERE event_type='asset.index.requested'
     AND status IN ('pending','retry_pending','processing')
   ORDER BY created_at DESC, id DESC LIMIT 5;"
```

**Expected**:
- At least 1 row with `status='retry_pending'` (NOT 'dead_letter' and NOT 'pending').
- `attempt_count >= 1` (proves the worker tried + failed + pooled for retry).

**Verification**: TBD on execution day.

> godlike/06 SSOT: `internal/application/jobs/outbox/pool.go` is the
> canonical retry-loop surface; `retry_pending` is a typed status value
> registered in `internal/application/jobs/outbox/events.go` (or the
> canonical status enum location per the codebase's typed-state discipline).

### Step 4 — verify `last_error` contains Qdrant connection error

**Operator SQL probe**:
```bash
sqlite3 data/media/media.db.sqlite \
  "SELECT status, last_error FROM outbox_events
   WHERE event_type='asset.index.requested' AND status='retry_pending'
   ORDER BY created_at DESC, id DESC LIMIT 1;"
```

**Expected**:
- `last_error` contains a substring matching the canonical Qdrant connection-died error class
  (e.g. `connection refused`, `failed to connect to Qdrant`, or the project's
  typed sentinel `ErrQdrantUnavailable`). The exact substring depends on
  the bounded-retry mechanism in the production outbox pool; the
  `pkg/retry.IsTransient` probe family is the canonical classifier.

**Verification**: TBD on execution day.

> godlike/07 NO-FAKE-AVAILABILITY: a silent-success here (last_error empty
> + status='retry_pending') means the outbox handled a transient Qdrant
> error WITHOUT observability — the operator-side report MUST flag this
> as a regression of the typed-error contract in `pkg/retry.WrapTransient`.

### Step 5 — verify `media_assets.index_state` stays NOT-INDEXED

**Operator SQL probe**:
```bash
sqlite3 data/media/media.db.sqlite \
  "SELECT id, lifecycle_state, index_state FROM media_assets
   WHERE source='script'
   ORDER BY created_at DESC, id DESC LIMIT 5;"
```

**Expected**:
- The newly-seeded asset's `index_state` stays at `DISCOVERED` (the pre-`INDEXED`
  state per `internal/domain/asset/index_state.go` enum) — NOT `INDEXED`.
- `lifecycle_state` stays `ACTIVE` (the row was inserted active but index_state
  hasn't transitioned since Qdrant is down).

**Verification**: TBD on execution day.

> godlike/06 SSOT: `internal/domain/asset/index_state.go` is the canonical
> SOLE owner of the index_state enum (DISCOVERED / INDEXING_PENDING /
> INDEXING / INDEXED / FAILED). The asset is in DISCOVERED until the
> IndexingHandler (downstream of Step 3's event) successfully Qdrant-upserts.

### Step 6 — `docker start pipelinegen-qdrant`

**Operator command**:
```bash
docker start pipelinegen-qdrant
```

**Expected**:
- Container starts within 10s.
- `curl -s http://localhost:6333/health` returns `{"status":"ok",...}`.

**Verification**: TBD on execution day.

### Step 7 — wait 30s + verify recovery

**SRE observation window**: 30 seconds wall-clock after Step 6.

**Operator probes**:
```bash
# 1. outbox completed
sqlite3 data/media/media.db.sqlite \
  "SELECT status, attempt_count FROM outbox_events
   WHERE event_type='asset.index.requested'
     AND aggregate_id='<seed-asset-id>'
   ORDER BY created_at DESC, id DESC LIMIT 1;"
# Expected: status='completed', attempt_count > 1 (proves retry-loop fired)

# 2. media_assets.index_state=INDEXED
sqlite3 data/media/media.db.sqlite \
  "SELECT index_state FROM media_assets WHERE id='<seed-asset-id>';"
# Expected: INDEXED

# 3. Qdrant scroll finds asset_id
curl -X POST http://localhost:6333/collections/media_assets_current/points/scroll \
     -H 'Content-Type: application/json' \
     -d '{"filter":{"must":[{"key":"asset_id","match":{"value":"<seed-asset-id>"}}]}}'
# Expected: len(result.points)==1, single point with asset_id=<seed>+lifecycle_state=ACTIVE
```

**Verification**: TBD on execution day.

> godlike/06 SSOT: the recovery path is an exemplar of `cmd/admin/qdrant_preflight_stubs.go::testMediaAssetsIndexStateIndexed`
> + `testQdrantScrollFindsAsset` (per the 4 implementation landed on `origin/main` 2026-07-08
> at SHA 10f1bf47 + the audit-pin `10f1bf47` documented in
> `architecture/waves/wave_p1_high.yaml#PR-QDRANT-PREFLIGHT-TEST-{5,6,7,8}-IMPL`).

## 2. Results template (to be filled by the on-call SRE on the execution day)

> The SRE opens this section on the maintenance window start + ticks each
> verdict box as the sequence progresses. Status flips to `shipped` only when
> ALL 7 boxes are marked PASS, the report is committed to origin/main, and the
> wave-tracker entry in `architecture/waves/wave_p1_high.yaml#PR-QDRANT-CHAOS-DAY-2026-08-01`
> is flipped from `scheduled` to `shipped` with the chaos_date cross-reference.

### Maintenance window metadata
- **Window start (UTC)**:     YYYY-MM-DD HH:MM:SS
- **Window end (UTC)**:       YYYY-MM-DD HH:MM:SS
- **On-call SRE**:            <name> (<handle>)
- **Cross-coordinator**:      <secondary on-call, optional>
- **PipelineGen version**:    <commit SHA on origin/main>

### Step verdicts

| Step | Label | Verdict (PASS / FAIL / PARTIAL) | Evidence (probes + log excerpts) |
|------|-------|-------------------------------|----------------------------------|
| 1    | docker stop             | ___ | <paste `docker ps` output + `curl :6333/health`> |
| 2    | enqueue seed asset      | ___ | <paste HTTP response + job_id> |
| 3    | outbox retry_pending    | ___ | <paste sqlite3 SELECT output> |
| 4    | last_error surfaces Qdrant error | ___ | <paste last_error substring> |
| 5    | media_assets.index_state stays NOT-INDEXED | ___ | <paste index_state column value> |
| 6    | docker start            | ___ | <paste `docker ps` + `curl :6333/health`> |
| 7    | recovery: outbox completed + INDEXED + Qdrant scroll | ___ | <paste 3 probe outputs> |

### Aggregate verdict

- **Overall PASS / FAIL / PARTIAL**: ___
- **Cross-cutting side-effects observed**: ___
- **Forward-pointers filed**: ___ (list any `PR-QDRANT-*` rear-pointer tickets)
- **Closure commit (recorded by post-execution bookkeeping)**: <SHA on origin/main>

## 3. Cross-references (per godlike/06 SSOT)

- **`architecture/action-plans/2026-07-04-qdrant-preflight-execution.md#section-3`** — canonical Test 9 spec + SRE-only forward-pointer language
- **`architecture/waves/wave_p1_high.yaml#PR-QDRANT-CHAOS-DAY-2026-08-01`** — wave-tracker slot (status: `scheduled`, status flips to `shipped` ON execution day after §Results filled)
- **`architecture/waves/wave_p1_high.yaml#PR-QDRANT-DOD-STOCK-PRODUCER`** — sister wave (the 3 hermetic TDD scenarios verify the canonical-finalizer emit path that Test 9's retry-loop surfaces end-to-end live)
- **`tests/operational/voiceover_c4_outbox_decoupling_smoke.sh`** — closest precedent (outbox-decoupling invariant verified via Qdrant-off smoke)
- **`tests/operational/semantic_qdrant_off_smoke.sh`** — sister precedent (semantic-location outbox decoupling)
- **`cmd/admin/qdrant_preflight_stubs.go::testMediaAssetsIndexStateIndexed`** — the canonical media_assets.index_state assertion surface (verified hermetic via `httptest`, ready for live replay)
- **`internal/application/jobs/outbox/pool.go`** — canonical retry-loop surface (where the `retry_pending` status is set on transient Qdrant errors)

## 4. Honest-limitation disclosure (per godlike/07)

- **This runbook is registered with `status: scheduled`, NOT `status: shipped`**,
  per godlike/07 NO-FAKE-AVAILABILITY. The agent cannot fabricate a `shipped`
  state without the on-call SRE coordinate + execute + record results.
- **The 7-step sequence is NOT executable in a single bash session**: it
  spans at least 90 seconds wall-clock + requires Docker daemon access +
  requires SRE coordination + requires a real YouTube seed asset. It
  CANNOT be agent-executed.
- **The wave-tracker slot will flip to `status: shipped` ONLY when**:
  (a) the on-call SRE executes all 7 steps + records PASS verdicts in §Results,
  (b) the SRE-committed report lands on origin/main + cross-references the
  wave-tracker entry with ship_date + ship_sha + chaos_date, and
  (c) the post-execution bookkeeping commits the `status: scheduled` →
  `status: shipped` flip on the wave-tracker slot.
- **Forward-pointer leaks**: any FAIL verdicts in §Results become new
  `PR-QDRANT-CHAOS-DAY-FAIL-NNN` rear-pointer tickets. The slim-schema
  ratchet on `architecture/waves/wave_p1_high.yaml#PR-QDRANT-CHAOS-DAY-2026-08-01`
  + `linked_issues` enforces this discipline.
