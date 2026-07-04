# Phase 2 — Archcheck Go-Migration Retirement (PR-ARCHCHECK-GO-MIGRATION-PHASE-2)

**Deadline:** 2026-08-15
**Phase 1 ship_sha:** `67cbcb73` (2026-07-04, shipped)
**Owner capability:** `architecture/archcheck/` + `scripts/ci-architectural-checks.sh`
**Status:** planning (this document)
**Predecessor:** [`PR-ARCHCHECK-GO-MIGRATION-PHASE-1`](../../current.yaml#PR-ARCHCHECK-GO-MIGRATION-PHASE-1)

---

## Goal

Phase 1 (shipped 2026-07-04) shipped 3 Go scanners as a **transitional baseline** alongside
the original 3 shell checks (per `godlike/08` §"zero-baseline rule" — the final acceptable
count is zero; transitional baselines require an explicit owner + deadline). Phase 2 closes
the loop: **retire the 3 shell checks**, **tighten the ci-gate to fail-closed on the Go
scanner**, and **promote `cmd/archcheck` from report-only to gate-promoted** as the SOLE
canonical fail-closed surface for those 3 invariant classes.

---

## Background — what's already live (post-Phase 1)

### 3 Go scanners (live, fail-closed via `verify-main`)
- `cmd/archcheck/scan/percheck_typeredecl.go` (mirrors shell Check 5: same-package
  duplicate-type-declarations)
- `cmd/archcheck/scan/percheck_txcontext.go` (mirrors shell Check 53: P0 C7
  atomic-complete wire-method ban)
- `cmd/archcheck/scan/percheck_monitor.go` (mirrors shell Check 54: FASE 3.7
  Commit 3 monitor-infra-import ban)
- All 3 registered in `cmd/archcheck/runner.go::DefaultChecks()` alongside the
  12 pre-existing scanners.
- Each scanner has a `_test.go` companion.

### `--strict` gate (live, fail-closed)
- `cmd/archcheck/main.go` default = `--strict false` (report-only; preserved for
  local inspectability per `godlike/07` minimum-blast-radius).
- `Makefile` `verify-main` target chains `go run ./cmd/archcheck --strict` as
  the 4th step (after `go-version-check` → `verify-format` → `tidy-check`).
- `.github/workflows/ci.yml::verify-main` runs `make verify-main` — so the
  Go scanner is the **fail-closed surface in CI** today.

### Shared allowlist (live, single source of truth)
- `docs/architecture/godlike/duplicate-types-allowlist.txt` — 17 entries with
  2026-09-01 deadline. The Go scanner reads the SAME file the shell check reads
  (canonical SSOT per `godlike/06` §"one canonical owner per fact").

### The 3 shell checks (still live, RETIRE in Phase 2)
- `scripts/ci-architectural-checks.sh` Check 5 (~line 538) —
  `same-package duplicate-type-declarations lint (QDRANT-RECOVERY-001 follow-up)`.
- `scripts/ci-architectural-checks.sh` Check 53 (~line 1683) —
  `forbid direct atomic-complete wire calls outside canonical Service (P0 C7, July 2026)`.
- `scripts/ci-architectural-checks.sh` Check 54 (~line 1776) —
  `FASE 3.7 Commit 3 — gate banning infra imports in monitor/`.

> **CRITICAL: shell-check structure (non-obvious gotcha):** the 3 target
> checks are **inline procedural bash blocks**, NOT `check_NN() { ... }`
> functions. Each block starts with `echo "=== Check N: <title> ==="` and
> runs until the next `echo "=== Check N+1: ... ==="` header (or `set +e` for
> the non-fatal wrappers). Deletion must remove the entire block including
> the header + the closing `echo "OK: ..."` line. **Do NOT grep for
> `^check_NN(`** — that pattern is not present in the file. A naive
> `git grep '^check_'` returns ZERO matches.
>
> **SECONDARY gotcha:** the file contains a SEPARATE earlier "Check 5" at
> line ~395 (`forbid mutation primitives in production callers (QDRANT-asset-mutation
> isolation, Wave 22)`) and a SEPARATE "Check 54" reference at line ~3120
> (a comment block referencing the FASE 3.7 Commit 3 contract, not the check
> itself). Both are UNRELATED to the 3 targets — the executor MUST match on
> the specific header titles listed above (`same-package duplicate-type-declarations`,
> `forbid direct atomic-complete wire calls`, `FASE 3.7 Commit 3 — gate banning
> infra imports in monitor/`) to avoid deleting the wrong block.

---

## Steps (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT sequence)

### Step 1 — EXPAND: lock the shell-retirement contract

Add 3 new Go unit tests (one per retired shell check) that read
`scripts/ci-architectural-checks.sh` and assert the canonical header strings
are strictly ABSENT. The tests run as part of `make verify-main` and
`go test ./cmd/archcheck/...`. If a future contributor re-adds the shell
check, the test fails fast at CI time.

**Test signatures** (one per scanner's `_test.go`):

```go
// cmd/archcheck/scan/percheck_typeredecl_test.go
func TestShellCheck5Retired(t *testing.T) {
    // Read scripts/ci-architectural-checks.sh and assert that
    // 'echo "=== Check 5: same-package duplicate-type-declarations' is
    // absent. Per PR-ARCHCHECK-GO-MIGRATION-PHASE-2.
}

// cmd/archcheck/scan/percheck_txcontext_test.go
func TestShellCheck53Retired(t *testing.T) {
    // Assert 'echo "=== Check 53: forbid direct atomic-complete wire calls' is
    // absent.
}

// cmd/archcheck/scan/percheck_monitor_test.go
func TestShellCheck54Retired(t *testing.T) {
    // Assert 'echo "=== Check 54: FASE 3.7 Commit 3 — gate banning infra imports'
    // is absent.
}
```

**Test pattern (full-file grep, allow fail-fast on missing script):**

```go
func TestShellCheckNRetired(t *testing.T) {
    // Resolve repo root from the test binary's working directory; the
    // archcheck binary's CWD is the project root by convention.
    content, err := os.ReadFile("scripts/ci-architectural-checks.sh")
    if err != nil {
        t.Skipf("shell script not present at scripts/ci-architectural-checks.sh: %v", err)
    }
    needle := `echo "=== Check N: <specific title fragment>`
    if strings.Contains(string(content), needle) {
        t.Fatalf("shell check still present (per PR-ARCHCHECK-GO-MIGRATION-PHASE-2 retirement):\n  %s\n  → the canonical fail-closed surface is cmd/archcheck's percheck_* scanner; the shell check is dead code", needle)
    }
}
```

**Rationale (godlike/07 minimum-blast-radius):** the 3 grep-assert tests are
the forward-prevention seam that locks the shell-retirement contract. They
are additive (3 new test functions, 3 file appends); they do NOT modify
existing tests or the production scanner logic.

### Step 2 — CUTOVER: hard-delete the 3 shell checks

Open `scripts/ci-architectural-checks.sh` and **hard-delete** the 3 inline
procedural bash blocks. Per `godlike/07` §"No fake availability": no shims,
no deprecation records, no pass-through wrappers — the shell check is dead
code; the Go scanner IS the source of truth; leaving the shell function
around (even as a thin pass-through) would be fake-availability.

**Deletion instructions (per-check, in order):**

1. **Check 5** (`same-package duplicate-type-declarations lint`):
   - Locate the header `echo "=== Check 5: same-package duplicate-type-declarations (QDRANT-RECOVERY-001 follow-up) ==="`.
   - Delete from the `echo` line (inclusive) to the line just before the next
     `echo "=== Check 6:` (exclusive).
   - Confirm: `rg -n 'Check 5: same-package duplicate-type-declarations' scripts/ci-architectural-checks.sh`
     returns ZERO hits.

2. **Check 53** (`forbid direct atomic-complete wire calls outside canonical Service`):
   - Locate the header `echo "=== Check 53: forbid direct atomic-complete wire calls outside canonical Service (P0 C7, July 2026) ==="`.
   - Delete from the `echo` line (inclusive) to the line just before the next
     `echo "=== Check 54:` (exclusive).
   - Confirm: `rg -n 'Check 53: forbid direct atomic-complete wire calls' scripts/ci-architectural-checks.sh`
     returns ZERO hits.

3. **Check 54** (`FASE 3.7 Commit 3 — gate banning infra imports in monitor/`):
   - Locate the header `echo "=== Check 54: FASE 3.7 Commit 3 — gate banning infra imports in monitor/ ==="`.
   - Delete from the `echo` line (inclusive) to the line just before the next
     `echo "=== Check 55:` (exclusive) — or to EOF if Check 54 is the
     last block in the file.
   - Confirm: `rg -n 'Check 54: FASE 3.7 Commit 3 — gate banning infra imports' scripts/ci-architectural-checks.sh`
     returns ZERO hits.

**Post-deletion verification (godlike/07 no-fake-availability):**

```bash
# 1. Bash syntax check — the file is still valid bash after the deletions.
bash -n scripts/ci-architectural-checks.sh && echo "OK: bash syntax valid"

# 2. No residual references to the deleted checks' canonical titles.
rg -n 'Check 5: same-package duplicate-type-declarations' scripts/ci-architectural-checks.sh || echo "OK: 0 hits for Check 5 title"
rg -n 'Check 53: forbid direct atomic-complete wire calls' scripts/ci-architectural-checks.sh || echo "OK: 0 hits for Check 53 title"
rg -n 'Check 54: FASE 3.7 Commit 3' scripts/ci-architectural-checks.sh || echo "OK: 0 hits for Check 54 title"

# 3. The unrelated "Check 5" (forbid mutation primitives, ~line 395) is UNTOUCHED.
rg -n 'Check 5: forbid mutation primitives' scripts/ci-architectural-checks.sh
#   expected: one hit (the header at line ~395 is preserved)

# 4. The shell self-check mode is still functional (--self-check regexes for
#    the remaining 50+ checks; the 3 retired checks were not in the
#    self-check fixture list, so this is unchanged).
bash scripts/ci-architectural-checks.sh --self-check
#   expected: exit 0 + "All self-checks passed"

# 5. The Go scanners still pass on the live tree.
go run ./cmd/archcheck --strict | jq '.summary'
#   expected: 0 violations across the 3 percheck_* scanners
```

### Step 3 — BACKFILL: tighten the `--strict` gate (no-op)

The current state already has `--strict` enforced via `make verify-main` →
`go run ./cmd/archcheck --strict`. Phase 2 makes no change to the gate
enforcement — Step 2 (shell retirement) is the substantive change; Step 3
is the verification that the gate is already the SOLE canonical fail-closed
surface for the 3 invariant classes.

**Verification (post-Shell-retirement):**

```bash
# 1. verify-main chains cmd/archcheck --strict as the 4th step.
grep -A 10 'verify-main:' Makefile | grep -A 5 '^verify-main:'
#   expected: 4-step chain ending in `go run ./cmd/archcheck --strict`

# 2. CI's verify-main job runs the Makefile target.
grep -A 3 'make verify-main' .github/workflows/ci.yml
#   expected: 1+ hits

# 3. CI's build job still runs the leaner shell script.
grep -A 3 'ci-architectural-checks.sh' .github/workflows/ci.yml
#   expected: 1 hit, no failure on the 3 retired checks (they're gone)
```

**Rationale (godlike/07 minimum-blast-radius):** keeping `main.go`'s
`--strict false` default + relying on the Makefile to force `--strict` is
the correct ergonomic posture — a developer running
`go run ./cmd/archcheck` locally (no flag) gets a report for inspection,
while `make verify-main` (the canonical CI gate) gets the fail-closed
semantics. Flipping the default to `--strict true` would be a breaking
change for any local invocation without the flag (godlike/07 violation).

### Step 4 — CONTRACT: 3-surface lockstep

Land the closure on the canonical 3 surfaces (per `godlike/06` SSOT lockstep
discipline + `CANONICAL.md` §1):

1. **`architecture/current.yaml`** — add a new entry
   `PR-ARCHCHECK-GO-MIGRATION-PHASE-2` with:
   - `owner_capability: architecture/archcheck + scripts/ci-architectural-checks.sh`
   - `status: pending` (flipped to `shipped` post-commit per the standard discipline)
   - `deadline: 2026-08-15`
   - `forward_pointer: PR-ARCHCHECK-GO-MIGRATION-PHASE-1` (the predecessor wave)
   - `linked_issues`: 4 slots (Step 1 tests + Step 2 deletion + Step 3 verification + Step 4 lockstep)
   - `description`: narrative mirroring this plan's "Goal" + "Steps" + "Honest scope-lock" sections

2. **`CHANGELOG.md`** — entry under `## Unreleased → ### Refactor`:
   - Title: `archcheck: Phase 2 — retire 3 shell checks (5/53/54), promote cmd/archcheck to gate-promoted`
   - Body: 1-paragraph summary of the 3 retirements + the 3 grep-assert tests + the ci-gate tightening
   - Trailer: `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` per `AGENTS.md` Git-Lesson-3

3. **`AGENTS.md`** — mirror entry under `## Recent cross-cutting closures`:
   - Title: `[PR-ARCHCHECK-GO-MIGRATION-PHASE-2 closure (date, ship-sha)]`
   - Body: godlike/06 SSOT (one canonical owner per fact) + godlike/07 minimum-blast-radius + the carry-forward invariants below

**Validation gate (post-commit):**

```bash
# YAML validity
python3 -c "import yaml; docs=list(yaml.safe_load_all(open('architecture/current.yaml'))); print(f'YAML VALID: {len(docs)} documents')"

# gofmt clean
gofmt -l cmd/archcheck/scan/

# go vet clean
go vet ./cmd/archcheck/...

# go test clean (the 3 new tests + the 17 existing tests)
go test -count=1 -short ./cmd/archcheck/...

# make verify-main exits 0
make verify-main

# The shell script still passes the bash syntax check + the self-check mode
bash -n scripts/ci-architectural-checks.sh && echo "OK: bash syntax valid"
bash scripts/ci-architectural-checks.sh --self-check
```

---

## Honest scope-lock (godlike/07 no-fake-availability)

**UNCHANGED carry-forwards (NOT touched by Phase 2):**

- The 17 allowlist entries in `docs/architecture/godlike/duplicate-types-allowlist.txt`
  (deadline 2026-09-01, ratchet via individual migration PRs per the file's
  documented process).
- The OTHER 50+ shell checks (0, 1, 2, 3, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
  23, 24-32, 33, 46, 47, 48, 50, 51, 52, 55, 57, 58, 59, N, etc.) — all
  remain canonical fail-closed in CI's `build` job.
- The shell script's `--self-check` mode (the regex unit-test fixture for
  the 50+ remaining checks).
- The UNRELATED "Check 5" (forbid mutation primitives, line ~395) — preserved
  in place; the executor's grep-assert must match the SPECIFIC title
  fragment, not the bare "Check 5" prefix.
- The historical-comment "Check 54" reference at line ~3120 (a documentation
  block referencing the FASE 3.7 contract) — preserved; not a check header.
- `cmd/archcheck/main.go` default flag values (`--strict false`).
- The `Makefile` `verify-main` chain (the 4-step order is already canonical).
- `.github/workflows/ci.yml` (no changes — the gate contract is unchanged
  from CI's perspective; the build job continues to run the leaner shell
  script and the verify-main job continues to run `make verify-main`).

**Pre-existing build issues (carry-forward, NOT regressions of Phase 2):**

- The 5-item carry-forward from `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`:
  `monitor/enqueue.go` `strings.ToLower` + `monitor/scheduler.go` `NewUnboundJobEnqueuer`
  + `stockpipeline/run_upload.go` syntax error + `app/module_media.go` `clips.Deps.MutationsDispatcher`
  + `images/routing` import cycle. All predate Phase 1 + Phase 2; carry forward
  unchanged per `CHANGELOG.md` forward-pointer convention.

---

## Execution order (per the godlike/07 minimum-blast-radius discipline)

Two atomic commits, each landing independently on `main` per `AGENTS.md`
Git-Lesson-2 (direct-to-main workflow):

1. **Commit 1 — Code & Tests** (Steps 1 + 2):
   - Add 3 grep-assert tests to `cmd/archcheck/scan/percheck_*_test.go`.
   - Delete the 3 inline bash blocks from `scripts/ci-architectural-checks.sh`.
   - Verification: `gofmt -l cmd/archcheck/scan/`, `go test -short ./cmd/archcheck/...`,
     `bash -n scripts/ci-architectural-checks.sh`, `bash scripts/ci-architectural-checks.sh --self-check`,
     `make verify-main` all exit 0.

2. **Commit 2 — Lockstep Closure** (Step 4):
   - Add `PR-ARCHCHECK-GO-MIGRATION-PHASE-2` to `architecture/current.yaml`.
   - Add the `## Unreleased → ### Refactor` entry to `CHANGELOG.md`.
   - Add the mirror entry to `AGENTS.md` under `## Recent cross-cutting closures`.
   - Trailer: `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`.
   - Verification: `python3 -c "import yaml; ..."` exits 0; `gofmt -l` exits 0;
     `make verify-main` exits 0.

The 2-commit split mirrors the precedent set by `PR-PERSIST-PR6-CANONICAL`
(2026-07-04) + `PR-DRIVECLIENT-RAW-RETIRE` (2026-07-04) + `PR-CHROME-PROVIDER-SPLIT`
(2026-07-04) — substantive code changes land in isolation on the targeted
subtree (in this case, the `cmd/archcheck/scan/` files + the shell script
deletion) before the lockstep documentation commit flips the wave-tracker
entry from `pending` → `shipped` on the canonical 3 surfaces.

---

## Cross-references

- `architecture/current.yaml#PR-ARCHCHECK-GO-MIGRATION-PHASE-1` (Phase 1 predecessor, shipped 2026-07-04 SHA `67cbcb73`)
- `architecture/current.yaml#FASE-3.7-CHECK-3` (the gate that Check 54 enforces)
- `architecture/current.yaml#id-20` (QDRANT-RECOVERY-001 follow-up — the original Check 5 tracker)
- `cmd/archcheck/main.go` (CLI dispatch — preserved with `--strict false` default)
- `cmd/archcheck/runner.go::DefaultChecks()` (3 new percheck_* entries shipped in Phase 1)
- `cmd/archcheck/scan/percheck_typeredecl.go` (Phase 1 Check 5 Go scanner)
- `cmd/archcheck/scan/percheck_txcontext.go` (Phase 1 Check 53 Go scanner)
- `cmd/archcheck/scan/percheck_monitor.go` (Phase 1 Check 54 Go scanner)
- `Makefile` `verify-main` target (the canonical fail-closed surface; 4th step = `go run ./cmd/archcheck --strict`)
- `.github/workflows/ci.yml::verify-main` (runs `make verify-main` as the fail-closed gate)
- `docs/architecture/godlike/duplicate-types-allowlist.txt` (shared allowlist — 17 entries, 2026-09-01 deadline, UNCHANGED)
- `AGENTS.md` §"Git-Lesson-2" (direct-to-main workflow for the 2-commit split)
- `AGENTS.md` §"Git-Lesson-3" (`Co-authored-by:` trailer convention)
- `AGENTS.md` §"Recent cross-cutting closures" (canonical mirror entry landing site)
- `AGENTS.md` §"godlike/08 ARCHITECTURE-CI-GATES" (zero-baseline rule that motivated Phase 1's transitional baseline)
- `CHANGELOG.md` `## Unreleased → ### Refactor` (canonical CHANGELOG entry landing site)
