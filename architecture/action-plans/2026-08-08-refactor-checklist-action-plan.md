# Refactoring Checklist — Action Plan (2026-08-08)

> **Source:** User-pasted Italian refactoring checklist, 4 macro-areas (Architectural Fragility & Coupling, Code Complexity & Maintainability, Performance & Resource Management, Logic Simplification & Dead Code Elimination) + 12 code smells + Impact × Frequency priority matrix.
> **Status (2026-08-08):** Phase 0 baseline planned. Execution **directly on `main`** per AGENTS.md Git-Lesson-2 (no branches, no `--no-ff`, no `--force`); each per-PR lands atomically with `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>.` trailer per AGENTS.md Git-Lesson-3.
> **Owner capability:** `architecture` (plan owner) + per-PR `owner_capability` field.

---

## §0 Honest Scope-Lock (godlike/07 NO-FAKE-AVAILABILITY)

- **Static analysis (verifiable NOW, hermetic):** cyclic deps (`go list -deps -f '{{.ImportCycle}}'` + `rg` patterns), dead code (`rg 'func (\w+)', then go test coverage`), primitive obsession (rg for `string`/`int` standing in for domain concepts), mutable globals (rg for `^var .* = ` at package level outside test files), cyclomatic complexity (`gocyclo -over 25 ./internal/...` if available, else `gocloc`).
- **Live server / E2E (requires runtime):** hidden allocations in hot loops (pprof on load test), sync blocking I/O (tracing under load), O(n) lookups in hot paths (benchmarks before/after).
- **Human review (architectural decision):** over-engineering / YAGNI judgment calls, I/O abstraction boundaries (feature envy vs co-located logic), primitive obsession → typed-enum trade-offs.

The plan is **static-priority-by-complexity**, NOT git-log-frequency. The forward-pointer `PR-REFACTOR-WAVE-4-HOTSPOT-CROSSREF` (deadline 2026-09-15) cross-validates via `git log --since=90.days --pretty=format: --name-only | sort | uniq -c | sort -rn | head -30` per the established codebase pattern (`PR-CLEANUP-HOTSPOT-CROSSREF` + `PR-P12-HOTSPOT-CROSSREF` + `PR-GODOBJ-HOTSPOT-CROSSREF`).

---

## §1 YAML Wave Tracker Ratchet (canonical SOLE owner of status)

**Slim-shape per godlike/06 SSOT (one-canonical-owner-per-fact).** Append verbatim to `architecture/waves/wave_p1_high.yaml` once `PR-CURRENT-YAML-PARSE-FIX-PART-N` (forward-pointer, deadline 2026-09-01) lands — until then the action plan + CHANGELOG + AGENTS entries are the canonical closure record per the carry-forward convention (mirrors `PR-CHECK-5-FOLLOWUP` precedent).

```yaml
  - id: PR-REFACTOR-WAVE-4-2026-08-08
    status: pending
    exit_signal: false
    owner_capability: architecture
    deadline: 2026-08-30
    exit_gate: "All linked_issues status: shipped (directly on main, no branch). Per-PR verification gate: gofmt -l <file> clean + go vet ./<subtree>/... exit 0 + go build ./<subtree>/... exit 0 + go test -short -count=1 ./<subtree>/... PASS. godlike/07 NO-FAKE-AVAILABILITY: no claim of 'shipped' without a real commit on origin/main."
    linked_issues:
      # ── P0 absolute: high frequency + high fragility ────────────────
      - id: PR-REFACTOR-P0-CYCLIC-DEPS
        owner_capability: internal/application
        status: pending
        deadline: 2026-08-12
        description: "Resolve import cycles by extracting `ports.go` interfaces at package boundaries (canonical Pattern 0 per AGENTS.md Pattern 0). Verification: `go list -deps -f '{{.ImportCycle}}' ./internal/application/...` returns empty."
      - id: PR-REFACTOR-P0-IO-BINDER
        owner_capability: internal/infrastructure
        status: pending
        deadline: 2026-08-12
        description: "Isolate direct I/O (DB drivers, network, fs) in adapter packages. Expose to application layer ONLY via typed ports per godlike/06 SSOT. Verification: `rg 'os\\.(Open|Read|Write)|sql\\.Open|database/sql' internal/application/` returns zero hits outside test files."
      # ── P1 high: code complexity (maintainability) ───────────────────
      - id: PR-REFACTOR-P1-GLOBAL-STATE
        owner_capability: internal/api
        status: pending
        deadline: 2026-08-15
        description: "Replace package-level `var xxx = map[...]` mutable singletons with struct fields injected via ctor. Verification: `rg '^var [A-Z]\\w+ (map|\\[\\])' internal/` returns 0 hits outside test files + each touched package has -short test coverage."
      - id: PR-REFACTOR-P1-CYCLOMATIC
        owner_capability: internal/application/scripts/usecase
        status: pending
        deadline: 2026-08-15
        description: "Extract deeply nested if/switch into delegate tables or early returns. Target: top 5 files with `gocyclo -top 10`. Verification: `gocyclo -over 25 ./internal/application/scripts/usecase/...` returns empty (or counts drop ≥ 30%)."
      # ── P2 medium: performance + resource management ────────────────
      - id: PR-REFACTOR-P2-BLOCKING-IO
        owner_capability: internal/application/jobs
        status: pending
        deadline: 2026-08-20
        description: "Remove synchronous os.ReadFile / os.Open in hot paths; lift to eager-load at boot via injected I/O binder. Verification: `rg 'os\\.ReadFile|os\\.Open' internal/application/jobs/` audited + each call site has a benchmark showing improvement."
      # ── P3 low: dead code + YAGNI ─────────────────────────────────────
      - id: PR-REFACTOR-P3-DEAD-CODE
        owner_capability: internal
        status: pending
        deadline: 2026-08-25
        description: "Purge unused structs/functions and YAGNI branches. Verification: `rg -L 'func (\w+)'` audit + `go test -cover` does not drop on touched subtrees."
      - id: PR-REFACTOR-P3-PRIMITIVE-OBSESSION
        owner_capability: internal
        status: pending
        deadline: 2026-08-25
        description: "Replace stringly-typed step/job/category literals with `type X string` aliases or typed enums at hot call sites. Verification: spot-check top 5 call sites; `rg '\"[a-z_]+\"' internal/application/jobs/` count drops or each remaining literal is on the canonical const set."
    forward_pointers:
      - id: PR-REFACTOR-WAVE-4-HOTSPOT-CROSSREF
        deadline: 2026-09-15
        description: "Post-wave git-log frequency cross-validation per established codebase pattern."
```

---

## §2 4 Macro-Areas × Priority Matrix

| Macro-area | Code smell | Band | Why this band |
|------------|-----------|------|---------------|
| **1. Architectural Fragility & Coupling** | Cyclic deps | **P0** | Blocks isolated testing; high blast radius; hard to fix later without rewrite. |
| | Feature envy | P1 | Logic in wrong place; hot-spot candidate; affects onboarding. |
| | I/O binder missing | **P0** | Direct DB/network calls = swap-the-driver → breakage. |
| **2. Code Complexity & Maintainability** | High cyclomatic complexity | P1 | Latent bug factory; test coverage gaps. |
| | Shared mutable global state | P1 | Race condition factory in concurrent code. |
| | Primitive obsession | P3 | Correctness envelope risk; validation duplication. |
| **3. Performance & Resource Management** | Hidden allocations in loops | P2 | GC pressure; degrades under load. |
| | Sync blocking I/O on hot path | P2 | Latency spikes under concurrent load. |
| | Inefficient algorithms (O(n) vs O(1)) | P2 | Throughput ceiling. |
| **4. Logic Simplification & Dead Code Elimination** | Over-engineering / YAGNI | P3 | Cognitive load; future-evolution cost. |
| | Dead code & stale files | P3 | Build time; directory noise. |
| | Cognitive complexity vs Occam's razor | P3 | Onboarding + review cost. |

---

## §3 Per-Item Execution Checklist (the canonical "what to do")

For each `linked_issues[]` row in §1, the agent executing MUST:

1. Run a pre-flight `rg` / `go list` audit and report the current count (so the future agent can measure delta).
2. Make the minimal-surface change that achieves the goal (godlike/07 minimum-blast-radius: 0 new exported symbols, 0 signature drift, 0 composition-root wiring shifts).
3. Verify: `gofmt -l <file>` clean + `go vet ./<subtree>/...` exit 0 + `go build ./<subtree>/...` exit 0 + `go test -short -count=1 -timeout 90s ./<subtree>/...` PASS.
4. Commit **directly on main** per AGENTS.md Git-Lesson-2: `git -c user.email='agent@pipelinegen.local' -c user.name='PipelineGen Agent' commit -m '<subject>

<body>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.'`
5. `git fetch origin` + `git push origin main` (race-protect per AGENTS.md Git-Lesson-4: `git log --oneline HEAD..@{u}` MUST be empty pre-push).
6. Backfill `architecture/waves/wave_p1_high.yaml#PR-REFACTOR-WAVE-4-2026-08-08.linked_issues[<id>].{ship_sha, ship_date}` when the parse carry-forward (`PR-CURRENT-YAML-PARSE-FIX-PART-N`, deadline 2026-09-01) lands.

---

## §4 Verification Gates (canonical pattern per AGENTS.md Pattern 5)

```bash
# Per-PR (mandatory for each linked_issues row)
gofmt -l <file-or-glob>
go vet ./<target-subtree>/...
go build ./<target-subtree>/...
go test -short -count=1 -timeout 90s ./<target-subtree>/...

# Wave-flip aggregator (run when ALL linked_issues are shipped)
go run ./cmd/archcheck --strict              # promoted from report-only to gate (per PR-ARCHCHECK-GO-MIGRATION-PHASE-2)
bash scripts/ci-architectural-checks.sh        # forward-prevention gates (Checks 5/50/52/53/54/62)
go test -short -count=1 -timeout 180s ./...    # full project regression
```

---

## §5 Cross-References (godlike/06 SSOT umbrella)

- `architecture/waves/wave_p1_high.yaml#PR-REFACTOR-WAVE-4-2026-08-08` — canonical SOLE owner of the wave-tracker status (DEFERRED per `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer, deadline 2026-09-01).
- `architecture/waves/wave_p1_high.yaml#PR-REFACTOR-WAVE-3-2026-08-08` — sister wave (5 PR-SPLIT closures shipped 2026-07-06 → 2026-08-08; uses the same per-PR + wave-flip pattern this plan extends).
- `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — 6-item voiceover + app build-issue carry-forward UNCHANGED (NOT a regression of this plan).
- `architecture/waves/wave_p1_high.yaml#GODOBJ-2026-07-03` — god-object decomposition wave (P0 absolute + Mechanical + Cut-not-split + Small-but-dangerous bands). Some items in this plan (P0-CYCLIC-DEPS, P0-IO-BINDER) overlap with GODOBJ-2026-07-03 deadlines; the wave-tracker ratchet enforces "ship the older one first" per `PR-GODOBJ-HOTSPOT-CROSSREF` precedent.
- `architecture/waves/wave_p1_high.yaml#CODE-QUALITY-CLEANUP-2026-07-04` — sister wave (12 dirty areas, 4 priority bands, deadline 2026-09-15). P3 items here are complementary to P3 of that wave.
- `architecture/waves/wave_p1_high.yaml#CLEANUP-PRIORITY-1-5-2026-07-25` — wave-flip completed 2026-08-08; canonical "how to land a wave" precedent for the wave-flip criterion structure of this plan.
- `AGENTS.md` — Pattern 0 (port abstraction), Pattern 5 (file split), Pattern 7 (reuse services), Pattern 8 (thin transport only). godlike/06 SSOT (one canonical owner per fact), godlike/07 (NO-FAKE-AVAILABILITY + minimum-blast-radius + typed-error contract), godlike/08 (zero-baseline rule). Git-Lesson-2/3/4/5 for the commit + push + race-protect + byte-equivalent-replay-recovery discipline.

---

## §6 Forward-Pointers (godlike/07 honest scope-lock)

- **PR-REFACTOR-WAVE-4-HOTSPOT-CROSSREF** (deadline 2026-09-15) — post-wave git-log frequency cross-validation per the established codebase pattern.
- **PR-CURRENT-YAML-PARSE-FIX-PART-N** (deadline 2026-09-01) — the wave-tracker slot in §1 cannot be appended until the YAML parse carry-forward is resolved; the action plan + CHANGELOG + AGENTS entries are the canonical closure record in the interim (per `PR-CHECK-5-FOLLOWUP` precedent).
- **PR-REFACTOR-P0-FEATURE-ENVY-AUDIT** (deadline 2026-08-22) — separate audit-pr for the 3rd P0/1 row in §1; feature envy is harder to detect statically (needs human review) so it is a forward-pointer, not a direct linked_issue.
- **gocyclo availability** (operator-only) — if `gocyclo` is not installed locally, use `gocloc` or run `gocloc --by-file --output-type=json internal/ | jq '.[] | select(.code > 250)'` as a cyclomatic proxy (P1-CYCLOMATIC verification gate falls back to this).

---

## §7 Pre-existing carry-forward (NOT regressions of this plan)

- 6-item voiceover + app build-issue list per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — UNCHANGED.
- `architecture/waves/wave_p1_high.yaml` parse carry-forward per `PRE-EXISTING-YAML-PARSE-2026-07-04` (forward-pointer `PR-CURRENT-YAML-PARSE-FIX-PART-N`, deadline 2026-09-01) — UNCHANGED.
- Pre-existing test failures across stockpipeline + voiceover + jobs (carry-forward per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` convention) — UNCHANGED.

---

## §8 Co-authored-by trailer (mandatory on every per-PR commit)

```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
```

---

## §9 Lifecycle audit-trail

- 2026-08-08 — Plan landed as documentation-only commit on `main`. No code change in this commit. Wave-tracker slot DEFERRED per §6 forward-pointer.
- 2026-08-08 → 2026-08-30 — per-PR execution window (P0 → P1 → P2 → P3 bands per §1).
- 2026-09-15 — wave-flip aggregator criterion must be met (PR-REFACTOR-WAVE-4-HOTSPOT-CROSSREF cross-validation).
- Wave-flip: `status: shipped + exit_signal: true` ONLY when all 7 linked_issues are `status: shipped` AND the §4 verification gates are green AND the hotspot cross-validation surfaces zero NEW hotspots.
