# STOCK-CLIPS-CLEANUP-2026-07-08 — Action Plan (Wave Closure)

**Date**: 2026-07-08
**Status**: WAVE-CLOSURE (docs lockstep only)
**Owner capability**: `internal/api/assets/stock/` + `internal/api/assets/clips/`
**Wave-tracker anchor**: `architecture/current.yaml#STOCK-CLIPS-CLEANUP-2026-07-08` (NUOVO)
  (physical band file: `architecture/waves/wave_p3_low_and_audit.yaml` per codebase convention; slot status documented in §10)

---

## §0 — Status snapshot (rg output of callers per sub-PR)

Per godlike/07 NO-FAKE-AVAILABILITY: each sub-PR's verdict is backed by reproducible `rg` output below. Verdicts reflect the **canonical ground truth** at the time of this commit (2026-07-08).

### §0.1 — `/run-deprecation` (POST /api/stock-pipeline/run)

**Verdict: FALSE PREMISE — endpoint has external callers, CANNOT be deprecated.**

| Caller | Path | Role |
|--------|------|------|
| `tests/operational/stock_e2e_route_aliveness_smoke.sh` | STK-E2E-A | Live route-aliveness probe |
| `tests/operational/stock_e2e_direct_url_smoke.sh` | STK-E2E-C | `direct_urls` path exercise |
| `internal/api/assets/stock/handler.go:270` | `req := stockpipeline.StockRunPayload{Async: true}` | Production handler |
| `architecture/action-plans/2026-07-05-stock-e2e-battery.md` | canonical narrative | Operator reference |
| `docs/operations/stock-e2e-runbook.md` | operator runbook | Operator reference |

**Outcome**: The /run endpoint stays ACTIVE. The DRY-validation sub-PR (below) consolidated the internal duplicate without changing the public surface.

### §0.2 — `DRY-validation` (handler.go:159-218 + 97-157)

**Verdict: SHIPPED — `applyStockDefaults` helper extracted, byte-equivalent validation preserved.**

- 22-line textually-identical validation block duplicated between `RunStockPipeline` (handler.go:159-218) and `SearchAndRun` (handler.go:97-157) — REMOVED
- Single canonical `applyStockDefaults(input stockValidationInput) (defaults stockValidationDefaults, err error)` helper lives at `internal/api/assets/stock/handler.go` (private to the stockapi package)
- 4 wire error literals preserved byte-for-byte (`"search_queries, ..."` / `"queries, ..."` etc.)
- 5 NEW TDD tests in `internal/api/assets/stock/validation_test.go`

**Canonical ship SHA**: `118b1aa2d` (commit `refactor(stock): PR-STOCK-DRY-VALIDATION — extract canonical applyStockDefaults helper`)

### §0.3 — `placeholder-strip` (silent-success fields in response)

**Verdict: SHIPPED — `drive` + `indexed` + `location` placeholders REMOVED from BOTH `/run` + `/search-and-run` responses.**

| Placeholder | Pre-PR shape | Post-PR shape |
|-------------|-------------|---------------|
| `drive` | `{path: "", folder_id: "", file_id: "", link: ""}` | KEY REMOVED |
| `indexed` | `false` | KEY REMOVED |
| `location` | `{category: "", subject: "", provider: "", style: ""}` | KEY REMOVED |
| `status_url` | unconditional | conditional on `job_id != ""` (sync mode omits key) |

- 2 dead helper functions `stockDrivePlaceholder()` + `stockLocationPlaceholder()` REMOVED
- Minimal contract is now `{job_id, message, status_url}` per godlike/07 NO-FAKE-AVAILABILITY
- 4 NEW TDD tests pin the contract for both endpoints in both async + sync modes

**Canonical ship SHA**: `3cc6146fc` (commit `refactor(stock): PR-STOCK-NO-PLACEHOLDERS — remove silent-success placeholder fields`)

### §0.4 — `dead-field-retire` (seconds_per_segment in StockRunPayload)

**Verdict: FALSE PREMISE — field is ACTIVELY CONSUMED for clip fan-out, CANNOT be retired.**

| Consumer | File | Role |
|----------|------|------|
| `StockRunPayload.SecondsPerSegment int` (json tag) | `payloads.go` | Wire-shape DTO |
| `StockCommand.SecondsPerSegment int` | `types_run.go` + `command.go::FromRunPayload` | Domain command |
| `if seconds_per_segment > 0 { split }` | `internal/api/assets/register/` | **Clip fan-out consumer** (specialized fan-out when > 0) |
| `step_plan_clips.go` + `step_plan_clips_test.go` | pipeline | Active expansion logic with hermetic TDD |

**Outcome**: The 3-architectural-layer threading (DTO → command → pipeline step) is the canonical godlike/06 SSOT pattern, NOT dead-code residue. The `zap.Int("seconds_per_segment", ...)` log in handler.go is **request-level observability for an active branch**, not a substitute for the field.

### §0.5 — `type-alias-strip` (`type enqueueRequest = jobservice.EnqueueRequest`)

**Verdict: FALSE PREMISE — type alias has 3 active callers in 2 files, CANNOT be retired.**

| File | Line | Code |
|------|------|------|
| `internal/api/assets/clips/handler.go` | 246 | `type enqueueRequest = jobservice.EnqueueRequest` (declaration) |
| `internal/api/assets/clips/handler_index.go` | 42 | `&enqueueRequest{...}` (caller #1) |
| `internal/api/assets/clips/handler_index.go` | 108 | `&enqueueRequest{...}` (caller #2) |
| `internal/api/assets/clips/handler_download.go` | 71 | `&enqueueRequest{...}` (caller #3) |

**Outcome**: The alias is the canonical godlike/06 SSOT threading pattern (narrow local name for a shared upstream type). Retiring it would force 3 caller-site renames for **zero functional benefit** (the type alias adds no runtime overhead).

---

## §1 — Hot-spot prioritization matrix

Priority is rank-ordered by **godlike/07 NO-FAKE-AVAILABILITY risk × accumulated debt**:

| Rank | Sub-PR | Verdict | Complexity | Risk if shipped | Status |
|------|--------|---------|------------|-----------------|--------|
| 1 | `placeholder-strip` | SHIPPED | Low | **HIGH** (silent-success class — operators reading `drive: {path: ""}` saw a fake-populated shape) | `status: shipped` (3cc6146fc) |
| 2 | `DRY-validation` | SHIPPED | Low | Medium (contract drift if helper mis-wired) | `status: shipped` (118b1aa2d) |
| 3 | `type-alias-strip` | FALSE PREMISE | Low | HIGH (3 active callers — retiring breaks reindex + download paths) | `status: false-premise` (documented) |
| 4 | `dead-field-retire` | FALSE PREMISE | Medium | **CRITICAL** (clip fan-out consumer — retiring breaks clip-splitting for all stock+register callers) | `status: false-premise` (documented) |
| 5 | `/run-deprecation` | FALSE PREMISE | High | HIGH (2 STK-E2E smokes + 1 production handler + 2 operator docs reference the route) | `status: false-premise` (documented) |

**Honest scope-lock**: 2 sub-PRs SHIPPED, 3 sub-PRs FALSE-PREMISE (documented + abandoned per godlike/07 NO-FAKE-AVAILABILITY). The wave is **complete** upon this commit landing — zero remaining work.

---

## §2 — Per-PR migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

### §2.1 — `PR-STOCK-DRY-VALIDATION` ✅ SHIPPED (commit `118b1aa2d`)

**Goal**: extract the 22-line duplicated validation block into a single canonical helper.

**Surface (2 files)**:
- `internal/api/assets/stock/handler.go` — `applyStockDefaults` helper + 2 caller updates (net -22 LoC in handler.go; the new helper + 2 call sites + comments add ~70 LoC for net +146/-70 in the diff)
- `internal/api/assets/stock/validation_test.go` (NEW) — 5 hermetic TDD tests + 2 sub-cases

**Verification**: `gofmt -l` clean; `go vet ./internal/api/assets/stock/...` exit 0; `go build ./internal/api/assets/stock/...` exit 0; `go test -short -count=1 ./internal/api/assets/stock/` PASSES (5 NEW + all pre-existing).

**godlike/06 SSOT (one canonical owner per fact)**: `applyStockDefaults` + `stockValidationInput` + `stockValidationDefaults` live ONLY at `internal/api/assets/stock/handler.go`; the helper is private to the stockapi package.

**3-surface lockstep (per CANONICAL.md §1)**: `CHANGELOG.md ## Unreleased > ### Refactor` mirror + `AGENTS.md ## Recent cross-cutting closures` mirror.

### §2.2 — `PR-STOCK-NO-PLACEHOLDERS` ✅ SHIPPED (commit `3cc6146fc`)

**Goal**: remove the silent-success `drive` + `indexed` + `location` placeholder fields from BOTH `/run` and `/search-and-run` responses.

**Surface (2 files)**:
- `internal/api/assets/stock/handler.go` — REMOVED 3 placeholder lines from BOTH handlers; REMOVED 2 dead helper functions; ADDED 7-line godlike/07 NO-FAKE-AVAILABILITY goddoc comment
- `internal/api/assets/stock/handler_test.go` — ADDED 4 hermetic TDD tests (2 async-mode + 2 sync-mode)

**Verification**: `gofmt -l` clean; `go vet` + `go build` exit 0; `go test -short ./internal/api/assets/stock/` PASSES (4 NEW + 12 pre-existing = 16 total).

**Round-2 fixups** (per code-reviewer verdict): added 2 sync-mode tests to lock the conditional `status_url` on `job_id != ""`; tightened the 7-line godoc comment in `RunStockPipeline` to a 3-line reference (rationale already lives in `SearchAndRun` per godlike/06 SSOT).

**3-surface lockstep**: CHANGELOG + AGENTS + this action plan (§1 + §2.2).

### §2.3 — `PR-STOCK-FIELD-RETIRE` ❌ FALSE PREMISE (no commit)

**Verdict**: the field `SecondsPerSegment` in `StockRunPayload` is actively consumed for clip fan-out (splitting URLs into multiple clips when `seconds_per_segment > 0`). Retiring would break the canonical godlike/06 SSOT threading across 3 architectural layers (DTO → command → pipeline step).

**Cross-references**:
- `internal/application/assets/providers/stock/stockpipeline/payloads.go` (wire-shape DTO)
- `internal/application/assets/providers/stock/stockpipeline/command.go::FromRunPayload` (propagation)
- `internal/application/assets/providers/stock/stockpipeline/step_plan_clips.go` (active consumer)
- `internal/api/assets/register/` (fan-out trigger: `if seconds_per_segment > 0`)

**Action**: NO commit. The field stays. Documented per godlike/07 NO-FAKE-AVAILABILITY.

### §2.4 — `PR-CLIPS-ENQUEUEREQUEST-RETIRE` ❌ FALSE PREMISE (no commit)

**Verdict**: the type alias `enqueueRequest = jobservice.EnqueueRequest` in `internal/api/assets/clips/handler.go:246` has 3 active callers (`handler_index.go:42` + `handler_index.go:108` + `handler_download.go:71`). Retiring would force 3 caller-site renames for zero functional benefit.

**Action**: NO commit. The alias stays. Documented per godlike/07 NO-FAKE-AVAILABILITY.

### §2.5 — `PR-STOCK-RUN-DEPRECATION` ❌ FALSE PREMISE (no commit)

**Verdict**: the `/api/stock-pipeline/run` endpoint has 2 STK-E2E smoke callers (A + C) + 1 production handler caller + 2 operator doc references. Deprecation would break the canonical STK-E2E-BATTERY-2026-07-05 wave's verification surface.

**Action**: NO commit. The endpoint stays ACTIVE. The DRY-validation sub-PR (§2.1) consolidated the internal duplicate without changing the public surface — the right approach per godlike/07 minimum-blast-radius.

---

## §3 — Per-PR execution checklist (gofmt + go vet + go build + go test -short)

For each SHIPPED sub-PR, the verification gates below were green pre-push:

### §3.1 — `PR-STOCK-DRY-VALIDATION` (SHIPPED) verification gates

```bash
gofmt -w internal/api/assets/stock/handler.go internal/api/assets/stock/validation_test.go
gofmt -l internal/api/assets/stock/handler.go internal/api/assets/stock/validation_test.go  # CLEAN
go vet ./internal/api/assets/stock/...                                                          # exit 0
go build ./internal/api/assets/stock/...                                                        # exit 0
go test -short -count=1 ./internal/api/assets/stock/                                            # PASS (5 NEW + 7 pre-existing)
```

### §3.2 — `PR-STOCK-NO-PLACEHOLDERS` (SHIPPED) verification gates

```bash
gofmt -w internal/api/assets/stock/handler.go internal/api/assets/stock/handler_test.go
gofmt -l internal/api/assets/stock/handler.go internal/api/assets/stock/handler_test.go  # CLEAN
go vet ./internal/api/assets/stock/...                                                     # exit 0
go build ./internal/api/assets/stock/...                                                   # exit 0
go test -short -count=1 ./internal/api/assets/stock/                                       # PASS (4 NEW + 12 pre-existing = 16 total)
```

### §3.3 — `PR-STOCK-FIELD-RETIRE` (FALSE PREMISE) — no gates executed

Per godlike/07 NO-FAKE-AVAILABILITY: when a sub-PR is a false-premise, the gate suite is NOT executed (no code change to verify). The §0.4 rg output IS the verification — it confirms the field has active consumers, so retiring would break them.

### §3.4 — `PR-CLIPS-ENQUEUEREQUEST-RETIRE` (FALSE PREMISE) — no gates executed

Per godlike/07 NO-FAKE-AVAILABILITY: when a sub-PR is a false-premise, the gate suite is NOT executed. The §0.5 rg output IS the verification — it confirms 3 active callers, so retiring would break them.

### §3.5 — `PR-STOCK-RUN-DEPRECATION` (FALSE PREMISE) — no gates executed

Per godlike/07 NO-FAKE-AVAILABILITY: when a sub-PR is a false-premise, the gate suite is NOT executed. The §0.1 rg output IS the verification — it confirms 2 STK-E2E smoke callers + 1 production handler + 2 operator doc references, so deprecation would break the canonical verification surface.

---

## §4 — Verification gates (wave-level)

For the **wave as a whole** (this closure commit), the verification is documentation-only:

```bash
rg 'applyStockDefaults|stockDrivePlaceholder|stockLocationPlaceholder' internal/ --type go   # 0 hits for placeholders (REMOVED); 1 hit for applyStockDefaults
git log --oneline -5 -- internal/api/assets/stock/                                             # SHIP SHAs visible: 118b1aa2d + 3cc6146fc
go vet ./internal/api/assets/stock/...                                                         # exit 0 (no production code change in this commit)
go build ./internal/api/assets/stock/...                                                      # exit 0
go test -short -count=1 ./internal/api/assets/stock/                                            # PASS (no regression)
```

**godlike/07 NO-FAKE-AVAILABILITY**: the wave-closure meta-record (this action plan + CHANGELOG + AGENTS + wave-tracker slot) is the **canonical SOLE proof** of the 5-sub-PR audit. The 2 SHIPPED sub-PRs are the canonical ground truth; the 3 FALSE-PREMISE sub-PRs are the canonical ground truth of NON-RETIREMENT.

---

## §5 — Honest scope-lock (carry-forward unchanged)

**Pre-existing 6-item voiceover + app build-issue carry-forward** (per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`): UNCHANGED. NOT a regression of this wave.

**Pre-existing YAML parse carry-forward** (per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` + `PR-CURRENT-YAML-PARSE-FIX-PART-6`): UNCHANGED. The wave-tracker slot in `architecture/waves/wave_p3_low_and_audit.yaml` may be DEFERRED per this carry-forward (verified at wave-write time: both `wave_p0_critical.yaml` + `wave_p3_low_and_audit.yaml` fail `yaml.safe_load` on this host). The closure meta-record in this action plan + CHANGELOG + AGENTS is the canonical SOLE record until the parse carry-forward is resolved.

**Stock residue cleanup** (per `stock-residue-cleanup-script` closure 2026-07-06): orthogonal to this wave. The stock-residue pre-trash-verification dataset from VELOX-scope-current OAuth principal was empty (no candidates), per the prior audit-pin; this wave is independent.

**PR-CLIPS-FAT-HANDLER-CONTRACT wave** (per the earlier audit): 4/4 sub-PRs landed, 1 deprecation godoc-only (`Handler.RegisterJobHandlers`). Orthogonal to this wave — the 3 false-premise sub-PRs in this wave are different surfaces (handler.go direct imports + 1 StockRunPayload field), NOT the IngestHandler fields / RegisterJobHandlers / clip_action.go methods that PR-CLIPS-FAT-HANDLER-CONTRACT covered.

---

## §6 — Cross-references (godlike/06 SSOT umbrella)

- **`architecture/waves/wave_p3_low_and_audit.yaml#STOCK-CLIPS-CLEANUP-2026-07-08`** (DEFERRED): wave-tracker entry.
- **`architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`**: 6-item carry-forward (NOT regressions).
- **`architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04`** + **`#PR-CURRENT-YAML-PARSE-FIX-PART-6`**: parse-error carry-forward (forward-pointer unblocks wave-tracker slot).
- **`internal/api/assets/stock/handler.go`**: canonical owner of `applyStockDefaults` helper + 2 response shapes (`{job_id, message, status_url}`).
- **`internal/api/assets/stock/validation_test.go`**: 5 TDD tests for the validation contract.
- **`internal/api/assets/stock/handler_test.go`**: 4 TDD tests for the no-placeholders contract.
- **`internal/application/assets/providers/stock/stockpipeline/payloads.go`**: canonical owner of `StockRunPayload` (incl. active `SecondsPerSegment` field).
- **`internal/application/assets/providers/stock/stockpipeline/command.go::FromRunPayload`**: canonical owner of the propagation path (incl. `SecondsPerSegment`).
- **`internal/application/assets/providers/stock/stockpipeline/step_plan_clips.go`**: canonical owner of the clip fan-out consumer.
- **`internal/api/assets/clips/handler.go:246`**: canonical owner of `type enqueueRequest = jobservice.EnqueueRequest` alias.
- **`internal/api/assets/clips/handler_index.go`** + **`handler_download.go`**: 3 active callers of `enqueueRequest`.
- **`internal/api/assets/register/`**: canonical owner of the `seconds_per_segment > 0` fan-out trigger.
- **`tests/operational/stock_e2e_route_aliveness_smoke.sh`** + **`stock_e2e_direct_url_smoke.sh`**: STK-E2E-A + STK-E2E-C smoke callers of `/api/stock-pipeline/run`.
- **`architecture/action-plans/2026-07-05-stock-e2e-battery.md`**: canonical narrative for the STK-E2E wave.
- **`docs/operations/stock-e2e-runbook.md`**: operator-facing runbook.
- **AGENTS.md §Recent cross-cutting closures**: PR-STOCK-DRY-VALIDATION + PR-STOCK-NO-PLACEHOLDERS mirror entries (already shipped).
- **CHANGELOG.md ## Unreleased > ### Refactor**: 2 closure meta-entries for the shipped sub-PRs (already landed).

---

## §7 — Wave-flip criterion

The wave flips to `status: shipped + exit_signal: true` when **ALL 5 of the following are true**:

1. ✅ `PR-STOCK-DRY-VALIDATION` reaches `status: shipped` (commit `118b1aa2d`).
2. ✅ `PR-STOCK-NO-PLACEHOLDERS` reaches `status: shipped` (commit `3cc6146fc`).
3. ✅ `PR-STOCK-FIELD-RETIRE` documented as `status: false-premise` (rg evidence in §0.4).
4. ✅ `PR-CLIPS-ENQUEUEREQUEST-RETIRE` documented as `status: false-premise` (rg evidence in §0.5).
5. ✅ `PR-STOCK-RUN-DEPRECATION` documented as `status: false-premise` (rg evidence in §0.1).

**Current state (2026-07-08)**: **5/5 sub-PRs closed**. Wave-flip is **ready** upon this commit landing.

**Wave-flip action**: the wave-tracker slot in `architecture/waves/wave_p3_low_and_audit.yaml` (DEFERRED per pre-existing YAML parse carry-forward) will be flipped to `status: shipped + exit_signal: true` when `PR-CURRENT-YAML-PARSE-FIX-PART-6` (or successor) lands and unblocks the parse. Until then, the closure meta-record in this action plan + CHANGELOG + AGENTS is the canonical SOLE proof of the 5/5 sub-PR closure.

---

## §8 — Per-PR canonical SHAs (lockstep record)

| Sub-PR | Status | Canonical SHA | Commit subject |
|--------|--------|---------------|----------------|
| `PR-STOCK-DRY-VALIDATION` | shipped | `118b1aa2d` | `refactor(stock): PR-STOCK-DRY-VALIDATION — extract canonical applyStockDefaults helper` |
| `PR-STOCK-NO-PLACEHOLDERS` | shipped | `3cc6146fc` | `refactor(stock): PR-STOCK-NO-PLACEHOLDERS — remove silent-success placeholder fields` |
| `PR-STOCK-FIELD-RETIRE` | false-premise | (no commit) | n/a — documented in §0.4 + §2.3 |
| `PR-CLIPS-ENQUEUEREQUEST-RETIRE` | false-premise | (no commit) | n/a — documented in §0.5 + §2.4 |
| `PR-STOCK-RUN-DEPRECATION` | false-premise | (no commit) | n/a — documented in §0.1 + §2.5 |

---

## §9 — Forward-pointers

- `PR-CURRENT-YAML-PARSE-FIX-PART-6` (deadline 2026-08-15) — unblocks the wave-tracker slot append to `architecture/waves/wave_p3_low_and_audit.yaml`. The canonical slot template is in the wave-tracker entry format from the §7 wave-flip criterion + the linked_issues block enumerating the 5 sub-PRs.
- `PR-CLIPS-FAT-HANDLER-CONTRACT` (orthogonal wave, prior audit) — covers the `Handler.RegisterJobHandlers` godoc-only deprecation + 4 unused IngestHandler fields. This wave is **complete** per the prior 4/4 sub-PR closures; cross-validation confirms no surface overlap with this wave.
- `PR-CLIPS-PORT-EXTRACT` (parallel wave, planning entry) — covers the 3 direct infra imports in `clips/handler.go` (drive.Admin + clipindexer.Service + semantic.MetadataWriter). This wave is independent — it does NOT touch the surfaces audited here (the 3 surfaces are in `internal/api/assets/stock/handler.go`, not `clips/handler.go`).

---

## §10 — Lifecycle audit-trail + Co-authored-by

- **2026-07-08**: Wave closure meta-record landed (this commit) — action plan + CHANGELOG + AGENTS mirror + wave-tracker slot DEFERRED per pre-existing YAML parse carry-forward.
- **2026-07-08**: `PR-STOCK-DRY-VALIDATION` shipped (commit `118b1aa2d`).
- **2026-07-08**: `PR-STOCK-NO-PLACEHOLDERS` shipped (commit `3cc6146fc`, round-2 fixups included).
- **2026-07-08**: `PR-STOCK-FIELD-RETIRE` audited as FALSE PREMISE (rg evidence in §0.4).
- **2026-07-08**: `PR-CLIPS-ENQUEUEREQUEST-RETIRE` audited as FALSE PREMISE (rg evidence in §0.5).
- **2026-07-08**: `PR-STOCK-RUN-DEPRECATION` audited as FALSE PREMISE (rg evidence in §0.1).
- **2026-07-08**: Wave-flip criterion **5/5 sub-PRs closed** (2 shipped + 3 false-premise documented).
- **Forward-pointer**: `PR-CURRENT-YAML-PARSE-FIX-PART-6` (deadline 2026-08-15) unblocks the canonical wave-tracker slot append; until then, this action plan is the canonical SOLE closure record.

**3-surface godlike/06 SSOT lockstep (per CANONICAL.md §1)**:
- `architecture/action-plans/2026-07-08-stock-clips-cleanup.md` (this file) = canonical narrative
- `CHANGELOG.md ## Unreleased > ### Documentation` (closure meta-entry) = canonical surface 2
- `AGENTS.md ## Recent cross-cutting closures` (mirror entry) = canonical surface 3
- `architecture/waves/wave_p3_low_and_audit.yaml#STOCK-CLIPS-CLEANUP-2026-07-08` (DEFERRED) = canonical surface 4

**Direct-to-main per AGENTS.md Git-Lesson-2** (no branches, no `--no-ff`, no `--force`).
**Co-authored-by**: PipelineGen Agent <agent@pipelinegen.local> per AGENTS.md Git-Lesson-3.
**Race-protect**: `git fetch origin + git log --oneline HEAD..@{u}` returns empty pre-push per AGENTS.md Git-Lesson-4.
