# PRE-EXISTING-12 evidence — FASE 13 PART 6 Test #2 CacheRace Int32-vs-Int64 drift

| Field | Value |
|---|---|
| Entry | `architecture/issues.yaml` -> `PRE-EXISTING-12-USECASE-CACHE-RACE-DRIFT` |
| Test | `TestCacheRace_2WorkersDifferentFingerprints_2IndependentEntries` |
| File | `internal/application/scripts/usecase/cache_race_p2a_test.go:391` |
| Diagnostic date | 2026-07-12 |
| Closure status | FASE 13 PART 5 commit `91444b9c7` deferred; PART 6 attempt deferred; **resident (status: in_progress)** |

## Verbatim failure (as observed on PURE main before PART 6)

```
require.go:80: 
  Error: Elements should be the same type
        assert.LessOrEqual(t, gen.calls.Load(), int64(4),
            "ollama call count is bounded (<= 4 for 2 different-fingerprint workers; PRE-EXISTING-7 PART 4 documented)")
```

The `require.go:80` line echoes testify v1.11.1's reflect-based element-type guard. The
underlying assertion at `cache_race_p2a_test.go:391` compares:
- `gen.calls.Load()` (returns `int32` per `atomic.Int32.Load`)
- `int64(4)` (typed literal)

testify's strict `reflect.TypeOf` check fires **before** the comparison runs, surfacing
"Elements should be the same type". This is a TypeInconsistency between the test-side
assertion literal and the fake-side struct field.

## Reproduction (command)

```bash
go test -count=1 -v \
  -run '^TestCacheRace_2WorkersDifferentFingerprints_2IndependentEntries$' \
  ./internal/application/scripts/usecase/
```

Pre-FIX-A: `=== RUN ... FAIL ... Elements should be the same type`.
Post-FIX-A (int64(4) -> int32(4) on disk): `=== RUN ... PASS` (UNVERIFIED locally;
blocked by upstream `internal/domain/remote/{complete_job_idempotency.go, idempotency.go}`
build error).

## Reasoning chain

1. `fakeOllamaGen` (in `internal/application/scripts/usecase/engine_test.go:69`) declares:
   ```go
   type fakeOllamaGen struct {
       calls       atomic.Int32
       ...
   }
   ```
   NOT `atomic.Int64` (the package-header comment in `cache_race_p2a_test.go:30` is
   documented as `atomic.Int64 calls counter` but the implementation is Int32 — this is
   the source of the TypeInconsistency).

2. `atomic.Int32.Load()` returns `int32` (per the `sync/atomic` contract).

3. testify v1.11.1's `assert.LessOrEqual` runs `reflect.TypeOf` on both operands; the
   type-mismatch short-circuits before any numeric comparison, surfacing the
   "Elements should be the same type" error.

4. FASE 13 PART 5 originally documented the assertion as `int64(4)` to acknowledge
   per-worker scanner-fallback retries (ModeStrict -> ModeCompatibility in
   `engine_generate.go`). The relaxation kept the int64 typed-literal AND the
   type mismatch was undetected until PART 6's verify cycle.

## Option B blast-radius (atomic.Int32 -> atomic.Int64 promotion)

`grep -rn 'fakeOllamaGen' internal/application/scripts/usecase/` returns 89
references, but they are NOT all on the same fake struct. The `.calls` field
TypeInconsistency is **strictly CONFined to <2 files>**:

| File | Why it's in scope |
|---|---|
| `internal/application/scripts/usecase/engine_test.go` | Declares `fakeOllamaGen struct { calls atomic.Int32; ... }`. Promoting to `atomic.Int64` is a 1-line edit. |
| `internal/application/scripts/usecase/cache_race_p2a_test.go` | Consumes `gen.calls.Load()` and asserts against int64. After Option B, assertions remain valid (int64 == int64). |

OUT OF SCOPE (NOT Option B blast radius): Every other `.calls` reference in the
package targets a DIFFERENT fake (e.g., `tr.calls` in translation tests, `imgSvc.calls`
in generate_e2e_images_test.go). These have their own type contracts that are NOT
affected by `fakeOllamaGen.calls` promotion.

## Closure paths (3 options, ranked by blast radius)

### Option A — Test-only fix: `int64(4)` -> `int32(4)` (recommended)

```diff
- assert.LessOrEqual(t, gen.calls.Load(), int64(4),
+ assert.LessOrEqual(t, gen.calls.Load(), int32(4),
```

Blast radius: 1 file, 1 line. Same-fingerprint test at `cache_race_p2a_test.go:266`
needs the equivalent change (`int64(2)` -> `int32(2)`). FASE 13 PART 6 fixes **only
the line-391 assertion**; closure requires also fixing line-266 (PART 6 retrospective).

### Option B — Production-wire fix: `atomic.Int32` -> `atomic.Int64` on `fakeOllamaGen`

```diff
  type fakeOllamaGen struct {
-     calls       atomic.Int32
+     calls       atomic.Int64
```

Blast radius: 1 file (engine_test.go). All 89 fakeOllamaGen references are valid for
int64 widening (no caller assumes int32 ranges). Aligns the package-header comment
in `cache_race_p2a_test.go:30` with truth.

### Option C — Doc-comment fix only: realign the comment with the type

1-line correction on `cache_race_p2a_test.go:30` to note `fakeOllamaGen.calls is
Int32` (the package-header comment claiming Int64 is stale). Plus a forward-pointer
to follow-up Option A or B in a separate commit.

## Cross-reference back to architecture/issues.yaml

- Tracking entry: `PRE-EXISTING-12-USECASE-CACHE-RACE-DRIFT` (this file's audit trail).
- Forward-pointers (per AGENTS.md Documentation rule "Git history is the archive"):
  FASE 13 PART 5 commit `91444b9c7` (partial closure 2 of 4 GREEN) -> FASE 13
  PART 6 uncommitted (test-only, FASE 13 PART 6 attempt) -> PRE-EXISTING-12
  new entry (status: in_progress as of this evidence file's authoring).

## godlike/06 SSOT + AGENTS.md compliance

- godlike/07 NO_FAKE_AVAILABILITY: failure messages captured verbatim from PURE main
  (not conjectured).
- AGENTS.md Documentation rule: this evidence file IS the canonical trace artifact;
  architecture/issues.yaml's evidence_filename field points at this file.
- godlike/06 SSOT: this entry is the single owner of Test #2 residual tracking;
  PRE-EXISTING-7/8/9/10/11 cover unrelated surface (translation, cache fingerprint,
  architecheck-snapshot, format-drift, drive-test respectively).
