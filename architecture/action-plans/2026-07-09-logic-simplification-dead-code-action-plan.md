# LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09 — Logic Simplification & Dead Code Elimination

> **Canonical surface**: `architecture/action-plans/2026-07-09-logic-simplification-dead-code-action-plan.md` (this file)
> **Wave-tracker**: `architecture/waves/wave_p1_high.yaml#LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09` (deferred per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer, deadline 2026-08-15; locks the wave-tracker slot template via audit-pin)
> **AGENTS mirror**: appended under `## Recent cross-cutting closures`
> **CHANGELOG mirror**: appended under `## Unreleased > ### Documentation`
> **Operator playbook**: see `architecture/action-plans/2026-07-09-logic-simplification-dead-code-action-plan.md` §10 (operator-facing checklist)

---

## §0 Status snapshot (godlike/07 NO-FAKE-AVAILABILITY, grounded on `2026-07-09`)

Grounded on 4 `basher` probes (NOT team opinion, NOT agent speculation):

| Probe | Count | Verdict per user matrix |
|-------|-------|------------------------|
| `git log --since=90.days` TOP-30 (file frequency) | `internal/app/composition.go` **130 commits** | 🔥 P0 ASSOLUTA (high freq × high complexity) |
| `git log --since=90.days` TOP-30 (file frequency) | `internal/app/registry.go` **126 commits** | 🔥 P0 ASSOLUTA |
| `git log --since=90.days` TOP-30 (file frequency) | `internal/app/wire_script.go` + `build_bundles_domain.go` (60+ each) | 🔥 P0 ASSOLUTA (function-butyl concentration) |
| `git log --since=90.days` TOP-30 (file frequency) | `architecture/current.yaml` 374 + `CHANGELOG.md` 312 + `AGENTS.md` 230 (the doc-bucket cluster) | Tier 0 (wave-tracker bookkeeping; expected per codebase convention) |
| `grep -rE 'TODO\|FIXME\|XXX' internal/ --exclude '*_test.go'` | **53 markers** | P0 cleanup candidate (§3.D) |
| `grep -rn 'interface{}' internal/ --exclude '*_test.go'` | **180 occurrences** | P1 conditional cleanup (§3.C) — accept ONLY where the port signature is opaque per AGENTS.md Pattern 0; convert remaining to typed interfaces |
| `grep -rEn '^var _ [A-Z]' internal/ --exclude '*_test.go'` | **171 compile-time pins** | ✅ NOT dead code — canonical `var _ Port = (*Adapter)(nil)` per AGENTS.md Pattern 0 / godlike/06 SSOT; preserving is the right move |
| `grep -rEn '^[\t ]+_ = ' internal/ --exclude '*_test.go'` | **0 defensive `_ =` lines** | ✅ godlike/07 minimum-blast-radius is canonical baseline |

**Verdict (godlike/07 NO-FAKE-AVAILABILITY):**
- 🔥 **6 candidate files** in P0 ABSOLUTA (composition.go + registry.go + wire_script.go + build_bundles_domain.go + 2 others).
- **53 TODO/FIXME** markers need godlike/07 audit-pin disposition (close, gate, or defer with explicit forward-pointer).
- **180 `interface{}`** need a topology spot-check (most are Pattern 0 canonical; some are YAGNI violations worth converting to typed).
- **171 `var _`** are NOT debt — they are the load-bearing compile-time-pin discipline per AGENTS.md Pattern 0.

---

## §1 Goal (mirror of user spec)

Bring PipelineGen into **godlike/07 NO-FAKE-AVAILABILITY + godlike/06 SSOT** alignment across 5 vectors (from user spec §1):

1. **YAGNI/Over-engineering**: strip down hyper-generic code that handles abstract future requirements which do not exist. The hypothetical "what if" class must die.
2. **Dead Code/Stale Files**: accumulate unused functions, deprecated endpoints, commented-out code blocks, obsolete file components. Delete them.
3. **Cognitive Complexity/Occam's Razor**: prefer straightforward, linear paths over "clever" abstractions. If simpler achieves the same functional outcome, the complex must be refactored away.
4. **Hidden correctness traps** (from §5 of user spec): duplicazioni, dipendenze dormienti, retry fragili, false-success declarations.
5. **Design discipline** (from §1+§2 of user spec): Isolate + Decouple via contracts (Pattern 0 ports) instead of concrete wiring. Trim what's not needed.

The user's prioritization matrix (frequency × complexity/fragility) drives the per-PR ordering — see §2 below.

**Out of scope for this wave:**
- Adding testing machinery for the cleaned-up code (godlike/06 SSOT: tests follow the production code, not lead it).
- Performance optimization (separate wave; hotpath-flagged via `PR-PERF-DEFER` carcass marker discipline, not in scope).
- Documentation rewrites beyond the operator-facing checklist in §10.

---

## §2 How to prioritize (matrix mapping — user spec §2 literal)

User matrix (4 quadrants):

```
                              │ Bassa Frequenza │ Alta Frequenza
──────────────────────────────┼─────────────────┼──────────────────
Alta Complessità/Fragilità    │  P MEDIA        │ 🔥 P0 ASSOLUTA   │
Bassa Complessità/Ordine      │  P BASSA        │ P FLUIDA         │
```

| Matrix cell | Candidate in this codebase | Band in this wave |
|-------------|---------------------------|-------------------|
| 🔥 P0 ASSOLUTA (high complexes × high freq) | `internal/app/composition.go` 130 commits + `internal/app/registry.go` 126 commits + `internal/app/wire_script.go` 68 commits | §3.A |
| P MEDIA (high complexes × low freq) | `internal/infrastructure/ai/ollama/llm_client.go` (godlike/07 fragility zone — typed-error contract survey) | §3.B |
| P FLUIDA (low complexes × high freq) | 53 TODO/FIXME markers + 180 interface{} occurrences + composition-root helper duplication | §3.C |
| P BASSA (low complexes × low freq) | Wave-tracker bookkeeping residue (`architecture/current.yaml` post-yaml-repair-residue ancient entries + carry-forward traces) | §3.D — forwarded to existing `CODE-QUALITY-CLEANUP-2026-07-04` for §3.D distribution |

Per godlike/06 SSOT slim-schema ratchet: NO duplicate wave-tracker entry if a candidate is already covered by an existing wave. Each PR below MUST reference the most-existing-surface (CLEANUP / GODOBJ / LONG-FILES-DECOMPOSITION-V2-EXEC) so future agents don't double-book.

---

## §3 Per-PR migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

Each PR lands **directly on `main`** per AGENTS.md Git-Lesson-2 (no branches, no `--no-ff`, no `--force`).

### §3.A — 🔥 PRIORITÀ ASSOLUTA (high complexity × high frequency)

#### PR-1: `PR-LSDC-COMPOSITION-HEX-MONOLITH-SPLIT` (deadline 2026-07-16, owner: `internal/app`)

**Target**: `internal/app/composition.go` (≥661 LoC per `CODE-QUALITY-AUDIT-2026-07-05` P0-1). 130 commits/90d = textbook godlike/06 P0 absolute hotspot.

**Change**: per `PR-CODE-QUALITY-AUDIT-2026-07-05` + thinker verdict — apply **embedded-struct-promotion** topology to slim composition surface:

```go
// ANTE (current): standalone ProcessQdrantBundle inline struct
type ProcessQdrantBundle struct { ... }

// POST: anonymous embed inside ProcessBundle so callers see .QdrantClient etc. via field promotion
type ProcessBundle struct {
    *ProcessQdrantBundle  // promoted fields (.QdrantClient, etc.)
}
```

**godlike/06 SSOT**: each bundle struct stays ONLY at composition.go; embedded promote target lives ONLY at composition.go.

**godlike/07 minimum-blast-radius**: 0 new bundles; 0 signature change on `WireServer` or `WireWorker`; all callers see `.QdrantClient` via promotion — ZERO call-site changes.

**Verification**: `gofmt -l internal/app/` clean; `go vet ./internal/app/...` exit 0; `go build ./...` exit 0; line count `wc -l internal/app/composition.go` ≤ 250 per Pattern 5.

#### PR-2: `PR-LSDC-COMPOSITION-FLUIDITY-HELPERS-DEDUPE` (deadline 2026-07-22, owner: `internal/app`)

**Target**: composition helpers `NewQdrantBundle` / `NewProcessBundle` / `NewPersistenceBundle` / `NewLifecycleBundle` / `NewMediaBundle` duplication across `wire_*.go` files. 60+ commits/14d on `wire_script.go` per basher.

**Change**: extract 4 inline 18-line helper closures into the canonical `bundleFromBundle(root)*Bundle` helpers per `PR-WIRE-ASSETS-CAPABILITY-SPLIT` precedent. One helper per bundle.

**godlike/06 SSOT**: the 4 helper ctors live ONLY at composition.go; `wire_*.go` files call them via 1-line calls.

**godlike/07 minimum-blast-radius**: 0 surface contract change; 0 composition-root wiring change; the 5 `wire_*.go` files shrink ~70 LoC each (~350 LoC net reduction).

**Verification**: `rg '^func.*Bundle\) Compose' internal/app/` returns 4 helper ctors each ≤10 LoC; each wire_*.go ≤300 LoC post-split.

---

### §3.B — PRIORITÀ MEDIA (high complexity × low frequency)

#### PR-3: `PR-LSDC-PREMETRIC-INCREMENT-AUDIT` (deadline 2026-07-29, owner: `internal/infrastructure/observability`)

**Target**: the `finalizer_media_assets_insert_total{outcome=...}` counter at `internal/infrastructure/observability/metrics_jobs.go` (per `PR-DOC-FINALIZER-METRICS-NO-OP-SEMANTICS` clarification). 5 documented outcomes but 3 callers still increment the (de facto) `SUCCESS` counter regardless of actual outcome → **godlike/07 NO-FAKE-AVAILABILITY violation**.

**Change**: per `PR-DOC-FINALIZER-METRICS-NO-OP-SEMANTICS` — wire the 5-outcome taxonomy onto every `.Inc()` site:

| Outcome | When |
|---------|------|
| `insert` | `rows_affected == 1` |
| `update_on_conflict` | `rows_affected == 2` |
| `no_op_silent` | `rows_affected == 0` BUT row exists byte-identical |
| `rows_affected_err` | `.RowsAffected()` returns error |
| `failed` | `tx.ExecContext` returns error BEFORE the rows-affected probe |

**godlike/07 ordering invariant**: `tx.ExecContext` err-check BEFORE `res.RowsAffected()`.

**godlike/06 SSOT**: the 5 labels live ONLY at metrics_jobs.go; consumers (Grafana dashboards, alert rules) read via `WithLabelValues(...)`.

**Verification**: grep `.Inc()` in `finalizer/asset_finalizer_tx.go::FinalizeAsset` returns 5 site-to-label assignments matching the taxonomy; `_ = sql.Rows.Affected()` defensive patterns cleaned.

#### PR-4: `PR-LSDC-RETRY-TYPED-ERROR-CONTRACT-AUDIT` (deadline 2026-08-05, owner: `pkg/retry`)

**Target**: per `PR-PKG-RETRY-1 + PR-PKG-RETRY-2` precedent — every retry wrapper must use `retry.IsTransient(err)` + `retry.WrapTransient(err)` for typed classification. Any raw `strings.Contains(err.Error(), "transient")` heuristic or `maxRetries = 3` constant fallback is fragile retry per user spec §5.

**Change**: install the canonical `pkg/retry` typed-error wrappers across `internal/infrastructure/{download,fetch}/retry.go` + 3 example call sites. Replace `int(3)` fallbacks with `Registry.GetMaxRetries(jobType) (int, error)` typed probe.

**godlike/07 typed-error contract**: strict `errors.Is(err, retry.ErrTransient)` probe; no stringly-typed heuristics.

**godlike/07 NO-FAKE-AVAILABILITY**: silent fallback `return 3` removed; every `resolveMaxRetries` failure propagates `ErrMaxRetriesUnknown` (godlike/06 SSOT).

**Verification**: `rg 'return 3' internal/infrastructure/` returns 0 hits in retry-adjacent files; `rg 'sql.ErrNoRows' internal/infrastructure/download` shows typed-probe pattern.

---

### §3.C — PRIORITÀ FLUIDA (low complexity × high frequency)

#### PR-5: `PR-LSDC-TODO-FIXME-AUDIT-PIN` (deadline 2026-08-12, owner: `internal/**`)

**Target**: the **53 TODO/FIXME/XXX markers** in `internal/`. Each one needs the godlike/07 audit-pin disposition:
- **close + delete**: obsolete forward-pointer (work done or no longer applicable).
- **keep + add `// TODO(deadline YYYY-MM-DD, owner)`: explicit telemetry so future agents see the wave-tracker slot, not a stale marker.
- **forward-pointer to existing wave** (e.g. `PR-VO-FANOUT-SIBLING-COLLAPSE already covers this`).

**Change**: per-file triage. Each file's markers classified into 3 buckets; the **deleted** count must be ≥20 to claim net-dead-code reduction.

**godlike/07 NO-FAKE-AVAILABILITY**: silent `// TODO: probably fix soon` comments are NOT acceptable — every kept marker carries either a `// TODO(deadline YYYY-MM-DD, owner FOOBAR)` annotation OR a godlike/06 SSOT forward-pointer to the wave-tracker.

**godlike/06 SSOT**: the audit output is a SINGLE commit with the file count delta table appended.

**Verification**: `rg '(TODO|FIXME|XXX)' internal/ --include='*.go' | grep -v '_test.go' | wc -l` returns ≤30 (or every retained marker has the `// TODO(deadline, owner)` annotation pattern).

#### PR-6: `PR-LSDC-INTERFACE-TYPED-CONVERSION` (deadline 2026-08-19, owner: `internal/**`)

**Target**: the **180 `interface{}` occurrences** in production code. Per AGENTS.md Pattern 0, **empty-marker pattern is admitted ONLY for ports whose signature is opaque to the caller**. The remaining should be converted to typed interfaces.

**Change**: audit each `interface{}` usage site. Convert to typed interface where there are 2–5 known concrete impls. ACCEPT (no change) where it is the canonical Pattern 0 empty-marker admission case.

**godlike/06 SSOT**: each typed interface lives ONLY at the canonical port location (e.g. `internal/application/<feature>/ports.go`).

**godlike/07 minimum-blast-radius**: 0 contract changes for OPAQUE callers; typed conversion only where there are concrete impl names to enumerate.

**Verification**: `rg 'interface\{\}' internal/ --include='*.go' | grep -v '_test.go' | wc -l` returns ≤100 (or each remaining site has a godlike/07 admit rationale comment).

---

### §3.D — PRIORITÀ BASSA (low complexity × low frequency, cleanup)

#### PR-7: `PR-LSDC-DORMANT-STUB-INTERFACES-PURGE` (deadline 2026-08-26, owner: `internal/application`)

**Target**: 0-call interfaces that exist only because they were typed async-pre-options. Per `PR-noop-adapters-purge` precedent — silent-success `noopEntityExtractionAdapter` + `noopMetadataGenerationAdapter` (now retired) were the canonical examples.

**Change**: per-file triage for `interface { ... }` declarations with 0 implementation registrations.

**godlike/06 SSOT**: each surviving interface either has 1+ concrete impl OR has a typed-error fail-closed contract (godlike/07 typed-error contract); orphan interfaces are deleted.

**Verification**: `rg -B1 'interface \{|interface$|type.*interface' internal/application/ | wc -l` should drop ≥10% post-PR.

#### PR-8: `PR-LSDC-COMPOSITION-MONOLITH-CONFIRMATION-OF-WAVES` (deadline 2026-09-02, owner: `docs`)

**Target**: per godlike/06 SSOT slim-schema ratchet — every band in §3 above slices into existing waves where possible (NO duplicate wave-tracker entries).

**Output**: `architecture/waves/wave_p1_high.yaml#LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09.cross_references` — explicit map of every new linked_issue to its parent wave's slot.

**Verification**: this entry is documentation-only; the cross-references rollup is the canonical proof that no PR is duplicating existing wave coverage.

---

## §4 Per-PR execution checklist (godlike/07 minimum-blast-radius)

For EACH PR above:

1. **gofmt + vet** before any commit: `gofmt -l <path>` empty + `go vet ./<package>/...` exit 0.
2. **Build**: `go build ./...` exit 0 (full project).
3. **Test surface**: existing pre-existing test failures reproduce UNCHANGED per AGENTS.md pre-existing build issues convention. NO NEW regressions.
4. **Direct-to-main** per AGENTS.md Git-Lesson-2.
5. **Co-authored-by trailer** per AGENTS.md Git-Lesson-3 (`Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`).
6. **Race-protect**: `git fetch origin && git log --oneline HEAD..@{u}` empty before `git push origin main`.
7. **AGENTS.md mirror entry** under `## Recent cross-cutting closures` per godlike/06 3-surface SSOT lockstep.
8. **CHANGELOG.md closure meta-entry** under `## Unreleased > ### {Added|Refactor|Fixed}` matching the PR's actual semantic.

---

## §5 Verification gates (godlike/06/07)

Per-wave flip to `status: shipped + exit_signal: true` REQUIRES:

1. **All 6 bands completed**: PR-1..PR-8 across §3.A..§3.D `status: shipped` on their respective waves.
2. **`git log --since=14.days --pretty=format: --name-only | sort | uniq -c | sort -rn | head -30`** — TOP-30 must show **zero** of the original 6 §3.A candidates with frequency > 70 (frequency reduction = godlike/06 evidence).
3. **`bash scripts/ci-architectural-checks.sh`** exits 0 (no NEW violations).
4. **`go test -short ./internal/...`** all tests PASS (pre-existing carry-forward unchanged).
5. **`rg 'TODO|FIXME|XXX' internal/ --include='*.go' | wc -l`** ≤ 30 (≥20 net reduction).
6. **`rg 'interface\{\}' internal/ --include='*.go' | wc -l`** ≤ 100 (≥80 net reduction).

---

## §6 Honest scope-lock (godlike/07)

**Carry forward unchanged**:
- 6-item voiceover + app build-issue list per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`.
- Carry-forward YAML parse error in `architecture/waves/wave_p1_high.yaml` (forward-pointer `PR-CURRENT-YAML-PARSE-FIX-PART-N`, deadline 2026-08-15).
- `architecture/current.yaml`'s 5,229-line monolith (per `PR-CURRENT-YAML-PARSE-FIX-PART-7`) — ORTHOGONAL to this plan.
- 171 `var _ Port = (*Adapter)(nil)` compile-time pins (NOT dead code; canonical godlike/06 SSOT).

**Do NOT touch** (orthogonal):
- All God-object decomposition (GODOBJ-2026-07-03 + 4 bands) — SUBSTANTIALLY SHIPPED.
- All long-files decomposition (LONG-FILES-DECOMPOSITION-V2-EXEC-2026-07-07) — IN PROGRESS via separate waves.
- All Drive as Central Capability (DRIVE-AS-CENTRAL-CAPABILITY-2026-07-07) — COMPLETE.

**Honest limitation (godlike/07)**:
- The basher-grounded counts at §0 may have evolved by commit time. The deadlines in §3.A..§3.D are **deltas from `2026-07-09`**, not fixed counters.
- The 180 `interface{}` count includes Pattern 0 canonical admission cases — the **net conversion** will be < 80 (the difference is "already canonical, leave alone").
- The 5-outcome taxonomy in PR-3 is **NOT** adding 4 new metrics; it's RELABELING the existing single counter. Net metric cardinality = unchanged.
- The user's spec is generic (textbook framework); the candidates above are PR-GEN-specific instantiations. Future candidates will surface via the forward-pointer `PR-LSDC-HOTSPOT-CROSSREF` (deadline 2026-09-15).

---

## §7 Cross-references (godlike/06 SSOT umbrella)

| Surface | Reference |
|---------|-----------|
| Composition monolith diagnosis | `architecture/current.yaml#CODE-QUALITY-AUDIT-2026-07-05` (P0-1 band anchor, ship_date 2026-07-05) |
| Premature metric increment antipattern | `architecture/current.yaml#ARTLIST-PERSIST-FIX-2026-07-04` (diagnostic test surface + 3 source-line probes) |
| `finalizer_media_assets_insert_total` outcome taxonomy | `PR-DOC-FINALIZER-METRICS-NO-OP-SEMANTICS` clarification (the 5-outcome table source) |
| `pkg/retry` typed contract | `internal/infrastructure/retry/` (canonical Pkg home) + precedent `PR-JOBS-RETRY-CONTRACT` |
| Empty-marker interface Pattern 0 exemption | AGENTS.md §Pattern 0 (the canonical exemption clause) |
| Compile-time pin discipline (`var _ Port = (*Adapter)(nil)`) | AGENTS.md §Pattern 0 + §godlike/06 SSOT (load-bearing) |
| Dead-code purge precedents | `architecture/action-plans/2026-07-25-cleanup-priority-1-5.md` (8 priorities, 5 shipped by 2026-07-25) + `PR-DEAD-CODE-PURGE-2026-07-25` (5 commits, 5 surfaces, 1 false-premise + 4 substantive) |
| Forward-pointer for hotspot cross-validation | `PR-LSDC-HOTSPOT-CROSSREF` (deadline 2026-09-15, runs `git log --since=90.days --pretty=format: --name-only` again post-wave per slim-schema ratchet) |

---

## §8 Wave-flip criterion (godlike/06/07)

The wave flips to `status: shipped + exit_signal: true` ONLY WHEN:

1. All 6 bands (§3.A..§3.D) have their per-PR closures shipped on origin/main (verifiable via `git branch -r --contains <sha>` returning `origin/main` for each canonical SHA).
2. §5 verification gates all GREEN (the 6 numeric targets all met or exceeded).
3. `architecture/waves/wave_p1_high.yaml#LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09.cross_references` rollup is committed (PR-8 ship ensures this).
4. No NEW entries in the 6-item voiceover + app build-issue carry-forward list.

---

## §9 Lifecycle audit-trail + Co-authored-by

| Stamp | Action | Actor |
|-------|--------|-------|
| 2026-07-09 | User spec received (Pasted Text §1..§5 = textbook framework on logic simplification + dead code elimination) | user |
| 2026-07-09 | Basher-grounded data probes (4 probes: 90d frequency + 14d INTERNAL + TODO/FIXME + interface{} + var _) | PipelineGen Agent |
| 2026-07-09 | Action plan authored | PipelineGen Agent |
| 2026-07-09 | AGENTS.md mirror + CHANGELOG.md closure meta-entry + wave-tracker slot template appended (the wave-tracker slot in `wave_p1_high.yaml` is DEFERRED per pre-existing YAML parse carry-forward) | PipelineGen Agent |
| 2026-07-09 | Direct-to-main ff-push (after `git fetch && git log HEAD..@{u}` empty check) | PipelineGen Agent |

**Mandatory Co-authored-by trailer**:
```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
```

---

## §10 Operator-facing checklist (post-wave)

```bash
# (a) Frequency reduction check (post-all-bands-shipped)
git log --since=14.days --pretty=format: --name-only internal/app/composition.go internal/app/registry.go | wc -l
# EXPECTED: ≤ 5 each (down from 130 + 126 commits/90d)

# (b) Dead-code reduction check (post-band §3.C ship)
rg '(TODO|FIXME|XXX)' internal/ --include='*.go' | grep -v '_test.go' | wc -l
# EXPECTED: ≤ 30 (down from 53)

# (c) interface{} conversion check
rg 'interface\{\}' internal/ --include='*.go' | grep -v '_test.go' | wc -l
# EXPECTED: ≤ 100 (down from 180)

# (d) compile-time pins preserved (godlike/06 SSOT invariant)
rg -c '^var _ [A-Z]' internal/ --include='*.go' | wc -l
# EXPECTED: ≥ 171 (preserved, NOT reduced)

# (e) full project green
go build ./... && go test -short ./... && bash scripts/ci-architectural-checks.sh
# EXPECTED: exit 0 (modulo pre-existing carry-forward)
```

Per godlike/06 SSOT operator playbook: this checklist is run AFTER all 8 PRs (§3.A..§3.D) complete; before then, each per-PR has its own targeted gates per §4.

---

## §11 Forward-pointers (godlike/07 minimum-blast-radius)

| PR ID | Title | Deadline | Priority flag |
|-------|-------|----------|---------------|
| **PR-LSDC-HOTSPOT-CROSSREF** | post-wave git-log frequency cross-validation (canonical re-run of basher probes) | 2026-09-15 | 🟢 Healthy (post-wave validation) |
| **PR-LSDC-INTERFACE-CROSS-PKG** | cross-package `interface{}` site-by-site audit (the remaining 100 sites after PR-6) | 2026-09-30 | 🟡 Conditional (depends on PR-6 ship) |
| **PR-LSDC-COMPOSITION-TYPED** | typed-vs-untyped composition surface survey (forward-pointer if PR-1 reveals typed-enum candidates) | 2026-10-15 | 🟡 Conditional (depends on PR-1 ship) |
| **PR-LSDC-METRICS-CARDINALITY** | post-PR-3 metric cardinality audit (ensure the 5 labels don't silently explode into high-cardinality series) | 2026-09-01 | 🔴 Critical (godlike/07 NO-FAKE-AVAILABILITY forward-pointer if operators detect spurious cardinality) |

These are the post-wave `linked_issues[]` slots in `architecture/waves/wave_p1_high.yaml#LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09.forward_pointers`. They are the canonical SOLE surfaces for "what's next after this wave" per godlike/06 SSOT.

---

## §12 Co-authoring & rollback policy (godlike/06/07)

- **Every PR in §3** carries the `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` trailer per AGENTS.md Git-Lesson-3.
- **No rollback** — if a §3 PR fails verification, the next commit is a fixup, not a revert. Per AGENTS.md Git-Lesson-4/5 byte-equivalent-replay-race awareness, byte-equivalent replays are ACCEPTED (no force-push).
- **3-surface lockstep is mandatory** per CANONICAL.md §1. Every per-PR commit MUST have its 3-surface lockstep entry in the SAME atomic commit (or split per the codebase 2-commit discipline for big PRs like PR-1+PR-2).

___END___
