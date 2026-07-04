# Voiceover Subsystem — Refactor & Decomposition Action Plan

**Date:** 2026-07-04
**Author:** PipelineGen Agent
**Owner:** architecture doc maintainer + 5 per-capability owners (see Wave-tracker)
**Scope:** `internal/api/assets/voiceover/`, `internal/application/voiceover/`, `internal/application/voiceover/jobs/`, `internal/infrastructure/audio/`, `scripts/bridges/tts_edge.py` + composition adapters.
**Status:** in_progress (Wave 32, `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04`)
**Audit-trail anchor:** `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04`
**Trigger:** Italian Phase 1 (read-only) audit snapshot pasted to the orchestrator on 2026-07-04.

> **Rule:** NO BRANCHES — direct-to-main per AGENTS.md Git-Lesson-2.
> Each per-item PR lands on `main` with auto-sufficient granularity; Co-authored-by trailer per Git-Lesson-3.

---

## TL;DR

6-file decomposition chain (3 split-per-stage + 1 TTS worker + 1 DRY pair + 1 parent state machine) + 1 typed-primitive extraction (`voiceover.TextHash`). 1 absolute band (P0) + 1 medium band (P1) + 1 quick-win (P1.1 typed-primitive). All targets carry explicit **kill candidates** (dormant / duplicated / fake-availability code physically deleted per godlike/07 no-fake-availability). Per godlike/06 SSOT each priority band owns one wave-tracker entry that hosts the per-item `linked_issues`. Bands execute in parallel during EXPAND phase; the absolute band takes priority on shared locks (the parent-aggregator TX seam + the runResilient 7-step ladder + the fanout Enqueuer port).

```
                                  PRIORITY (static, by complexity + risk)
                                  ─────────────────────────────────────────────────

  ┌──── BAND 1 ─────────── P0 ABSOLUTE (kill-candidate) ──────────────────────┐
  │  1. internal/infrastructure/audio/processor.go                             │
  │     [TTS BRIDGE: SPAWN-PER-CALL — kill=rebuild as persistent worker]      │
  │  2. internal/application/voiceover/stages.go (605 LoC)                     │
  │     [5 STAGES IN ONE FILE: synthesize/postprocess/destination/persist/finalize]│
  │  3. internal/application/voiceover/finalizer.go (538 LoC)                  │
  │     [11-STEP TERMINAL CHAIN: extract Step 6 outbox cleanup]                │
  │  4. internal/application/voiceover/parent_aggregator.go (469 LoC)          │
  │     [STATE-MACHINE + POLL + AGGREGATE in 1 Tick method]                    │
  │  5. DRY pair: usecase.go (523) ↔ process_voiceover_item.go (380)           │
  │     [BATCH/PER-ITEM EXECUTE share 5-stage pipeline — extract PipelineExec] │
  └──────────────────────────────────────────────────────────────────────────┘

  ┌──── BAND 2 ─────────── P1 STRUCTURAL ──────────────────────────────────────┐
  │  6. parent_aggregator state-machine column migration (P1.2)                │
  │     [forward-pointer already filed in generate_handler.go:267]            │
  └──────────────────────────────────────────────────────────────────────────┘

  ┌──── BAND 3 ─────────── P1.1 QUICK WIN (typed-primitive) ───────────────────┐
  │  7. voiceover.TextHash typed extraction (P1.1)                             │
  │     [BCP-47-style typed envelope for SHA256[:16] duplicate]                │
  │  8. voiceover.Language typed extraction (parallel to TextHash)              │
  │  9. voiceover.StyleGroup typed extraction (parallel to TextHash)           │
  └──────────────────────────────────────────────────────────────────────────┘
```

---

## 1. Subsystem Map (canonical surface)

Per the Phase 1 audit, the voiceover subsystem is **2 services + 1 use case + 1 fanout use case + 1 parent aggregator**, already well-divided per AGENTS.md Pattern 0/5. The decomposition targets the god-file splits (Band 1 #2/#3/#4) + the duplicated orchestration (Band 1 #5) + the persistent-TTS-worker gap (Band 1 #1).

```
┌──────────────────────────────┬──────────────────────────────────────────────────────────────────────────────────────────────┬────────────────────┐
│ Strato                       │ File canonici                                                                                                                │ LoC produttivi     │
│                              │                                                                                                                              │ (top, audit)       │
├──────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────┼────────────────────┤
│ API (transport)              │ internal/api/assets/voiceover/{handler.go, types.go, module.go}                                                              │ 232                │
│ Application (orchestrazione) │ stages.go 605 · usecase.go 523 · ports.go 503 · finalizer.go 538 · process_voiceover_item.go 380 · process.go 278 · executor.go 233│ totale ~3.800      │
│ Application/jobs             │ parent_aggregator.go 469 · fanout.go 289 · generate_handler.go 287 · generate_item_handler.go 227 · parent_state.go 210  │ totale ~1.480      │
│ Domain                       │ internal/domain/voiceover/{command.go, result.go, errors.go}                                                                 │ ~220               │
│ Infrastructure (TTS bridge)  │ internal/infrastructure/audio/processor.go (TTS bridge) + internal/app/adapters_voiceover_use_case.go (useCaseTTSAdapter)│ ~250               │
│ Subprocess esterno           │ scripts/bridges/tts_edge.py (Edge TTS via edge-tts)                                                                          │ python             │
└──────────────────────────────┴──────────────────────────────────────────────────────────────────────────────────────────────┴────────────────────┘
```

The voiceover → TTS wiring passes through the canonical port `TTSProvider` and the adapter `useCaseTTSAdapter` (`internal/app/adapters_voiceover_use_case.go`); compile-time lock: `var _ voiceover.TTSProvider = (*useCaseTTSAdapter)(nil)`. `*audioasset.Processor` is never imported from `internal/application/voiceover/`. **No circular deps detected.**

---

## 2. Kill-Candidate Matrix (per godlike/07 no-fake-availability)

| ID  | TARGET FILE                                  | KILL CANDIDATE                                                                                                                       |
|-----|----------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------|
|  1  | `internal/infrastructure/audio/processor.go`  | **Spawn-per-call** `exec.CommandContext("python3", ...)` → **persistent worker** (HTTP server or in-process gRPC). The Python `tts_edge.py` bridge becomes a long-lived subprocess with a typed request/response wire (mirrors `internal/application/images/slide_worker_process.go::ensureStarted` from PR-CHROME-PROVIDER-SPLIT, 2026-07-04). |
|  2  | `internal/application/voiceover/stages.go`   | **5-stage file** (605 LoC) → split per stage (one file per stage). No behavior change in EXPAND; legacy 5-stage Execute continues to call the extracted methods. |
|  3  | `internal/application/voiceover/finalizer.go`| **Step 6 outbox cleanup** inlined in the 11-step terminal chain → extract to `finalizer_cleanup_outbox.go` (single owner per godlike/06). |
|  4  | `internal/application/voiceover/jobs/parent_aggregator.go` | **State-machine + poll + aggregate in 1 Tick** → split: `state_machine.go` (Phase enum + transitions) + `parent_aggregator.go` (Tick thin orchestrator) + `parent_aggregator_state.go` migration to typed column (already forward-pointed in `generate_handler.go:267`). |
|  5  | `usecase.go` (523) ↔ `process_voiceover_item.go` (380) | **DRY pair** — extract canonical `PipelineExecutor` neutral struct that both wrappers invoke (Pattern 0 godlike/06 SSOT; ctx-different adapters if needed). |
|  6  | (P1.2) parent_aggregator state column         | `parent_aggregator_state` typed column migration (already filed as forward-pointer in `generate_handler.go:267`). |
|  7  | (P1.1) `voiceover.TextHash` typed             | **SHA256[:16] duplicate** in `planner.go:120` (uses `hashutil.SHA256String(text)[:16]`) + `fanout.go:285` (own impl `textHashSHA256(text)`) → single typed `voiceover.TextHash` (BCP-47-style) + `ComputeTextHash(text string) TextHash` helper in `internal/application/voiceover/texthash.go`. AGENTS.md forward-pointer "closed by Step 12 cleanup (extract to voiceover/texthash.go)" honored. |
|  8  | (P1.1) `voiceover.Language` typed             | **14+ raw-string `Language` sites** (BCP-47) in `command.go / task.go / result.go / types.go / validation.go / stages.go / planner.go / registry_adapter.go / result_dto.go / persistence/repository.go / fanout.go`. Single typed `voiceover.Language` envelope. (Optional; do as one PR with TextHash for half-migration risk mitigation per audit §2.4 watch-list #3.) |
|  9  | (P1.1) `voiceover.StyleGroup` typed           | **11+ raw-string `StyleGroup` sites** in `types.go:345 / metadata.go:80 / destination_resolver.go:150,165,182 / process_voiceover_item.go:272 / parent_state.go / result_dto.go`. Single typed `voiceover.StyleGroup` envelope. (Optional; do as one PR with TextHash.) |

---

## 3. Expected Split Topologies (godlike/06 one-owner-per-fact)

### Band 1 — P0 absolute

```
# 1. TTS persistent worker (kill spawn-per-call)
internal/infrastructure/audio/processor.go       → public surface: synthesize + ensureStarted + Stop + Health
internal/infrastructure/audio/worker_process.go  → subprocess lifecycle (ensureStarted + Stop)
internal/infrastructure/audio/worker_protocol.go → JSON/typed wire (writeRequest + readResponse + readRawResponse + workerResponse + mapToStruct)
internal/infrastructure/audio/worker_health.go   → health probes (healthCheck + Health + ActiveCooldownProfiles)
scripts/bridges/tts_edge.py                      → tts_edge_server.py (HTTP long-lived server: POST /synthesize)

# 2. stages.go (5 stages split)
internal/application/voiceover/stage_synthesize.go
internal/application/voiceover/stage_postprocess.go
internal/application/voiceover/stage_destination.go
internal/application/voiceover/stage_persist.go
internal/application/voiceover/stage_finalize.go        # already partly in finalizer.go

# 3. finalizer.go (11-step terminal chain)
internal/application/voiceover/finalizer.go             # 10 steps remain (or smaller)
internal/application/voiceover/finalizer_cleanup_outbox.go  # Step 6 outbox cleanup
internal/application/voiceover/finalizer_test_helpers.go    # test-only stub helpers (split from finalizer_test.go)

# 4. parent_aggregator.go (state machine + poll + aggregate)
internal/application/voiceover/jobs/parent_aggregator.go       # thin orchestrator: Tick + Recovery
internal/application/voiceover/jobs/parent_state_machine.go   # Phase enum + transitions + IsValid
internal/application/voiceover/jobs/parent_aggregator_state.go # typed column migration
internal/application/voiceover/jobs/parent_eligibility.go     # eligibility check extraction

# 5. DRY pair (usecase.go ↔ process_voiceover_item.go)
internal/application/voiceover/pipeline_executor.go            # canonical neutral struct
internal/application/voiceover/usecase.go                      # batch wrapper (thin)
internal/application/voiceover/process_voiceover_item.go       # per-item wrapper (thin)
```

### Band 3 — P1.1 typed-primitive quick win

```
internal/application/voiceover/texthash.go         # type TextHash string + ComputeTextHash(text) TextHash
internal/application/voiceover/language.go         # type Language string + ParseLanguage(s) (Language, error)
internal/application/voiceover/stylegroup.go       # type StyleGroup string + ParseStyleGroup(s) (StyleGroup, error)
```

(All 3 typed primitives in a single PR per audit §2.4 watch-list #3: "Primitive obsession — se vuoi fare typed, fallo in un PR ampio, NON puntuale (rischio di half-migration)".)

---

## 4. Priority Matrix (frequency × complexity)

Per the audit §3 matrix, all 5 P0 absolute items cluster in the upper-right (high frequency + high complexity); P1 items cluster in the medium band.

| File                                       | Freq. Modifica | Complessità/Fragilità | Quadrante       | Azione consigliata                          |
|--------------------------------------------|----------------|-----------------------|-----------------|---------------------------------------------|
| `stages.go` (605)                          | 🔴 Alta        | 🔴 Alta               | 🔥 ASSOLUTA     | Split per stage (5 file)                    |
| `finalizer.go` (538)                       | 🔴 Alta        | 🔴 Alta               | 🔥 ASSOLUTA     | Estrarre Step 6 cleanup outbox              |
| `parent_aggregator.go` (469)               | 🔴 Alta        | 🔴 Alta               | 🔥 ASSOLUTA     | Estrarre state-machine + poll               |
| `usecase.go` (523)                         | 🟡 Media       | 🔴 Alta               | 🔥 ASSOLUTA     | Merge con process_voiceover_item.go (DRY)   |
| `process_voiceover_item.go` (380)          | 🟡 Media       | 🔴 Alta               | 🔥 ASSOLUTA     | Vedi sopra (DRY pair)                       |
| `internal/infrastructure/audio/processor.go`| 🟡 Media       | 🔴 Alta               | 🔥 ASSOLUTA     | Worker Python persistente                    |
| `ports.go` (503)                           | 🟢 Bassa       | 🟡 Media              | Priorità Fluida | Aggiungere typed-error quando si cambia signatura |
| `filename.go` / `filename_builder.go`      | 🟡 Media       | 🟢 Bassa              | Priorità Fluida | Consolidare duplicazione {slug}/{lang}/{hash} (covered by P1.1 typed primitives) |
| `validation.go` / `command.go` / `types.go`| 🟢 Bassa       | 🟢 Bassa              | Bassa           | Lasciare                                     |
| `destination_resolver.go` (219)            | 🟡 Media       | 🟡 Media              | Priorità Media  | Solo se StyleGroup semantics cambia          |
| `orphan_sweeper.go` (443)                  | 🟢 Bassa       | 🟡 Media              | Priorità Media  | Solo se guasta a runtime                     |
| `process.go` (278)                         | 🟢 Bassa       | 🟡 Media              | Priorità Media  | Verificare se ancora raggiunto (PIVOT post per-item) |

---

## 5. Honest Limitation Declaration (godlike/07)

### 5.1 Carry-forward: "Cose che NON toccare"

Per the audit §5 + AGENTS.md godlike/07 honest-limitation, the following are **intentionally preserved** (not in this wave):

1. **Audit-pins / commenti di rimozione** in `usecase.go:478,511` + `stages.go:594-595` + `ports.go:158` — disciplinati da godlike/07 honest-limitation; toccare = rompere la chain di audit. Forward-port to AGENTS.md ## Recent cross-cutting closures if any per-file PR changes them.
2. **`useCaseTTSAdapter` panic-on-nil** (`internal/app/adapters_voiceover_use_case.go:82`) — fail-closed at startup is P0 #4 contract. Crasha l'app se mal configurato, comportamento voluto.
3. **Replace-Mode cleanup triplo** (`oldDriveFileID / oldLocalPath / oldCleanedPath` in `finalizer.go:88-95`) — intricato ma richiesto da P0.7 Step 10/12 per guarantire atomic swap-and-cleanup.
4. **Tick pattern in `parent_aggregator.go`** — polling intenzionale (vs signal-driven), Phase 1 wiring.

### 5.2 Static priority vs git-log frequency

This action plan ranks files by **static complexity + accumulated risk** per the audit snapshot. The forward-pointer entry `VO-DECOMPOSITION-HOTSPOT-CROSSREF` (deadline 2026-08-15, see `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04`) carries the post-wave cross-reference:

```bash
git log --since=90.days --pretty=format: --name-only internal/application/voiceover/ internal/application/voiceover/jobs/ internal/infrastructure/audio/ | sort | uniq -c | sort -rn | head -30
```

If this surfaces high-frequency hotspots NOT in the static 6-file list, the forward-pointer adds them to the canonical wave entry (append-only, NO inline rewrite per slim-schema ratchet).

### 5.3 Per-file deadline reasoning

| Band | Earliest deadline | Rationale |
|------|-------------------|-----------|
| P0 #1 (TTS persistent worker) | 2026-08-15 | Largest perf win but cross-language (Go + Python) — needs Python sidecar design + wire protocol definition |
| P0 #2-#5 (4 splits) | 2026-08-01 | Mechanical, mirrors PR-CHROME-PROVIDER-SPLIT cadence (1 week) |
| P1.2 (state machine) | 2026-08-01 | Already forward-pointed in `generate_handler.go:267` |
| P1.1 (TextHash + Language + StyleGroup typed) | 2026-07-25 | Single PR for half-migration risk mitigation (per audit §2.4 #3) |

### 5.4 Migration sequence (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT)

For each kill candidate (e.g. the spawn-per-call in `audio/processor.go`):

- **EXPAND** — canonical split lands; legacy call continues to function (defense-in-depth).
- **BACKFILL** — new callers migrate to the canonical split (each PR decrements legacy-call counter).
- **CUTOVER** — legacy spawn gated via typed sentinel; legacy-call counter reaches 0.
- **CONTRACT** — `architecture/deprecations.yaml` entry filed (cf. godlike/07 zero-baseline rule); legacy surface physically git-rm'd.

The audit's "elimination" instructions are interpreted as **CONTRACT-phase eliminations**, not EXPAND. The EXPAND phase must NOT silently break the production callers that still use legacy paths.

### 5.5 Pre-existing build issues carry forward unchanged

Same 5-item carry-forward as the prior CHANGELOG entries (per AGENTS.md "minimal blast radius" + "don't surprise downstream commits"):

```
- internal/application/assets/monitor/enqueue.go                (strings.ToLower undefined in isTransientEnqueueError)
- internal/application/assets/monitor/scheduler.go              (NewUnboundJobEnqueuer undefined)
- internal/application/assets/providers/stock/stockpipeline/run_upload.go  (file MISSING from disk; syntax error pre-existing)
- internal/app/module_media.go                                  (clips.Deps.MutationsDispatcher literal pre-existing)
- internal/application/images/routing                           (import cycle)
```

Each per-item PR lands in isolation on its own subtree and passes `gofmt + go vet + go build + go test` independently. Whole-project `go build ./...` is non-blocking per the CHANGELOG forward-pointer convention.

---

## 6. Execution Order (per the audit snapshot + per godlike/07 EXPAND discipline)

1. **P1.1 (TextHash + Language + StyleGroup typed)** — quick win, half-migration risk mitigation (single PR with all 3 typed envelopes). Unblocks the typed-error work in Band 1 #5 DRY pair. **Wave-band: P1.1 QUICK WIN, deadline 2026-07-25.**
2. **P0 #2 (split `stages.go`)** — 5 file split, mechanical, mirrors PR-CHROME-PROVIDER-SPLIT cadence. Lock acquisition on `process_voiceover_item.go` reads. **Wave-band: P0 ABSOLUTE, deadline 2026-08-01.**
3. **P0 #3 (extract Step 6 outbox cleanup from `finalizer.go`)** — 2-file split, single owner per godlike/06 SSOT. **Wave-band: P0 ABSOLUTE, deadline 2026-08-01.**
4. **P0 #4 (parent_aggregator state-machine + poll split)** — 4-file split; the `parent_aggregator_state` typed column migration is forward-pointer P1.2 (already filed in `generate_handler.go:267`). **Wave-band: P0 ABSOLUTE, deadline 2026-08-01.**
5. **P0 #5 (DRY pair: `usecase.go` ↔ `process_voiceover_item.go`)** — extract canonical `PipelineExecutor` neutral struct. **Wave-band: P0 ABSOLUTE, deadline 2026-08-15.**
6. **P0 #1 (TTS persistent worker)** — largest perf win, cross-language (Go subprocess + Python sidecar HTTP server). Lock acquisition: `voiceover.TTSProvider` port (compile-time assertion preserved). **Wave-band: P0 ABSOLUTE, deadline 2026-08-15.**
7. **P1.2 (parent_aggregator state column migration)** — runs in parallel with the P0 work; lands after P0 #4 split because the typed state-machine wrapper in the new `parent_state_machine.go` is the canonical surface. **Wave-band: P1 STRUCTURAL, deadline 2026-08-01.**

Steps 1+2+3+4 share the same compile-time lock on `stages.go` + `finalizer.go` (process_voiceover_item.go reads both). Step 5 reads the typed envelope from P1.1. Step 6 is independent (cross-language wire). Step 7 reads P0 #4's new `parent_state_machine.go`.

---

## 7. Per-Item Acceptance Criteria

Each per-item closure commit MUST pass targeted:

```bash
gofmt -d internal/<package>/<touched>/...                  # exit 0
go vet ./internal/<package>/<touched>/...                 # exit 0
go build ./internal/<package>/<touched>/...               # exit 0
go test -short -count=1 -timeout=60s ./internal/<package>/<touched>/...   # exit 0
rg <retired-symbol> --type go                              # returns 0 PRODUCTION-CODE hits (not comments)
```

Wave-level exit_gate (per godlike/07): all 6 P0 + 1 P1.2 + 1 P1.1 `linked_issues` flip to `status: shipped`. CI gate `Check 58` (forward-prevention, lands with P0 #1 — forbids `exec.CommandContext("python3", ...)` callers in `internal/infrastructure/audio/` outside the canonical persistent-worker surface).

---

## 8. Wave-tracker Entry (canonical anchor)

Live wave-tracker anchor (slim-schema per godlike/06): **`architecture/current.yaml#VO-DECOMPOSITION-2026-07-04`**.

Per-item SHAs land on the matching `linked_issues` slot. The wave flips to `status: done / exit_signal: true` once all 8 linked_issues reach `status: shipped` AND the `VO-DECOMPOSITION-HOTSPOT-CROSSREF` forward-pointer surfaces zero high-frequency hotspots not already in the plan (or, in the alternative outcome, adds them to the wave-tracker with extended deadlines).

---

## 9. Watch-list (per audit §6)

1. **Worker Python subprocess-per-call** è la singola fragilità di performance più grande — sopra ogni altra priority.
2. **DRY pair `usecase.go` ↔ `process_voiceover_item.go`** — verificare subito se l'orchestrazione diverge (potrebbe essere divergenza "by drift" non voluta).
3. **Primitive obsession su `Language / StyleGroup / TextHash`** — se vuoi fare typed, fallo in un PR ampio, NON puntuale (rischio di half-migration).
4. **Forward-pointer già tracciati**: estrazione `texthash.go`, migrazione `parent_aggregator_state` table, possible persistent TTS worker.

---

## 10. Author + sign-off

- **Author:** PipelineGen Agent
- **Date:** 2026-07-04
- **Owner:** architecture doc maintainer
- **Co-authored-by:** PipelineGen Agent `<agent@pipelinegen.local>` (per AGENTS.md Git-Lesson-3)
- **Commit (plan-only):** `chore(architecture): register VO-DECOMPOSITION-2026-07-04 wave-tracker entry + voiceover decomposition action plan` (direct-to-main per AGENTS.md Git-Lesson-2; Co-authored-by trailer; no --force)
- **Audit-pin canonical anchor:** `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04` is the live wave-tracker; this action plan is its narrative companion (per the slim-schema zero-legacy policy).

**Cross-reference (3-surface lockstep per godlike/06 SSOT):**
- `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04` (wave-tracker anchor + 8 net-new linked_issues + exit_gate)
- `CHANGELOG.md ## Unreleased → ### Added → VO-DECOMPOSITION-2026-07-04 entry` (closure audit-pin)
- `AGENTS.md ## Recent cross-cutting closures` (mirror entry)
