# God-Object Decomposition — Action Plan

**Date:** 2026-07-03
**Author:** PipelineGen Agent
**Owner:** architecture doc maintainer + 15 per-capability owners (see Wave-tracker)
**Scope:** Static-priority decomposition of 12 files classified by complexity + accumulated risk per the Italian audit snapshot pasted to the orchestrator on 2026-07-03.
**Status:** in_progress (Wave 31, `architecture/current.yaml#GODOBJ-2026-07-03`)
**Audit-trail anchor:** `architecture/current.yaml#GODOBJ-2026-07-03`

---

## TL;DR

12 file targets classified into **3 priority bands** + **1 dangerous-but-small** band. Each target carries an explicit **kill candidate** (the legacy / dormant / silent-success code that gets PHYSICALLY DELETED — not merely refactored). Per godlike/06 SSOT each priority band owns one wave-tracker entry that hosts the per-file `linked_issues`. Bands execute in parallel during EXPAND phase; the absolute band takes priority on shared locks (finalizer TX / jobs.svc ledger / extraction canonical-loop switch).

```
                                  PRIORITY (static, by complexity + risk)
                                  ─────────────────────────────────────────────────

  ┌──── BAND 1 ─────────── PRIORITY ABSOLUTE (P0) ────────────────────────────┐
  │  1. extraction_service.go     2. monitor/scheduler.go                     │
  │     [YOUTUBE EXTRACTION]         [CHANNEL MONITOR + OUTBOX DRAINER]        │
  │  3. images/generation_service 4. scripts/jobs/generation_job.go           │
  │     [6 CONCEPTS IN ONE FILE]     [DECODE+FS+MANIFEST+BROKER IN HANDLER]    │
  │  5. jobs/finalizer/job_finalizer.go     6. jobs/service.go                │
  │     [TERMINAL SUCCEEDED OWNER]            [REFLECTION ANY + SQL LEAKAGE]   │
  └──────────────────────────────────────────────────────────────────────────┘

  ┌──── BAND 2 ─────────── PRIORITY HIGH MECHANICAL ───────────────────────────┐
  │  7. app/composition.go        8. app/assets_register_adapters.go          │
  │     [BUNDLE DRAWER]              [ADAPTER DRAWER 7 CAPABILITIES MIXED]    │
  │  9. images/chrome_provider.go                                              │
  │     [PROCESS + LIFECYCLE + COOLDOWN + DEAD MULTI-PROFILE LOOP]            │
  └──────────────────────────────────────────────────────────────────────────┘

  ┌──── BAND 3 ─────────── CUT NOT SPLIT (godlike/07 no-fake-availability) ────┐
  │  10. ai/semantic/semantic.go 11. api/script/handler_legacy_adapters.go    │
  │      [COMPAT STUB NO-OP]         [3 LEGACY ROUTES WITH REMOVAL DATES]      │
  │  12. cmd/admin/qdrant_maintenance.go                                       │
  │      [5 MODES IN ONE FILE]                                                 │
  └──────────────────────────────────────────────────────────────────────────┘

  ┌──── BAND 4 ─────────── SMALL BUT DANGEROUS ────────────────────────────────┐
  │  13. application/books/job_handler.go  14. app/worker_registry.go         │
  │      [DRIVE ERR LOGGED, SUCCESS RETURNED]   [EVENTS SILENTLY DROPPED]      │
  │  15. app/module_media.go: WireAssets as 2nd composition root              │
  └──────────────────────────────────────────────────────────────────────────┘
```

---

## 1. Kill-Candidate Matrix (canonical per-file)

Per godlike/07 "no-fake-availability": the legacy / dormant code must be **physically deleted** from production. `rg <symbol>` returns zero hits **OUTSIDE narration comments** post-PR.

```
┌─────┬──────────────────────────────────────────┬────────────────────────────────────┐
│ ID  │  TARGET FILE                             │  KILL CANDIDATE                    │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  1  │ extraction_service.go                    │ Legacy inline segment loop:        │
│     │ (internal/application/youtube/usecase/)  │ processSeg != nil bypasses         │
│     │                                          │ ProcessYouTubeSegmentUseCase.       │
│     │                                          │ Single ownership of the canonical  │
│     │                                          │ per-segment path.                  │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  2  │ monitor/scheduler.go                     │ drainInterval = 5s hardcoded —     │
│     │ (internal/application/assets/monitor/)   │ move to MonitorRuntimePolicy;      │
│     │                                          │ outbox-loop is split to            │
│     │                                          │ outbox_drainer.go (single           │
│     │                                          │ owner per outbox lifecycle).       │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  3  │ images/generation_service.go             │ (a) fallback legacy imageGen.      │
│     │ (internal/application/images/)           │     Generate; (b) ingest diretto   │
│     │                                          │     dal job (saltando ingest port);│
│     │                                          │ (c) parametri account/project     │
│     │                                          │     ignorati ma esposti.          │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  4  │ scripts/jobs/generation_job.go           │ (a) broker-wire coupling inline    │
│     │ (internal/application/scripts/jobs/)     │     nel handler; (b) single+batch  │
│     │                                          │     combined in one decision      │
│     │                                          │     tree; (c) filesystem ops in   │
│     │                                          │     handler (not pipeline).        │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  5  │ jobs/finalizer/job_finalizer.go          │ Mechanical split, NO behavior      │
│     │ (internal/application/jobs/finalizer/)   │ change. The two duplicated        │
│     │                                          │ row.status == "SUCCEEDED" checks  │
│     │                                          │ collapse into one central         │
│     │                                          │ completion_idempotency.go.        │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  6  │ jobs/service.go                          │ (a) RegisterHandler(jobType, any) │
│     │ (internal/application/jobs/)             │     — reflection; (b) stats leak  │
│     │                                          │     *sqljobs.JobStats from app    │
│     │                                          │     layer (SQLite leakage).        │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  7  │ app/composition.go                       │ (a) DriveBundle keeps raw         │
│     │ (internal/app/)                          │     *drive.Service + *drive.       │
│     │                                          │     Uploader pre-cutover;         │
│     │                                          │ (b) noop bundle types.            │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  8  │ assets_register_adapters.go              │ Mixed-capabilities drawer:        │
│     │ (internal/app/)                          │ dispatcher/outbox + ExistingClip   │
│     │                                          │ mapper + metadata writer +        │
│     │                                          │ publisher adapter coexisting.      │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  9  │ images/chrome_provider.go                │ Multi-profile cooldown loop is    │
│     │ (internal/application/images/)           │ DORMANT (numProfiles ignored;     │
│     │                                          │ --profiles 1 hardcoded).          │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  10  │ ai/semantic/semantic.go                  │ Compatibility stub no-op:         │
│     │ (internal/infrastructure/ai/semantic/)   │ Write() does not write a file;    │
│     │                                          │ GeneratePayload produces sync    │
│     │                                          │ values. Replaces log + exposes   │
│     │                                          │ "DISABLED / NOT_CONFIGURED"      │
│     │                                          │ explicitly.                       │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  11  │ handler_legacy_adapters.go               │ 3 endpoints with removal dates:   │
│     │ (internal/api/script/)                   │ curate (2026-09-30) +             │
│     │                                          │ generate-from-clips (2026-12-31)  │
│     │                                          │ + generate-with-images (2026-12-  │
│     │                                          │ 31). Do NOT split — legacy file  │
│     │                                          │ stays until ticket-count = 0.    │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  12  │ qdrant_maintenance.go                    │ 5 modes (audit/repair/delete/     │
│     │ (cmd/admin/)                             │ rebuild/json|human). Split to    │
│     │                                          │ per-mode admin commands OR       │
│     │                                          │ sub-package internal/application/ │
│     │                                          │ qdrant for real services.         │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  13  │ books/job_handler.go                     │ Silent-success bug: Drive upload  │
│     │ (internal/application/books/)            │ errors logged + "success":true    │
│     │                                          │ returned. Decide explicit-fail   │
│     │                                          │ OR delivery_status=PUBLISH_FAILED │
│     │                                          │ (per godlike/07 typed contract).  │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  14  │ worker_registry.go                       │ adaptHandler silently drops        │
│     │ (internal/app/)                          │ events + Progress errors + Is     │
│     │                                          │ Cancelled read errors. Surface    │
│     │                                          │ or capability-flag.               │
├─────┼──────────────────────────────────────────┼────────────────────────────────────┤
│  15  │ module_media.go: WireAssets              │ Second composition root. Today    │
│     │ (internal/app/)                          │ builds search / deletion /        │
│     │                                          │ enrichment / bulk upload /        │
│     │                                          │ upload use case / adapters.       │
│     │                                          │ Must fold into composition.go.    │
└─────┴──────────────────────────────────────────┴────────────────────────────────────┘
```

---

## 2. Expected Split Topologies

Per godlike/06 one-owner-per-fact: each split introduces **typed envelopes** at the seam so cross-file drift is a build failure, not a runtime panic (AGENTS.md Pattern 0).

```
# ── Band 1 (absolute) ─────────────────────────────────────────────────────

# 1. extraction_service.go (god service split)
extraction_service.go       orchestrazione sottile (5-step)
extraction_request.go       validazione e normalizzazione
extraction_fanout.go        concorrenza segmenti via pkg/concurrent
extraction_result.go        aggregazione risultati
extraction_destination.go   percorso locale e destinazione

# 2. monitor/scheduler.go (ChannelMonitor + outbox + scheduler split)
monitor.go             struct ChannelMonitor + costruzione
scheduler_loop.go      ticker + ClaimDue
channel_runner.go      semaforo + timeout + panic recovery
outbox_drainer.go      claim + dispatch outbox (sotto-port ownership)
channel_validation.go  Keywords / SemanticKeywords
check_outcome.go       MarkChecked + backoff

# 3. images/generation_service.go (6 concetti)
generation_usecase.go     richiesta → immagine locale
generation_job.go         adapter del job
generation_request.go     prompt / stile / dimensioni
provider_dispatch.go      SOLO GenerationProviderRegistry
image_manifest.go         manifest dell'artifact
sync_generation.go        adapter sincrono batch-of-one

# 4. scripts/jobs/generation_job.go
generation_handler.go       decode + dispatch
generation_outcome.go       success / partial / all-failed policy (ClassifyGenerationOutcome pure function)
generation_result_mapper.go typed result → broker map
generation_manifest.go      artifact manifest
generation_registration.go  registrazione job

# 5. jobs/finalizer/job_finalizer.go (mechanical)
job_finalizer.go          orchestratore transazionale
request_validator.go      validazione strutturale
lease_fence.go            SELECT e controllo ownership
completion_idempotency.go fingerprint e conflict
artifact_writer.go        ciclo artifact + outbox
job_completion_writer.go  result + events + SUCCEEDED

# 6. jobs/service.go (remove reflection, remove SQL leak)
service.go              façade minima
handler_registration.go SOLO HandlerFunc, niente reflection
enqueue_service.go      enqueue + idempotenza
job_queries.go          get/list/events
job_commands.go         cancel/retry

# ── Band 2 (mechanical) ─────────────────────────────────────────────────────

# 7. app/composition.go
bundle_drive.go          DriveBundle + 4-port surface per DRIVE-005
bundle_repo.go           repository bundles
bundle_search.go        search backends
bundle_process.go       process bundles
bundle_jobs.go          job bundles
compose_root.go          NewComposition root

# 8. assets_register_adapters.go
youtube_dispatcher_adapter.go
youtube_enrichment_adapter.go
youtube_fetch_adapter.go
youtube_drive_legacy_adapter.go   ← DIES post cutover
youtube_metadata_adapter.go
youtube_publisher_adapter.go
youtube_asset_mapper.go

# 9. chrome_provider.go
chrome_provider.go       Generate + interfaccia pubblica
slide_worker_process.go  start/stop/restart
slide_worker_protocol.go request/response JSON
slide_worker_health.go   health

# ── Band 3 (cut not split) ──────────────────────────────────────────────────

# 10. semantic.go → stub esplicito
semantic_stub.go           stub con log "DISABLED / NOT_CONFIGURED"
semantic_types.go          DTO types minimi
metadata_builder.go        helper (only if used)

# 11. handler_legacy_adapters.go → nessuna divisione futura
legacy_deprecation.go      Unico file estratto (header, metriche, removal dates)
# (route + DTO legacy + alias resolver + test legacy + wiring deleted as one shot at ticket=0)

# 12. qdrant_maintenance.go → per-mode dispatch
qdrant_maintenance.go          dispatch modalità (HELP + per-mode dispatcher)
qdrant_maintenance_audit.go
qdrant_maintenance_repair.go
qdrant_maintenance_delete.go
qdrant_maintenance_output.go

# ── Band 4 (small-but-dangerous) ─────────────────────────────────────────────

# 13. books/job_handler.go → fail-closed semantic decision (NEVER split, only semantic fix)
# 14. worker_registry.go → typed-error surface (no split)
# 15. module_media.go → fold WireAssets into composition.go
wire_media_search.go
wire_media_upload.go
wire_media_enrichment.go
wire_media_maintenance.go
wire_media_handlers.go
```

---

## 3. Execution order (per the Italian audit snapshot)

Per the audit's "ordine consigliato":

1. **extraction_service.go** — first because it carries BOTH canonical (`ProcessYouTubeSegmentUseCase`) AND legacy inline loop on the same inputs; killing the legacy removes the largest silent-drift surface.
2. **semantic stub** — explicit "DISABLED" markers kill the silent-fake-success corner.
3. **images/generation_service** — separates ingest port ownership from job body (today two potential owners of the same image).
4. **scripts/jobs/generation_job** — moves filesystem + manifest out of the handler (most touch surface).
5. **monitor/scheduler** — separates outbox drainer (single owner of outbox lifecycle).
6. **finalizer/job_finalizer** — mechanical, NO behavior change. The cleanest "training exercise" for the split topology.
7. **jobs/service reflection removal** — replace RegisterHandler(any) with HandlerFunc. Removes SQL leakage by introducing JobStats DTO.
8. **composition + adapter drawer** — mechanical band 2.
9. **legacy routes** — extract `legacy_deprecation.go` carrier; DELETE route + DTO + alias resolver + test + wiring at ticket=0 per the scheduled dates.
10. **admin command cleanup** — per-mode dispatch for qdrant_maintenance.

The order matters because steps 1+2+3 share lock acquisition with steps 5+7 (extraction canonical-loop switch, jobs.svc ledger, monitor outbox single-owner). Steps 4+6+8+9+10 are independent and execute in parallel.

---

## 4. Honest Limitation Declaration (godlike/07)

### 4.1 Static priority vs git-log frequency

This action plan ranks files by **static complexity + accumulated risk**, not by git-log modification frequency. The audit snapshot acknowledges: "Non sto inventando la frequenza di modifica: per la classifica hotspot definitiva bisognerà incrociarla con `git log`."

The forward-pointer entry `PR-GODOBJ-HOTSPOT-CROSSREF` (deadline 2026-08-01, see `architecture/current.yaml#GODOBJ-2026-07-03`) carries the post-wave cross-reference:

```
git log --since=90.days --pretty=format: --name-only | sort | uniq -c | sort -rn | head -30
```

If this surfaces high-frequency hotspots NOT in the static 12-file list, the forward-pointer adds them to the canonical wave entry (append-only, NO inline rewrite per slim-schema ratchet).

### 4.2 Per-file deadline reasoning

| Band | Earliest deadline | Rationale |
|------|-------------------|-----------|
| P0 absolute | 2026-08-15 (~6 weeks) | Locks on extraction canonical-loop + monitor outbox + finalizer TX |
| P0 mechanical | 2026-08-22 (~7 weeks) | Bundle separation is touching-wide; needs follow-on composition wiring |
| Cut not split | 2026-07-15 / 2026-08-22 / 2026-12-31 | semantic stub is small + safe (immediate); legacy routes have user-fixed dates (Sept + Dec 2026); admin command is mid-window |
| Small-but-dangerous | 2026-07-25 | "log+return success" and "silent-drop events" are quick fixes |

### 4.3 Out-of-scope files explicitly preserved

Per the audit's "File che non dividerei adesso" section, these files are **NOT** in this wave:

- `internal/infrastructure/drive/publisher.go` — single responsibility (already cohesive).
- `internal/infrastructure/drive/uploader_put.go` — single conflict-aware algorithm.
- `internal/application/youtube/jobs/job_handler.go` — already at a focused 6-step shape.
- `internal/application/books/job_handler.go` — semantic fix only, not a split (see Band 4 #13).

### 4.4 Migration sequence (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT)

For each kill candidate (e.g. the legacy inline segment loop in extraction_service.go):

- **EXPAND** — canonical split lands; legacy loop continues to function (defense-in-depth).
- **BACKFILL** — new callers migrate to the canonical split (each PR decrements legacy-call counter).
- **CUTOVER** — legacy loop gated via typed sentinel; legacy-call counter reaches 0.
- **CONTRACT** — `architecture/deprecations.yaml` entry filed (cf. godlike/07 zero-baseline rule); legacy surface physically git-rm'd.

The audit's "elimination" instructions (e.g. "eliminare il loop legacy") are interpreted as **CONTRACT-phase eliminations**, not EXPAND. The EXPAND phase must NOT silently break the production callers that still use legacy paths; this is the canonical godlike/07 discipline and is what makes the audit's 7 forbidden compatibility techniques unacceptable.

### 4.5 Pre-existing build issues carry forward unchanged

Same five items as the prior CHANGELOG entries (per AGENTS.md "minimal blast radius" + "don't surprise downstream commits"):

```
- monitor/enqueue.go                      (`strings.ToLower` undefined)
- monitor/scheduler.go                    (`NewUnboundJobEnqueuer` undefined)
- internal/application/assets/providers/stock/stockpipeline/run_upload.go  (syntax error)
- internal/app/module_media.go            (`clips.Deps.MutationsDispatcher` literal)
- internal/application/images/routing     (import cycle)
```

Each split commit lands in isolation on its own subtree and passes `gofmt + go vet + go build + go test` independently. Whole-project `go build ./...` is non-blocking per the CHANGELOG forward-pointer convention.

---

## 5. Wave-tracker Entry (canonical anchor)

Live wave-tracker anchor (slim-schema per godlike/06): **`architecture/current.yaml#GODOBJ-2026-07-03`**.

Per-file SHAs land on the matching `linked_issues` slot. The wave flips to `status: done / exit_signal: true` once all 15 linked_issues reach `status: shipped` AND the `PR-GODOBJ-HOTSPOT-CROSSREF` forward-pointer surfaces zero high-frequency hotspots not already in the plan (or, in the alternative outcome, adds them to the wave-tracker with extended deadlines).

---

## 6. Author + sign-off

- **Author:** PipelineGen Agent
- **Date:** 2026-07-03
- **Owner:** architecture doc maintainer
- **Co-authored-by:** PipelineGen Agent `<agent@pipelinegen.local>` (per AGENTS.md Git-Lesson-3)
- **Commit (plan-only):** `chore(architecture): register GODOBJ-2026-07-03 wave-tracker entry + decomposition action plan` (direct-to-main per AGENTS.md Git-Lesson-2; Co-authored-by trailer; no --force)
- **Audit-pin canonical anchor:** `architecture/current.yaml#GODOBJ-2026-07-03` is the live wave-tracker; this action plan is its narrative companion (per the slim-schema zero-legacy policy).
