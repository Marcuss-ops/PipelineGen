# 2026-07-05 — Voiceover completion action plan

> **Audit-trail anchor:** `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04` (canonical wave-tracker, `status: in_progress`, `deadline: 2026-08-15`).
> **Trigger:** Italian audit snapshot pasted to the orchestrator on 2026-07-05 — voiceover session delta `c7c6aadd` (PR-VO-FANOUT-SIBLING-COLLAPSE-FIX) + 4 pre-session shippped fixes (PR-VO-SUBFOLDER / PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE / PR-VO-COMPLETEPATH-FIX / PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-BATCH+PROMO).
>
> **Rule:** NO BRANCHES — direct-to-main per AGENTS.md Git-Lesson-2.
> Each per-PR lands on `main` with auto-sufficient granularity; Co-authored-by trailer per Git-Lesson-3.

---

## §1 — Status snapshot (the truth source today)

**Shippped in voiceover subsystem (8 entries verified):**

| SHA | PR-ID | Surface |
|-----|-------|---------|
| `c7c6aadd` | PR-VO-FANOUT-SIBLING-COLLAPSE-FIX | `internal/application/voiceover/jobs/fanout_dedup_test.go` (this session) |
| `c96eb1e0` + `bf436cd2` | PR-VO-SUBFOLDER | `internal/infrastructure/drive/publisher.go::resolveDestination` |
| `1a369308` | PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE | `internal/infrastructure/drive/errors.go` + `publisher.go` typed sentinel |
| `db2f3b1e` + `e71d10ad` | PR-VO-COMPLETEPATH-FIX (+ TESTS) | `internal/application/jobs/registry.go` flip + `internal/application/voiceover/jobs/finalizer_invariants_test.go` |
| `75c1d585` | PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-BATCH+PROMO | `internal/application/jobs/registry.go` flip + `registry_contract_test.go` |
| pre-session | PR-VO-STAGES-SPLIT (P0 #2) | `internal/application/voiceover/stage_*.go` (5 files) |
| `0f64965e` | PR-VO-FINALIZER-STEP6-EXTRACT (P0 #3) | `internal/application/voiceover/finalizer_cleanup_outbox.go` |
| pre-session | PR-VO-PARENT-AGGREGATOR-SPLIT (P0 #4) | `internal/application/voiceover/jobs/parent_*.go` (4 files) |

**Status:pending in voiceover subsystem (3 entries + 1 audit-pin per wave-tracker snapshot 2026-07-05):**

| PR-ID | Band | Deadline | Scope |
|-------|------|----------|-------|
| **PR-VO-TYPED-PRIMITIVES** | P1.1 | 2026-07-25 | Typed `Language` / `StyleGroup` / `TextHash` primitives in `internal/application/voiceover` |
| **PR-VO-PARENT-STATE-COLUMN** | P1.2 | 2026-08-01 | Activate SQL dual-write for `parent_state_typed` column in `FinalizeAggregateParent` (migration 129 already shipped) |
| **PR-VO-USECASE-PROCESS-DRY** | P0 #5 | 2026-08-15 | DRY pair extraction for per-item generation + fanout use cases |
| **VO-DECOMPOSITION-HOTSPOT-CROSSREF** | post-wave audit | 2026-08-15 | `git log --since=90.days` post-wave cross-validation |

**Status uncertainty (audit said "chiuso probabilmente pre-session" but wave-tracker shows pending):**

| PR-ID | Status (wave-tracker) | Status (audit) | Verification command |
|-------|-----------------------|----------------|----------------------|
| PR-VO-TTS-PERSISTENT-WORKER | `pending` | "chiuso probabilmente pre-session" | `git log --grep='TTS-PERSISTENT'` + `rg 'exec\.CommandContext.*python3' internal/infrastructure/audio/` + check `Check 58` active in `scripts/ci-architectural-checks.sh` |

---

## §2 — Forward-pointers from other waves tied to voiceover (7)

| PR-ID | Source wave | Deadline | Hard-block ordering |
|-------|-------------|----------|---------------------|
| PR-P1.2-SQL-DUAL-WRITE | CUTOVER-COMPLETE-WITH-ARTIFACTS | 2026-08-15 | **BEFORE** PR-VO-PARENT-STATE-COLUMN |
| PR-VO-BACKFILL | VO-DECOMPOSITION | TBD | **AFTER** PR-VO-PARENT-STATE-COLUMN |
| PR-VO-CUTOVER | VO-DECOMPOSITION | TBD | **AFTER** PR-VO-BACKFILL |
| PR-VO-ASSET-LOCATIONS-CONSUMER-AUDIT | PR-VO-COMPLETEPATH-FIX child | 2026-08-01 | Standalone (cross-capability Strada A vs B decision) |
| PR-VO-SUBFOLDER-TEST | PR-VO-SUBFOLDER child | 2026-07-25 | Standalone (test surface completion) |
| PR-VO-SUBFOLDER-SENTINEL | PR-VO-SUBFOLDER child | 2026-07-25 | Standalone (typed sentinel gap) |
| PR-VO-AGGREGATE-SUBPATH-CASCADE | PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE child | 2026-08-15 | Standalone (aggregate-mode fail-closed) |
| PR-parent-state-cutover | CLEANUP-PRIORITY-1-5-2026-07-25 | 2026-08-15 | **AFTER** PR-VO-PARENT-STATE-COLUMN |

---

## §3 — Execution order (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

### Wave A — Documentation lockstep (this commit)

- New file: `architecture/action-plans/2026-07-05-voiceover-completion-action-plan.md` (this file).
- `CHANGELOG.md ## Unreleased > ### Documentation` closure meta-entry.
- `AGENTS.md ## Recent cross-cutting closures` mirror entry.

**Verification:** YAML parse clean (`python3 -c "import yaml; yaml.safe_load(open('architecture/current.yaml'))"`); `git log --oneline origin/main -1` shows new commit; `git diff --name-only HEAD~1` enumerates exactly the 3 surfaces (this plan + CHANGELOG + AGENTS).

### Wave B — Cross-wave blocking dependency (deadline 2026-08-15)

**Order #1: PR-P1.2-SQL-DUAL-WRITE** (CUTOVER-COMPLETE-WITH-ARTIFACTS wave, owner capability `internal/application/jobs`).
- Surface: `FinalizeAggregateParent` reads `resultMap["parent_state"]` (legacy JSON key) AND writes the same value to `JobParentStateColumn` (the new typed column from migration 129) in the SAME `'sql.Tx'`.
- godlike/06 SSOT: SQL layer is the SOLE writer; typed column + JSON key coexist (no double-write race; dual-write is the EXPAND-phase surface).
- Verification: `go test -run TestFinalizeAggregateParent_DualWrite ./internal/application/voiceover/jobs/` exit 0; new TDD test pins the contract.

### Wave C — VO-DECOMPOSITION remaining 3 entries (deadline 2026-08-01)

**Order #2: PR-VO-PARENT-STATE-COLUMN** (typed column activation, depends on Wave B).
- Flips `FinalizeAggregateParent` from `resultMap["parent_state"] -> typed` to `typed -> typed`. JSON key either retained as legacy fallback (BACKFILL phase) or marked deprecated (CUTOVER phase, gated by PR-VO-BACKFILL).
- godlike/06 SSOT: typed column is the SOLE canonical reader post-CUTOVER (the JSON key retire is PR-VO-CUTOVER).

**Order #3: PR-VO-TYPED-PRIMITIVES** (P1.1, deadline 2026-07-25 — earliest).
- Surface: typed aliases `type Language string` + `type StyleGroup string` + `type TextHash string` in `internal/application/voiceover/types_typed.go` (canonical SSOT).
- 4 typed-error sentinels: `ErrLanguageEmpty` + `ErrStyleGroupInvalid` + `ErrTextHashMalformed` + `ErrTypedPrimitiveConflict`.
- `rg 'Language|StyleGroup|TextHash' internal/application/voiceover/` raw-string sites → 0 hits post-closure (per Check 58 + Check 60 wave-tracker exit_gate).
- godlike/06 SSOT: typed aliases live ONLY in `internal/application/voiceover/types_typed.go`; no re-declaration in business logic files.

**Order #4: PR-VO-USECASE-PROCESS-DRY** (P0 #5 DRY pair, deadline 2026-08-15).
- Surface: collapse the per-item generation use case AND the fanout use case into a single canonical `ProcessVoiceoverSegmentUseCase` (mirrored on the existing ProcessYouTubeSegmentUseCase pattern).
- godlike/06 SSOT: the canonical use case is the SOLE writer for `media_assets_voiceover` inserts; legacy call sites compile-time pinned via `var _ ports.Segmenter = (*ProcessVoiceoverSegmentUseCase)(nil)`.

### Wave D — Check 58 forward-prevention + top-of-wave P0 (deadline 2026-08-15)

**Order #5: PR-VO-TTS-PERSISTENT-WORKER + Check 58** (gated by §1 audit-pin verification).
- IF `git log --grep='TTS-PERSISTENT'` returns zero SHAs: ship the persistent worker driver + Check 58 forward-prevention gate in a single atomic commit.
- IF audit was correct (already shipped): flip the wave-tracker entry to `status: shipped` with `ship_sha` pin, lockstep with this plan.
- `Check 58` bans `exec.CommandContext(python3, ...)` callers in `internal/infrastructure/audio/` outside the canonical persistent-worker.

### Wave E — Post-wave cross-validation (deadline 2026-08-15)

**Order #6: VO-DECOMPOSITION-HOTSPOT-CROSSREF** (post-wave frequency audit).
```bash
git log --since=90.days --pretty=format: --name-only \
  internal/application/voiceover/ \
  internal/application/voiceover/jobs/ \
  internal/infrastructure/audio/ \
  | sort | uniq -c | sort -rn | head -30
```
- 0 NEW high-frequency hotspots not in this plan → plan stays.
- N NEW hotspots → slim-schema append-only ratchet adds them with extended deadlines.

### Wave F — Companion forward-pointer closures (parallel after Wave C)

- **PR-VO-SUBFOLDER-TEST** (deadline 2026-07-25) — extend `publisher_test.go` with the missing-edge-case coverage (subpath-missing + override-conflict).
- **PR-VO-SUBFOLDER-SENTINEL** (deadline 2026-07-25) — new typed sentinel `ErrSubpathInvalidForOverride` in `internal/infrastructure/drive/errors.go`.
- **PR-VO-ASSET-LOCATIONS-CONSUMER-AUDIT** (deadline 2026-08-01) — Strada A (adapt `SceneRenderer.ResolveAssetWebViewLink` to fall back on `media_assets.DriveLink`) vs Strada B (extend `voiceover.finalizer` with 7th step writing `asset_locations`). Binomial decision surfaces in a sibling wave-tracker entry.
- **PR-VO-AGGREGATE-SUBPATH-CASCADE** (deadline 2026-08-15) — aggregate-mode fail-closed when fallback exercised.
- **PR-parent-state-cutover** (CLEANUP-PRIORITY-1-5-2026-07-25, deadline 2026-08-15) — reader-side cutover to typed `parent_state_typed` column.

### Wave G — Backfill + Cutover forward-pointers (deadline TBD)

- **PR-VO-BACKFILL** (deadline TBD) — one-shot backfill CLI migrates existing rows from JSON key → typed column.
- **PR-VO-CUTOVER** (deadline TBD) — writers stop writing JSON key; readers prefer typed column; JSON key retired.

---

## §4 — Reordering hazard (godlike/07 minimum-blast-radius)

The 4 wave-tracker entries that depend on dual-write SQL MUST be ordered strictly:

```
PR-P1.2-SQL-DUAL-WRITE
    ↓ (typed column write coexists with JSON key write, EXPAND phase)
PR-VO-PARENT-STATE-COLUMN
    ↓ (typed column becomes the canonical reader input)
PR-parent-state-cutover
    ↓ (CUTOVER phase: readers prefer typed column)
PR-VO-BACKFILL
    ↓ (one-shot backfill: existing rows migrated)
PR-VO-CUTOVER
    ↓ (CONTRACT phase: JSON key physically retired)
```

Any reordering breaks godlike/07 typed-error contract — readers that consume `resultMap["parent_state"]` would see empty values post-typed-cutover without the BACKFILL phase running first. The wave-tracker slim-schema ratchet enforces `status: pending → status: in_progress → status: shipped` ordering; flips that skip the intermediate phases (e.g. `pending → shipped`) are HARD-FAIL per godlike/06 SSOT.

---

## §5 — Verification gates (per per-PR, godlike/07 minimum-blast-radius)

Each per-PR lands with:

| Gate | Command | Expected |
|------|---------|----------|
| gofmt | `gofmt -l <touched_files>` | empty stdout |
| vet | `go vet ./internal/application/voiceover/...` | exit 0 (modulo 6-item carry-forward) |
| build | `go build ./internal/application/voiceover/...` | exit 0 |
| test | `go test -short -count=1 ./<subtree>/...` | exit 0 (targeted subtree) |
| canonicality | `git branch -r --contains <ship_sha>` | returns `origin/main` |
| YAML parse | `python3 -c "import yaml; yaml.safe_load(open('architecture/current.yaml'))"` | exit 0 |

**Pre-existing carry-forward UNCHANGED** (per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`):

| ID | Owner | Status |
|----|-------|--------|
| FIX-MONITOR-ENQUEUE-TOLOWER | monitor | shipped (2026-06-30) |
| FIX-MONITOR-SCHEDULER-ENQUEUER | monitor | shipped (2026-07-03) |
| FIX-STOCKPIPELINE-REDECLARATION | stock | shipped (2026-07-04) |
| FIX-APP-MODULE-MEDIA-DISPATCHER | app | shipped (2026-07-04) |
| FIX-IMAGES-ROUTING-CYCLE | images | shipped (2026-07-04) |
| FIX-APP-WIRE-SCRIPT-SYNTAX | app | retired (2026-07-04) |
| FIX-APP-WORKERRUNTIME-SYNTAX | app/workerruntime | shipped (2026-07-04) |

NOT regressions of any per-PR in this wave.

---

## §6 — Honest scope-lock (godlike/07 NO-FAKE-AVAILABILITY + TRANSPARENCY)

1. **Audit uncertainty**: PR-VO-TTS-PERSISTENT-WORKER status is reported as "shippped probabilmente pre-session" in the user-pasted text but shows `status: pending` in the wave-tracker snapshot. Wave D §3-Order #5 explicitly verifies this via `git log --grep` before any closure decision.
2. **Cross-capability decision surface**: PR-VO-ASSET-LOCATIONS-CONSUMER-AUDIT requires deciding Strada A vs Strada B (voiceover finalizer extension vs SceneRenderer fallback). The decision is not unilateral — Wave F represents the decision surface; the executor should open a sibling wave-tracker entry if cross-capability consensus is needed.
3. **Wave-tracker is dynamic**: closures land incrementally on main; this audit is a snapshot at 2026-07-05 (`CHANGELOG.md ## Unreleased > ### Documentation` closure meta-entry timestamp).
4. **Migration sequence is locked**: typed `parent_state_typed` column lifecycle is `migration-129 → dual-write SQL → typed column activation → reader cutover → backfill → JSON key retire`. Out-of-order transitions break godlike/07 typed-error contract.
5. **Per-PR ship-quality discipline (godlike/06 3-surface SSOT)**: each per-PR closure mirrors the godlike/06 pattern (CHANGELOG entry + AGENTS.md mirror + wave-tracker flip + archive comment) — the slim-schema ratchet on `status:` enforces `pending → in_progress → shipped`.
6. **No "fake availability"**: every PR-ID in §1 + §2 + §3 has either (a) a verifiable audit-pin (status + ship_sha + ship_date + ship_via) OR (b) a clear `status: pending` + deadline (NOT a fake-success signal). The `VO-DECOMPOSITION-HOTSPOT-CROSSREF` post-wave audit (§3 Wave E) is the loop closure that prevents high-frequency hotspots from leaking past the planning phase.

---

## §7 — Cross-references (godlike/06 SSOT 3-surface lockstep)

| Surface | Anchor | Role |
|---------|--------|------|
| `architecture/current.yaml` | `VO-DECOMPOSITION-2026-07-04` | Canonical wave-tracker (status in_progress, 8 linked_issues) |
| `architecture/current.yaml` | `PRE-EXISTING-BUILD-ISSUES-2026-07-04` | 6-item carry-forward (NOT regressions) |
| `architecture/current.yaml` | `PR-VO-COMPLETEPATH-FIX` | Shippped parent entry (`db2f3b1e` + child entries) |
| `architecture/current.yaml` | `PR-VO-SUBFOLDER` | Shippped parent entry (`c03e7cb0` + child entries) |
| `architecture/action-plans/2026-07-04-voiceover-decomposition.md` | pre-existing plan | Predecessor narrative (this file is the post-wave delta) |
| `architecture/ownership/modules.yaml` | voiceover capability | Canonical owner identity |
| `CHANGELOG.md` | `## Unreleased > ### Documentation` | Closure meta-entry (this commit) |
| `AGENTS.md` | `## Recent cross-cutting closures` | Mirror entry (this commit) |

---

## §8 — Per-PR execution checklist (godlike/07 minimum-blast-radius template)

Copy-paste preamble for each per-PR that lands on main:

```
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit -m 'fix(voiceover): PR-VO-<ID> — <scope>

<body, including godlike/06 SSOT mirror patterns +
  godlike/07 typed-error contract +
  per-id verification gates + carry-forward preservation>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'

git fetch origin         # race-protection (AGENTS.md Git-Lesson-4/5)
git push origin main     # direct-to-main; no --force
```

**Race-handled push** (per AGENTS.md Git-Lesson-2/4/5):
- `git log --oneline HEAD..@{u}` empty → `git push origin main` fast-forwards.
- `git log --oneline HEAD..@{u}` non-empty + same subject → byte-equivalent-replay recovery (accept canonical SHA).
- `git log --oneline HEAD..@{u}` non-empty + conflicting content → rebase + manual merge inspection (Rebase-Conflict Lesson).

---

## §9 — Action plan signature (godlike/06 SSOT)

This action plan is the canonical single-source-of-truth for the voiceover-completion wave's per-PR execution strategy. Any agent encountering the wave-tracker entry `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04` with `status: in_progress` should consult this plan first; any agent closing a per-PR entry should append ship evidence to this file (NOT create a parallel plan).

**Plan owner:** voiceover capability (`internal/application/voiceover` + `internal/application/voiceover/jobs` + `internal/infrastructure/audio`).
**Plan status:** SHIPPED as documentation-only artifact (this commit).
**Plan revision:** v1 (2026-07-05). Revisions append to §6 history.
