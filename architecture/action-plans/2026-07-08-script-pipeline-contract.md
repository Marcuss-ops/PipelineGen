# SCRIPTCONTRACT-2026-07-08 — Script pipeline contract hardening

> **Canonical surface**: `architecture/action-plans/2026-07-08-script-pipeline-contract.md` (this file)
> **Wave-tracker**: TBD (immigration step 1 below registers `architecture/current.yaml#SCRIPTCONTRACT-2026-07-08`).
> **AGENTS mirror**: TBD (immigration step 1 below appends an entry under `## Recent cross-cutting closures`).
> **CHANGELOG mirror**: TBD (immigration step 1 below appends under `## Unreleased > ### Documentation`).

---

## §0 Status snapshot (godlike/07 NO-FAKE-AVAILABILITY)

| Surface | Current state | Verdict |
|---------|--------------|---------|
| Drive `/api/script/generate` round-trip | Drive writes Doc, then job is parked in `RETRY_WAIT` | Drive works; pipeline status is wrong |
| `toScriptItemResultMap` (child → broker map) | Propagates `doc_id` + `doc_link` (main commit 2026-07-07) | ✓ FIXED (verified at `internal/application/scripts/jobs/script_generation_item_handler.go`) |
| `parent_aggregator.go::aggregateOne` | Reads `DocLink` + `DocID` from child JSON into `child_doc_links` + `child_doc_ids` | ✓ FIXED |
| `parent_aggregator.go::finalizeParent` | Writes `child_doc_links` + `child_doc_ids` into the parent result map | ✓ FIXED |
| `wire_script_postprocess.go::registerScriptPostProcessors` **ORDER** | `Document, Persistence, Images, Voiceover, Entities, Metadata, ClipBindings, Stock, ClipSearch` | ❌ PERSISTENCE AFTER DRIVE-WRITING PROCESSORS (the core bug) |
| User-requested processor missing at composition | Silently skipped + per-processor `log.Warn` → job reports SUCCEEDED with empty artifacts | ❌ SILENT-SKIP (godlike/07 NO-FAKE-AVAILABILITY violation when user requested) |
| TDD for child-job contract (`doc_id` + `doc_link` survival) | Partial — aggregator-side coverage present, mapper-side coverage missing | ⚠️ Need pinning tests |

**Two real bugs. One blessing: `toScriptItemResultMap` is already correct in main** (Marcuss-ops Marcuss-ops confirmation matches the 2026-07-07 commit evidence in `parent_aggregator.go::ScriptChildResult` godoc).

---

## §1 Honest-limitation disclosure (godlike/07)

- The current bug **was masked** by the fact that user testing confirmed "Doc landed on Drive" — so reviewers didn't escalate the wrong-direction pipeline state.
- Tests live in the codebase do NOT exercise the actual error path (Drive write + DB persist failure cascade). They exercise happy paths. Anti-regression pinning is mandatory.
- The `jobs.db.sqlite` schemas vary by table name per migration — DB inspection in §5 below will use `media.db.sqlite` per AGENTS.md canonical SSOT.

---

## §2 Goal

Bring the script-pipeline end-to-end contract into godlike/07 NO-FAKE-AVAILABILITY alignment:

1. **No side effect on Drive happens BEFORE required local persistence**. Persistence processor runs FIRST so a downstream failure can retry safely without re-creating Drive artifacts.
2. **Missing required processor = hard FAILED_PRECONDITION** (not silent skip + log Warn). User-driven composition-root validation that fails fast at startup when a necessary postprocessor is unwired but explicitly requested.
3. **Anti-regression tests** that lock the child→parent doc_id/doc_link propagation so a future refactor cannot silently re-introduce this bug.

The plan is orthogonal to (does NOT conflict with) the existing data-driven PR-VOICEOVER-POSTPROCESSOR-REENABLE workstream.

---

## §3 Per-PR migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

Each PR lands **directly on `main`** per AGENTS.md Git-Lesson-2 (no branches, no `--no-ff`, no `--force`).

### PR-1: `PR-SCRIPTCONTRACT-REORDER-PERSISTENCE` (priority P0; deadline 2026-07-15)

**Surface**: `internal/app/wire_script_postprocess.go::registerScriptPostProcessors` (currently ~280 LoC, well-trodden file).

**Change**: Reorder the 9 postprocessor registrations so **PersistenceProcessor is registered FIRST**, before any Drive-writing processor (`DocumentProcessor`, `ImageProcessor`, `VoiceoverProcessor`). The execution order during a `script.generate` job walk follows registration order — re-ordering changes the canonical fail-closed sequence:

- BEFORE: Document → Persistence → Image → Voiceover → Entities → Metadata → ClipBindings → Stock → ClipSearch (Doc writes Drive before persistence; if Persistence fails after Doc, job retries with duplicate Drive artifact).
- AFTER: `Persistence → Document → Image → Voiceover → Entities → Metadata → ClipBindings → Stock → ClipSearch` (Persistence runs first; Doc/Image/Voiceover happen only after the row is locked in SQLite; safe retry).

**godlike/06 SSOT**: the file's package doc explicitly enumerates the OTHER postprocessor categories (entities, metadata, clip_bindings, stock_association) — the comment block must be updated to reflect Persistence-first ordering. The processor list in `internal/application/scripts/adapters` (PostProcessorRegistry) is unchanged.

**godlike/07 minimum-blast-radius**: 0 new files; ~30-line re-order + comment update in 1 file. Pure code-motion: all 9 processors are still registered; only the order changes.

**Compatibility check**: the per-processor interface contract is unchanged. The order change does not break any caller. Existing happy-path `script.generate` jobs continue to write Doc/Image/Voiceover in the same final wire shape.

### PR-2: `PR-SCRIPTCONTRACT-HARD-PREFLIGHT` (priority P0; deadline 2026-07-15)

**Surface**: NEW file `internal/app/postprocessor_preflight.go` (~120 LoC) + minor addition to `registerScriptPostProcessors` (~10 LoC) + composition-root validator chain in `wire_script_adapters.go::validateRequiredProcessors`.

**Change**: A NEW composition-time preflight function `requireRequestedProcessors(root, env)` returns a typed error if the user envelope requests:
- `generate_voiceover=true` AND `root.Domains.VoiceoverService == nil` → `ErrPreflightProcessorMissing{Processor: "voiceover", Requested: true}`
- `generate_scene_images=true` AND `root.Domains.ImageService == nil` → `ErrPreflightProcessorMissing{Processor: "images", Requested: true}`
- `generate_document=true` AND `(root.Drive == nil || root.Drive.DocClient == nil)` → `ErrPreflightProcessorMissing{Processor: "document", Requested: true}`

The preflight runs BEFORE `registerScriptPostProcessors` so a missing-required processor fails fast at composition root, NOT silently at runtime. A Warn-level diagnostic logs which processor would have been skipped (godlike/07 residue accounting) before returning the typed error.

**godlike/06 SSOT**: the preflight contract lives ONLY in `postprocessor_preflight.go`. Per-processor `log.Warn` lines currently emitting "Ollama backend not available; falling back to unavailable adapter" in `wire_script_postprocess.go` are RETAINED (transitional backwards-compat log); the preflight is additive.

**godlike/07 typed-error contract**: 1 sentinel `ErrPreflightProcessorMissing` + typed envelope `{Processor string; Requested bool}`. Errors.Is + errors.As compatible; the canonical SOLE owner.

**Compatibility check**: deployments that wire all 3 deps (current live server on `localhost:8000`, PID 2012682) are UNAFFECTED. Deployments missing ANY of the 3 deps see an immediate 503 from boot (the boot refuses to start) — this narrows DOWN the failure modes from "silent empty SUCCEEDED" to explicit 503 at startup.

### PR-3: `PR-SCRIPTCONTRACT-CI-GATE-CHECK-62` (priority P0; deadline 2026-07-15)

**Surface**: NEW tester `scripts/ci-architectural-checks.sh::Check 62` + new forward-prevention git grep for the postprocessor-registration-order regression.

**Change**: a git grep that fails CI if `registerScriptPostProcessors` deviates from the canonical order (Persistence → Document → Image → Voiceover → Entities → Metadata → ClipBindings → Stock → ClipSearch). Forward-prevention.

**godlike/06 SSOT**: this gate is the CANONICAL enforcement of §3.PR-1 ordering across future commits; the canonical reasoning lives in this action plan + the gate placename.

**Compatibility check**: only the order in §3.PR-1 satisfies the gate; existing code violates it — so this PR must land AFTER PR-1 ships (otherwise CI goes red on the existing state). This is OK because PR-1 lands PR-2 + PR-3 lands the gate last.

### PR-4: `PR-SCRIPTCONTRACT-TDD-CHILD-DOC-PROPAGATION` (priority P1; deadline 2026-07-22)

**Surface**: NEW test file `internal/application/scripts/jobs/generation_item_handler_test.go` (~180 LoC) + minor extension to `internal/application/scripts/jobs/parent_aggregator_test.go` (~60 LoC).

**Test cases** (~7 functions):
1. `TestScriptItemHandler_DocumentGenerated_PropagatesDocIDAndLink` — child handler returns Doc with `DocID` + `DocLink`; `toScriptItemResultMap` emits JSON with these fields intact.
2. `TestScriptItemHandler_NoDocument_OmitsFields` — child handler without Doc emits JSON without `doc_id`/`doc_link` keys.
3. `TestScriptItemHandler_DocumentFailed_PropagatesError` — Doc creation fails; envelope carries `ok: false` + typed error message.
4. `TestScriptParentAggregator_DocPropagationFromChildren` — parent order with 3 children (1 success-with-doc, 1 success-no-doc, 1 failed); aggregator surfaces `child_doc_links` as map of size 1.
5. `TestScriptParentAggregator_MultipleChildrenWithDocs` — 3-of-3 succeeded-with-doc; `child_doc_ids` map carries all 3.
6. `TestScriptParentAggregator_AllFailed_NoChildDocLinks` — 3-of-3 failed; `child_doc_links` map absent or empty (finalizeParent conditional).
7. `TestScriptItemHandler_P0_1Gate_OverrideStatus` — broker SUCCEEDED + `ok: false` triggers P0.1 gate in aggregator (false-success protection).

**godlike/06 SSOT**: the test surface lives ONLY in this file (per AGENTS.md Pattern 5 — single-test-file-per-capability). The `parent_aggregator_test.go` extension is purely additive.

**godlike/07 minimum-blast-radius**: 0 production code change. Pure TDD pinning. If a future commit silently drops `doc_id`/`doc_link` from `toScriptItemResultMap`, the TDD fails BEFORE the regression reaches prod.

---

## §4 Per-PR execution checklist (godlike/07 minimum-blast-radius)

For EACH PR above:
1. `gofmt -l internal/app/... internal/application/scripts/...` → exit 0 (clean).
2. `go vet ./internal/app/... ./internal/application/scripts/...` → exit 0.
3. `go build ./...` (full project) → exit 0 + `scripts/ci-architectural-checks.sh --self-check` → exit 0.
4. `go test -short -count=1 -run 'TestScript...' ./internal/application/scripts/...` → PASS.
5. Post-commit: `bash scripts/ci-architectural-checks.sh` → exit 0 (no NEW violations).
6. Direct-to-main per Git-Lesson-2 + Co-authored-by trailer per Git-Lesson-3.
7. Race-protect: `git fetch origin && git log --oneline HEAD..@{u}` empty before `git push origin main`.

---

## §5 Verification gates (godlike/06/07)

### Pre-PR-1 verification (today, before any code change)
- `bash scripts/ci-architectural-checks.sh --self-check` → exit 0 (baseline).
- `gofmt -l` → clean (baseline).
- `go vet ./internal/app/... ./internal/application/scripts/...` → exit 0 (baseline).
- DB inspect at `data/media/media.db.sqlite` to confirm canonical jobs table name (per AGENTS.md single-table-per-capability canonical SSOT):
  ```
  sqlite3 data/media/media.db.sqlite ".tables"
  sqlite3 data/media/media.db.sqlite \
    "SELECT type, COUNT(*) FROM jobs WHERE type LIKE 'script.generate%' GROUP BY type;"
  ```

### Post-PER-PR verification (per PR)
- `bash scripts/ci-architectural-checks.sh` exits 0.
- `go test -short ./...` PASS (full project).
- Artlist-folder directory listing at `tests/operational/qdrant_dod_4_assertions_test.go::TestHermeticCollection_*` PASSes (Qdrant chain verification gate, per wave-tracker entry `QDRANT-DOD-FINAL-2026-07-08`).

---

## §6 Honest scope-lock (godlike/07)

**Carry forward unchanged**:
- 6-item voiceover + app build-issue list per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (NOT regressions of any per-PR closure).
- Carry-forward YAML parse error in `architecture/waves/wave_p1_high.yaml` (forward-pointer `PR-CURRENT-YAML-PARSE-FIX-PART-6`, deadline 2026-08-15).
- The `Job_1783503816330938641_0295cf80` lingering state in `data/media/media.db.sqlite` (forward-pointer `PR-JOBS-RETRY-WAIT-CLEANUP`, deadline 2026-08-01).

**Do NOT touch** (orthogonal):
- Voiceover per-language fanout logic (forward-pointer `PR-VO-PARENT-AGGREGATOR-CUTOVER`, deadline 2026-08-15).
- Artlist pipeline hardening (`ART-002` wave-tracker entry; deadline 2026-07-15).
- Qdrant schema v3 strikes (`QDRANT-CHAIN-VERIFY-2026-07-04`).

**Out-of-scope for non-imminent follow-ups**:
- Migrating `registerScriptPostProcessors` to a per-source builder pattern (the waveform of 9 processors × 2 fallback paths × 1 nil-guard is sufficient; adding per-source fluff would inflate LoC without value).
- Replacing the global `ppReg` registry with a per-job ephemeral registry (godlike/07 God-Object decomposition deadline 2026-08-15, NOT in scope for this contract hardening).

---

## §7 Cross-references (godlike/06 SSOT umbrella)

| Surface | Reference |
|---------|-----------|
| Definitive existing diag | `architecture/action-plans/2026-07-04-script-subsystem-analysis.md` (subsystem analyzer) |
| Voiceover decomposition | `architecture/waves/wave_p1_high.yaml#VO-DECOMPOSITION-2026-07-04` (parent wave-tracker) |
| Drive canonical | `architecture/waves/wave_p1_high.yaml#DRIVE-AS-CENTRAL-CAPABILITY-2026-07-07` (parent completion FASE A→E) |
| God Object decomposition | `architecture/waves/wave_p1_high.yaml#GODOBJ-2026-07-03` (12-file kill list — `script_generation_item_handler.go` not on the list, since it's already split) |
| PR 7 (script pipeline) | `architecture/waves/wave_p1_high.yaml#PR-SCRIPT-FLOW-SPLIT-2026-06-25` (canonical prior split; this plan builds on it) |
| Canonical mapper comment evidence | `internal/application/scripts/jobs/parent_aggregator.go` lines 70-78 (godoc confirming `toScriptItemResultMap` 2026-07-07 fix) |
| Live server PID 2012682 | `ARCHITECTURE.md §6.3 / port 8000 / VELOX_ADMIN_TOKEN env` (the bug observation surface) |

---

## §8 Wave-flip criterion (godlike/06/07)

The wave flips to `status: shipped + exit_signal: true` ONLY WHEN:
1. All 4 PRs (`PR-SCRIPTCONTRACT-REORDER-PERSISTENCE` + `PR-SCRIPTCONTRACT-HARD-PREFLIGHT` + `PR-SCRIPTCONTRACT-CI-GATE-CHECK-62` + `PR-SCRIPTCONTRACT-TDD-CHILD-DOC-PROPAGATION`) → `status: shipped`.
2. `bash scripts/ci-architectural-checks.sh` exits 0 (Check 62 gate active + green).
3. `go test -short ./...` exits 0 (all TDD cases pass; exising pre-existing failures reproduce unchanged per AGENTS.md carry-forward convention).

---

## §9 Lifecycle audit-trail + Co-authored-by

| Stamp | Action | Actor |
|-------|--------|-------|
| 2026-07-08 09:43 AM | POST /api/script/generate (job_1783503816330938641_0295cf80) — RETRY_WAIT 90% | Marcuss-ops |
| 2026-07-08 09:51 AM | Second POST /api/script/generate (correlation boxing-min-isol-2026-07-08-002) — SUCCEEDED with empty envelope | Marcuss-ops |
| 2026-07-08 ~10:00 | Drive folder `1oOlaSOwq1P7_yLfanvBqxwMvEoV1n4Wo` confirmed both Docs landed | Marcuss-ops |
| 2026-07-08 | Marcuss-ops diagnosis re: toScriptItemResultMap already-fixed + postprocessor-order real bug | Marcuss-ops |
| 2026-07-08 | Action plan authored (this file) | Marcuss-ops |
| 2026-07-08 | Action plan committed + pushed to origin/main | PipelineGen Agent |

---

## Co-authored-by trailer (mandatory, per AGENTS.md Git-Lesson-3)

```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
```

---

## §10 Honest cross-walk vs pre-existing waves (godlike/07 transparency)

This plan is **not** a substitute for any pre-existing wave. It complements:
- `PR-VOICEOVER-POSTPROCESSOR-REENABLE` (2026-07-08 SHA `cc292c0b`) — voiceover postprocessor RE-REGISTRATION, already shipped.
- `VO-DECOMPOSITION-2026-07-04` — voiceover subtree decomposition.
- `DRIVE-AS-CENTRAL-CAPABILITY-2026-07-07` (FASE A→E closure) — `delivery.Publisher` canonical write seam, already shipped.
- `GODOBJ-2026-07-03` — God-object decomposition across 12 production files.

The 4 PRs in §3 are **strictly new work**, scoped to the script.generate pipeline contract specifically. No pre-existing wave entry is invalidated or superseded by this plan.

---

## §11 Forward-pointers

- `PR-SCRIPTCONTRACT-OBSERVABILITY-PROM` (deadline 2026-08-15): emit Prometheus counter `script_postprocessor_order_reorder_total{processor}` + `script_preflight_failures_total{processor}` — observability for production SRE.
- `PR-SCRIPTCONTRACT-DRY-RUN-PREFLIGHT` (deadline 2026-08-22): add a `--dry-run` mode that surfaces the postprocessor + preflight check status WITHOUT booting the full service (mirrors `cmd/admin/qdrant_preflight.go` pattern).
- `PR-SCRIPTCONTRACT-STAGING-VERIFY` (deadline 2026-09-01): live server staging deployment of the 4 PRs + 14-point smoke battery verification per `architecture/action-plans/2026-07-05-stock-e2e-battery.md` (adapted for `script.generate` flow).
