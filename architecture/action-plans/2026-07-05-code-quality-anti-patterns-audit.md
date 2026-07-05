# Code-Quality Anti-Patterns Audit — Action Plan

**Date:** 2026-07-05
**Author:** PipelineGen Agent
**Owner:** architecture doc maintainer + 4 per-hotspot owners + 3 forward-pointer wave owners
**Scope:** Audit of the 4 most-impactful code-quality anti-patterns visible in the `origin/main` snapshot of 2026-07-05 + 3 P1 forward-pointers that are already covered by other in-flight waves. The audit is **diagnostic-first**: each entry must be measurable (≥ 1 occurrence in `internal/`) and diagnostically distinct (no overlapping concerns).
**Status:** pending (wave-tracker anchor `architecture/current.yaml#CODE-QUALITY-AUDIT-2026-07-05`)
**Audit-trail anchor:** `architecture/current.yaml#CODE-QUALITY-AUDIT-2026-07-05`
**Companion entries:**
- `architecture/current.yaml#CODE-QUALITY-CLEANUP-2026-07-04` (existing 12-P0/P1 cleanup wave — NOT duplicated here; only cross-referenced)
- `architecture/current.yaml#CUT-FALSE-SUCCESS-FIRST-2026-07-04` (sibling false-success-first wave — INTENTIONALLY distinct)
- `AGENTS.md` §Recent cross-cutting closures (today's mirror entry)
- `CHANGELOG.md` `## Unreleased > ### Added` (closure meta-entry, today)

---

## TL;DR

The codebase has **4 P0 anti-patterns** that violate the three dominant discipline lenses (`godlike/06 SSOT` one-owner-per-fact, `godlike/07 no-fake-availability`, `AGENTS.md Pattern 5` 250-LoC cap) at compile-time or data-discrepancy severity. Each is grounded in real evidence and cross-referenced to an existing wave-tracker slot or to a sibling wave-tracker entry. **3 P1 forward-pointers** point at gaps already covered by other in-flight waves (QDRANT-CHAIN-VERIFY-2026-07-04 + GODOBJ-2026-07-03 + CODE-QUALITY-CLEANUP-2026-07-04); this audit does not duplicate them, it just confirms the deferral is correct.

```
                       CODE-QUALITY-AUDIT-2026-07-05 — 4 P0 + 3 P1
                       ──────────────────────────────────────────────

  ┌──── P0 HOTSPOTS ──────── 4 immediate must-fix anti-patterns ──────────────┐
  │  P0-1  Composition Monolith (Pattern 5 LoC Violation)                    │
  │       [composition.go > 250 LoC cap — direct SSOT entanglement]          │
  │  P0-2  Premature Metric Increment (Fake-Success)                         │
  │       [Processed++ / status=completed BEFORE Commit() returns]           │
  │  P0-3  Wire Interface Shadowing (godlike/06 SSOT break)                  │
  │       [ad-hoc wire types duplicating canonical production ports]          │
  │  P0-4  Stale Build Carry-Forwards (CI Blindness)                         │
  │       [PRE-EXISTING-BUILD-ISSUES-2026-07-04 — 6 items blocking build]    │
  └───────────────────────────────────────────────────────────────────────────┘

  ┌──── P1 FORWARD-POINTERS ── 3 defensible gaps, deferred to other waves ────┐
  │  P1-1  Silent-Success IndexClip gap (already in QDRANT-CHAIN-VERIFY)      │
  │  P1-2  Unfinished God-Object Splits (GODOBJ-2026-07-03 in flight)        │
  │  P1-3  Dead-Code Registry Residue (CODE-QUALITY-CLEANUP-2026-07-04)       │
  └───────────────────────────────────────────────────────────────────────────┘
```

---

## 1. Honest Limitation Declaration (godlike/07)

### 1.1 The three discipline lenses

The audit scores each hotspot against three lenses, in order of severity:

1. **godlike/06 SSOT (one canonical owner per fact)**: every wire-shape + every slot + every state-machine transition must have exactly one canonical owner. A second owner (even a "wire-time shadow") violates the discipline.
2. **godlike/07 no-fake-availability**: any path that returns `STATUS=OK` OR increments a counter BEFORE the durable write commits is fake-availability — silent data loss is the failure mode. Audit signals: `_ = stmt.Close()` / `ReturnProcessed++` before `tx.Commit()` / `WriteFile succeeded` without verify.
3. **AGENTS.md Pattern 5 (file cap 250 LoC, 8-dep constructor cap)**: files in a feature directory must not exceed 250 LoC unless explicitly grandfathered; constructors must declare at most 8 deps; capability-without-descriptor fails. `internal/app/` is the canonical SSOT surface for the composition root and is the most under-enforced zone today.

### 1.2 Static priority vs git-log frequency

This audit is derived from static analysis (file LoC counting, surface signature comparison, cross-file cross-reference). It is NOT derived from `git log --since=14.days --pretty=format: --name-only | sort | uniq -c` frequency measurement. The forward-pointer entry `PR-CODE-QUALITY-AUDIT-2026-07-05-HOTSPOT-CROSSREF` (deadline 2026-08-15) carries the post-wave cross-validation; if high-frequency hotspots emerge, they get append-only entries to the wave-tracker per godlike/06 slim-schema ratchet.

### 1.3 Pre-existing build issues carry forward unchanged

Per CHANGELOG forward-pointer convention: the 6-item carry-forward list at `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04.linked_issues` is unchanged. P0-4 below is the audit's framing of that list; CI unblocking of `go build ./...` is part of the existing meta-closure path, not a regression of THIS audit.

### 1.4 Why these 4 P0 (and not others)

The `CODE-QUALITY-CLEANUP-2026-07-04` wave-tracker already tracks 12 P0/P1 areas organized by God-object class. This audit does NOT duplicate those. The 4 P0 here are **DIAGNOSTICALLY DISTINCT**:

| Codice | Distinct from CODE-QUALITY-CLEANUP-2026-07-04 because... |
|---|---|
| P0-1 Composition Monolith | Composition-root level, not application-youtube/scripts level |
| P0-2 Premature Metric Increment | Cross-cutting runtime anti-pattern (data truth vs reported status), not a single-file split |
| P0-3 Wire Interface Shadowing | godlike/06 SSOT layer, not a deletion/split |
| P0-4 Stale Build Carry-Forwards | CI-level discipline, not per-package discipline |

Each maps to a different wave-tracker slot for the actual fix; audit just confirms the slots are correctly populated.

---

## 2. The 4 P0 Hotspots (immediate — fix before next feature wave)

### P0-1 — Composition Monolith (AGENTS.md Pattern 5 LoC Violation)

- **Anti-pattern signature**: `internal/app/composition.go` exceeds the 250 LoC cap (the canonical metric-tested baseline is 661 LoC on `origin/main`); multiple sibling bundle files in `internal/app/` track over the cap similarly; the composition root carries global state that violates godlike/06 "one canonical owner per fact" because wiring decisions + struct definitions co-exist in one file.
- **Evidence (observed 2026-07-05)**:
  - `internal/app/composition.go` ~21.8 KB (≈ 661 LoC baseline). Far over the 250 LoC cap.
  - Multiple `internal/app/composition_*.go` split attempts in flight (post-2026-07-04 session history) but the original file remains the canonical Go entry-point for `NewComposition`. Per the PR-COMPOSITION-7-FILE-SPLIT audit-pin (2026-07-05 prep), the file was attempted to be slimmed to ~456 LoC but the attempted split could not satisfy the ≤250 LoC constraint while hosting all 13 bundle types.
- **Impact score**: 3 (SSOT entanglement; godlike/06 violation; slow to navigate; risk of cross-file imports drift)
- **Frequency score**: 4 (Affects core app layer systematically; every feature boot path crosses composition.go)
- **Suggested remediation scope** (≤50 words): Per godlike/06 SSOT, distribute each struct declaration across its owning per-capability file. Use **embedded struct promotion** (anonymous embed `ProcessQdrantBundle` into `ProcessBundle`) to preserve consumer-code byte-stability (`.QdrantClient`, `.VectorSvc` continue to resolve via field promotion). Construction logic extracted to per-bundle `build_*_bundle.go` files.
- **Cross-ref**: `architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04.linked_issues[PR-COMPOSITION-BUNDLE-SPLIT]` (live forward-pointer) + `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04` (per-band residue scope).

### P0-2 — Premature Metric / Status Increment (Fake-Success)

- **Anti-pattern signature**: production code returns `STATUS=OK` OR increments `Processed++` / `succeeded` counter BEFORE the underlying database transaction commits. Failure mode: silent data loss — caller thinks the work is persisted but the DB rolled back / connection died.
- **Evidence (surfaced 2026-07-04, live on `origin/main`)**: `internal/application/assets/providers/artlist/` per `architecture/current.yaml#ARTLIST-PERSIST-FIX-2026-07-04`. The pattern: `processed++` counter incremented BEFORE persist step returns; `clip-core` MarkSucceeded emits `job_completed` event before `media_assets` writes succeed; diagnostic test `internal/application/assets/providers/artlist/diagnostic_fake_success_test.go` is `SKIP-by-default` to surface the issue.
- **Impact score**: 5 (Strict godlike/07 violation; silent data loss; SSOT drift between counter and DB)
- **Frequency score**: 3 (Repeats across asset provider handlers in `internal/application/assets/providers/**`)
- **Suggested remediation scope** (≤50 words): Move all metric / status increments inside the transaction's success-callback OR post-`tx.Commit()` block. NEVER increment before the durable Commit callback. Use typed sentinels (`ErrPersistedBeforeIncrement` etc.) where the pattern would have been a silent increment.
- **Cross-ref**: `architecture/current.yaml#ARTLIST-PERSIST-FIX-2026-07-04.linked_issues[artlist-fake-success-diagnostic-2026-07-04]` (live forward-pointer, ship_sha: f241568c, ship_date 2026-07-04).

### P0-3 — Wire Interface Shadowing (godlike/06 SSOT break)

- **Anti-pattern signature**: a wire-time or composition-time interface declared locally that duplicates a canonical production port from `internal/infrastructure/<x>/` or `internal/application/<x>/ports.go`. The duplicate shadows the canonical surface, breaking `var _ Port = (*Adapter)(nil)` compile-time pin discipline.
- **Evidence (observation from composition layer)**: composition root often leans on local inline struct literals for handler-to-port connections instead of importing the canonical Pattern 0 port. Examples (extract via `rg "type.*interface" internal/app/*.go`): inline publisher/receiver/adapter shapes that mirror `drive.Publisher` / `voiceover.DriveUploader` / `jobs.OutboxDispatcher` from canonical SSOT. Each shadowed type weakens the compile-time safety net and silently re-introduces drift.
- **Impact score**: 5 (Compile-time cascade broken → runtime drift + fake-availability; the canonical godlike/06 SSOT guarantee becomes advisory)
- **Frequency score**: 3 (Multi-occurrence across composition files; each bundle may carry its own shadowing interface)
- **Suggested remediation scope** (≤50 words): At composition root, import the canonical Pattern 0 port from the owning capability package and wire via `var _ Port = (*Adapter)(nil)` compile-time pin. Remove inline wire types. Migrate per-bundle per-file (per godlike/07 minimum-blast-radius); per-bundle wiring migration is a per-file micro-PR.
- **Cross-ref**: `architecture/current.yaml#ART-001.linked_issues[PR-ARTLIST-RECOMMEND-ADAPTER]` + `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04.linked_issues[PR-WIRE-ASSETS-NIL-CLASSIFICATION]` (precedent for typed-wiring discipline).

### P0-4 — Stale Build Carry-Forwards (CI Blindness)

- **Anti-pattern signature**: known pre-existing build breakages tracked out-of-band in `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04.linked_issues` but allowed to persist, keeping `go build ./...` globally broken and blinding CI to NEW regressions.
- **Evidence**: the `PRE-EXISTING-BUILD-ISSUES-2026-07-04` wave-tracker carries 6 items as of `origin/main` audit-pin (1 ship_sha: 03d42b0c + workerruntime syntax errors already addressed; 5 still in flight: monitor/enqueue `strings.ToLower` undefined, monitor/scheduler `NewUnboundJobEnqueuer` undefined, stockpipeline/run_upload.go missing-on-disk, app/module_media.go dispatcher literal, images/routing import cycle). Each observed in pre-commit `git show origin/main:<file>` recipe.
- **Impact score**: 4 (Nullifies base CI safeguards; obscures real regressions; new feature work crosses already-broken paths)
- **Frequency score**: 5 (Constant global effect across the monorepo; every pre-commit full-build test runs into the carry-forward)
- **Suggested remediation scope** (≤50 words): For each carry-forward, identify the canonical interface/symbol mismatch via `git show origin/main:<file>` recipe (proves carry-forward on stashed pre-PR tree), then surgically retire the legacy surface (godlike/07 minimum-blast-radius). Track each retirement in `PR-META-CLOSURE-SHA-CORRECTION` (forward-pointer 2026-07-15).
- **Cross-ref**: `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (canonical meta-closure wave-tracker). Resolution path mostly already in flight via per-band closure PRs.

---

## 3. The 3 P1 Forward-Pointers (defensible gaps, deferred)

### P1-1 — Silent-Success IndexClip Gap (Qdrant Chain Verify)

- **Anti-pattern signature**: `IndexClip` short-circuits to `return nil` when the underlying subsystem is disabled, leaving the outbox dispatcher to mark `asset.index.requested` `completed` despite zero Qdrant writes. The pattern is: caller-level short-circuit returning `nil` + caller-level status assertion that treats `nil` as success.
- **Evidence**: 6 net-new `linked_issues` in `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04`. Test 11 of the 11-test Operator Pre-flight Checklist (`architecture/action-plans/2026-07-04-qdrant-verification-chain.md` §4) directly probes this gap.
- **Forward-pointer wave**: `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04.linked_issues[PR-QDRANT-INDEXCLIP-GUARD]` (Band A P0 fail-closed semantics; deadline 2026-07-15).
- **Audit conclusion**: deferral to QDRANT-CHAIN-VERIFY-2026-07-04 is correct; no new code change proposed here.

### P1-2 — Unfinished God-Object Splits

- **Anti-pattern signature**: classes/services combining extraction / orchestration / persistence across >500 LoC of mixed concerns. The previous `GODOBJ-2026-07-03` wave-tracker surveyed 12 such files and assigned 4 priority bands (P0 absolute, Mechanical, Cut-not-split, Small-but-dangerous). Most are still in flight.
- **Evidence**: `architecture/current.yaml#GODOBJ-2026-07-03.linked_issues` (16 slim-shape entries). 12 per-file + 1 cross-reference + 3 cross-references.
- **Forward-pointer wave**: `architecture/current.yaml#GODOBJ-2026-07-03` (12 net-new PRs distributed across 4 priority bands; per-band deadlines 2026-07-25 → 2026-08-22). Cross-validation forward-pointer `PR-GODOBJ-HOTSPOT-CROSSREF` deadline 2026-08-01.
- **Audit conclusion**: deferral is correct; the wave-tracker owns the per-file migration timeline. No new code change proposed here.

### P1-3 — Dead-Code / Aspirational Registry Residue

- **Anti-pattern signature**: "future-proofing" registries, unreachable parser fragments, 0-caller interfaces remaining in the binary. Previously-cleaned examples: `style_registry.Register`, `scene_stubs.go`, `media_curator_stubs.go`, `discovery.go::discoverSearchQueries`, `SceneBuilderUseCase`.
- **Evidence**: the previous `PR-DEAD-CODE-PURGE-2026-07-25` closure (commits 5a32611d + a9bfe0dd + 7fe92a48) cleaned 4 user-spec surfaces + 1 bonus surface. Remaining residue is lighter but still observable.
- **Forward-pointer wave**: `architecture/current.yaml#CODE-QUALITY-CLEANUP-2026-07-04.linked_issues[PR-LEGACY-QUARANTINE]` + `[PR-CODE-QUALITY-AUDIT-NEXT-CYCLE]` (deadline 2026-08-22).
- **Audit conclusion**: deferral is correct; the cleanup wave owns the per-file audit-and-delete work. No new code change proposed here.

---

## 4. Impact × Frequency Matrix

The combined score is `Impact × Frequency` (max 25). Higher combined score → fix first.

| ID | Title | Impact (1-5) | Frequency (1-5) | Combined | Cross-ref Wave | Deadline |
|---|---|---:|---:|---:|---|---|
| **P0-4** | Stale Build Carry-Forwards (CI Blindness) | 4 | 5 | **20** | `PRE-EXISTING-BUILD-ISSUES-2026-07-04` | 2026-07-31 |
| **P0-2** | Premature Metric Increment (Fake-Success) | 5 | 3 | **15** | `ARTLIST-PERSIST-FIX-2026-07-04` | 2026-08-01 |
| **P0-3** | Wire Interface Shadowing (godlike/06 SSOT break) | 5 | 3 | **15** | `EXTERNAL-AUDIT-2026-07-04` + `AUDIT-RESIDUE-2026-07-04` | 2026-08-15 |
| **P0-1** | Composition Monolith (Pattern 5 LoC Violation) | 3 | 4 | **12** | `EXTERNAL-AUDIT-2026-07-04.linked_issues[PR-COMPOSITION-BUNDLE-SPLIT]` | 2026-08-15 |
| P1-1 | Silent-Success IndexClip Gap (forward-pointer) | 5 | 2 | 10 | `QDRANT-CHAIN-VERIFY-2026-07-04` | 2026-07-15 |
| P1-2 | Unfinished God-Object Splits (forward-pointer) | 3 | 4 | 12 | `GODOBJ-2026-07-03` | 2026-08-22 |
| P1-3 | Dead-Code / Aspirational Registry Residue (forward-pointer) | 2 | 4 | 8 | `CODE-QUALITY-CLEANUP-2026-07-04.linked_issues[PR-LEGACY-QUARANTINE]` | 2026-08-22 |

**Fix-first ordering**: P0-4 → P0-2 → P0-3 → P0-1. Rationale below in §6.

---

## 5. Cross-Ref Lockstep (4-surface godlike/06 SSOT)

Per `CANONICAL.md` §1 + AGENTS.md Git-Lesson-2/3/4/5: each audit entry MUST appear on 4 surfaces (operator narrative + wave-tracker SSOT + CHANGELOG closure meta-entry + AGENTS mirror). The table below maps each P0/P1 to its canonical lockstep surface.

| Audit ID | Action Plan § | current.yaml `linked_issues[].id` | CHANGELOG § | AGENTS.md § |
|---|---|---|---|---|
| **P0-1** Composition Monolith | §2 | `PR-COMPOSITION-MONOLITH-LOCCAP-2026-07-05` | `## Unreleased > ### Refactor` | `§Recent cross-cutting closures > PR-COMPOSITION-MONOLITH` |
| **P0-2** Premature Metric Increment | §2 | `PR-FALSE-SUCCESS-METRICS-AUDIT-2026-07-05` | `## Unreleased > ### Fixed` | `§Recent cross-cutting closures > PR-FALSE-SUCCESS-METRICS` |
| **P0-3** Wire Interface Shadowing | §2 | `PR-WIRE-INTERFACE-SHADOWING-AUDIT-2026-07-05` | `## Unreleased > ### Fixed` | `§Recent cross-cutting closures > PR-WIRE-INTERFACE-SHADOWING` |
| **P0-4** Stale Build Carry-Forwards | §2 | `PR-STALE-BUILD-CARRY-FORWARDS-AUDIT-2026-07-05` | `## Unreleased > ### Fixed` | `§Recent cross-cutting closures > PR-STALE-BUILD-CARRY-FORWARDS` |
| P1-1 IndexClip Silent-Success | §3 | (forward-pointer only) | (no CHANGELOG entry — lives in QDRANT-CHAIN-VERIFY-2026-07-04) | (no entry — parent wave owns narrative) |
| P1-2 God-Object Splits | §3 | (forward-pointer only) | (no CHANGELOG entry — lives in GODOBJ-2026-07-03) | (no entry — parent wave owns narrative) |
| P1-3 Dead-Code Residue | §3 | (forward-pointer only) | (no CHANGELOG entry — lives in CODE-QUALITY-CLEANUP-2026-07-04) | (no entry — parent wave owns narrative) |

**Lockstep surfaces (4 canonical)**:
1. **Narrative** — this `architecture/action-plans/2026-07-05-code-quality-anti-patterns-audit.md` file (per-hotspot depth + matrix + cross-ref).
2. **Tracker** — `architecture/current.yaml#CODE-QUALITY-AUDIT-2026-07-05` (4 P0 linked_issues + 3 P1 forward_pointers, slim schema: id/owner_capability/status/deadline only).
3. **Closure** — `CHANGELOG.md` `## Unreleased > ### Added` (one entry per audit slot).
4. **Mirror** — `AGENTS.md` `## Recent cross-cutting closures` (audit-pin mirror entry).

---

## 6. Audit Ordering — Which P0 closes first?

**Execution Order:** **P0-4 → P0-2 → P0-3 → P0-1**.

**Reasoning:**

1. **P0-4 (Carry-Forwards) closes first** because every other refactor (per-file split, per-feature cleanup, godlike/06 fix) depends on `go build ./...` being green at least for the touched sub-tree. The carry-forward list currently blocks whole-project builds; without first retiring (or honest-acknowledging) the 6 items, CI cannot detect new regressions in the affected sub-trees during subsequent PR waves.

2. **P0-2 (Premature Metric Increment) closes second** because it is the canonical godlike/07 NO-FAKE-AVAILABILITY fix pattern. Closing it first sets the discipline baseline for the subsequent refactors — every new handler added in the P0-3 / P0-1 waves must NOT carry the same bug forward.

3. **P0-3 (Wire Interface Shadowing) closes third** because once metric discipline is enforced and metric increments are honest, the wire-shape refactors can proceed without risk of compounding fake-availability in the new surface. Per-bundle wiring migration is per-file micro-PR (godlike/07 minimum-blast-radius).

4. **P0-1 (Composition Monolith) closes fourth** because it requires the cleanest possible canonical interfaces (godlike/06 SSOT locked) before the slimming-to-≤250-LoC split can succeed. The prior session's attempt to slim composition.go to 456 LoC could not satisfy ≤250 because the bundle types couldn't fit; the embedded-struct-promotion pattern (per the thinker topology validation) is the canonical solution.

The 3 P1 forward-pointers are NOT closed in this audit wave; they are confirmed as covered by the 3 parent waves (QDRANT-CHAIN-VERIFY-2026-07-04, GODOBJ-2026-07-03, CODE-QUALITY-CLEANUP-2026-07-04). Closure is the parent's responsibility; this audit's job is to surface them as cross-references, not to fork new closure PRs.

---

## 7. What IS NOT in scope (godlike/07 honest scope-lock)

**Not in scope (already covered elsewhere):**
- Qdrant schema versioning (covered in `PR-QDRANT-PREFLIGHT-SCHEMA-V3-SHIPPED`, shipped 2026-07-04 via `architecture/qdrant/v3-schema.json`).
- Re-purging of dead-code surfaces already retired (`style_registry.Register`, `scene_stubs.go`, `media_curator_stubs.go`, `discoverSearchQueries`, `SceneBuilderUseCase`) — done in commit chain 5a32611d + a9bfe0dd + 7fe92a48 (PR-DEAD-CODE-PURGE-2026-07-25, 2026-07-04).
- Artlist handler-layer silent-drops (forward-pointer workflow-gated).
- Drive Surface port migration completion (canonical 4-port surface post DRIVE-005 closure 2026-06-30; the 5 patterns are locked into architecture/ownership.generated.yaml).
- Stock pipeline service.go / orchestrator_steps.go split before correctness fix (P0 ordering PR-STOCK-CORRECTNESS-FIX → PR-STOCK-SERVICE-SPLIT → PR-STOCK-ORCHESTRATOR-SPLIT enforced).

**Not in scope (forward-pointers only):**
- The 3 P1 entries (§3). They confirm deferral correctness; no code change proposed here.

**Not in scope (separate audit cycles):**
- Q16 CHANGELOG/architecture current.yaml contention — separate `Q16` 2-PR split already in flight.
- Code-reviewer hardening at the voiceover / image routing boundaries — separate waves.
- Voiceover 5-stage CUTOVER migration — separate `PR-VO-FINALIZER-STEP6-EXTRACT` forward-pointer.

**Pre-existing build issues** (per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`) carry forward unchanged — NOT regressions of this audit. The audit's P0-4 documents the carry-forward but the actual closure PRs land under the existing meta-closure path, not here.

---

## 8. Lifecycle (audit-trail)

| Date | Action | Status | Author |
|---|---|---|---|
| 2026-07-05 | Plan doc lands (this file) | pending | PipelineGen Agent |
| 2026-07-05 | Wave-tracker entry lands (lockstep) | pending | PipelineGen Agent |
| 2026-07-05 | CHANGELOG.md closure meta-entry lands | documentation-only | PipelineGen Agent |
| 2026-07-05 | AGENTS.md mirror lands (Recent cross-cutting closures) | documentation-only | PipelineGen Agent |
| 2026-07-05 | git fetch + rebase onto origin/main + ff-push | direct-to-main (no branch, no `--force`) | PipelineGen Agent |
| 2026-07-15 | P1-1 IndexClip Silent-Success parent-wave Band A deadline (forward-pointer) | pending | (QDRANT-CHAIN-VERIFY-2026-07-04) |
| 2026-07-31 | P0-4 Stale Build Carry-Forwards target deadline | pending | (PRE-EXISTING-BUILD-ISSUES-2026-07-04) |
| 2026-08-01 | P0-2 Premature Metric Increment target deadline | pending | (ARTLIST-PERSIST-FIX-2026-07-04) |
| 2026-08-15 | P0-3 Wire Interface Shadowing target deadline | pending | (EXTERNAL-AUDIT-2026-07-04 / AUDIT-RESIDUE-2026-07-04) |
| 2026-08-15 | P0-1 Composition Monolith target deadline | pending | (EXTERNAL-AUDIT-2026-07-04.linked_issues[PR-COMPOSITION-BUNDLE-SPLIT]) |
| 2026-08-15 | PR-CODE-QUALITY-AUDIT-2026-07-05-HOTSPOT-CROSSREF deadline (post-wave moat check) | pending | (this audit's forward-pointer) |
| 2026-08-22 | P1-2 / P1-3 parent-wave deadlines (God-Object + Cleanup) | pending | (GODOBJ-2026-07-03 + CODE-QUALITY-CLEANUP-2026-07-04) |
| post-2026-08-22 | CODE-QUALITY-AUDIT-2026-07-05 exit_gate flip (status: done / exit_signal: true) — only if all 4 P0 hotspots confirmed green AND 3 P1 parent-waves shipped | UNLOCK | (TBD) |

---

## 9. Cross-references

- `AGENTS.md` §godlike/06 SSOT (one canonical owner per fact) → §DOCUMENTATION_MAP → §Documentation Map
- `AGENTS.md` §godlike/07 no-fake-availability (canonical typed-error contract)
- `AGENTS.md` Pattern 5 (file cap 250 LoC; 8-dep constructor cap)
- `AGENTS.md` Git-Lesson-2 (direct-to-main workflow; no `--no-ff`, no `--force`)
- `AGENTS.md` Git-Lesson-3 (Co-authored-by trailer for agent commits)
- `AGENTS.md` Git-Lesson-4 (non-fast-forward race recovery)
- `AGENTS.md` Git-Lesson-5 (byte-equivalent-replay acceptance)
- `architecture/current.yaml#CODE-QUALITY-CLEANUP-2026-07-04` (sibling wave: 12 P0/P1 areas; cross-referenced, NOT duplicated)
- `architecture/current.yaml#CUT-FALSE-SUCCESS-FIRST-2026-07-04` (sibling wave: stock + Qdrant + generated-search; cross-referenced, NOT duplicated)
- `architecture/current.yaml#EXTERNAL-AUDIT-2026-07-04` (P0-3 + P0-1 umbrella)
- `architecture/current.yaml#AUDIT-RESIDUE-2026-07-04` (P0-3 + P0-1 residue scope)
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (P0-4 meta-closure)
- `architecture/current.yaml#ARTLIST-PERSIST-FIX-2026-07-04` (P0-2 parent)
- `architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04` (P1-1 parent)
- `architecture/current.yaml#GODOBJ-2026-07-03` (P1-2 parent)
- `architecture/current.yaml#GODOBJ-HOTSPOT-CROSSREF` (P1-2 cross-validation forward-pointer)
- `architecture/ownership.generated.yaml` (canonical SSOT for one-owner-per-fact)
- `architecture/action-plans/2026-07-04-cut-false-success-first.md` (parent of P1-1 + P0-2 thinking)
- `architecture/action-plans/2026-07-04-qdrant-verification-chain.md` (parent of P1-1)
- `architecture/action-plans/2026-07-04-code-quality-cleanup-action-plan.md` (sibling 12-area cleanup plan)
- `architecture/action-plans/2026-07-04-qdrant-preflight-execution.md` (companion for QDRANT-CHAIN-VERIFY)

---

**End of canonical audit action plan.** Future agents: read this file alongside `architecture/current.yaml#CODE-QUALITY-AUDIT-2026-07-05` for the canonical SSOT state surface. The wave-tracker entry's `linked_issues[].shipped_sha` field is the audit-trail canonical.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local> (per AGENTS.md Git-Lesson-3)
