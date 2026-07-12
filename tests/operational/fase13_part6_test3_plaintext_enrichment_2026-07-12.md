# PRE-EXISTING-13 evidence — FASE 13 PART 6 Test #3 PlaintextOutput registry enrichment

| Field | Value |
|---|---|
| Entry | `architecture/issues.yaml` -> `PRE-EXISTING-13-USECASE-PLAINTEXT-ENRICHMENT` |
| Test | `TestPlaintextOutput_P0F_Orchestrator_FakeOllamaCleanProse` |
| File | `internal/application/scripts/usecase/clip_plaintext_output_p0f_test.go:406` (test assertion line) |
| Diagnostic date | 2026-07-12 |
| Closure status | FASE 13 PART 5 commit `91444b9c7` deferred; PART 6 attempt deferred; **resident (status: in_progress)** |

## Verbatim failure (triple-nested wrapping, as observed on PURE main before PART 6)

```
FAIL — Received unexpected error:
  generation: script generation failed: generation: editorial quality gate failed:
  generation: editorial quality gate failed:
  code=QUALITY_GATE_FAILED item="p0f-clean-prose";
  source_text coverage below threshold; actual word count outside target tolerance
```

The triple-nesting is canonical `scriptpkg.ErrGenerationFailed` + `scriptpkg.ErrQualityGateFailed`
wrapping inside `GenerationError.Inner`. Each `generation: ` prefix is a wrapping layer.

## Reproduction (command)

```bash
go test -count=1 -v \
  -run '^TestPlaintextOutput_P0F_Orchestrator_FakeOllamaCleanProse$' \
  ./internal/application/scripts/usecase/
```

Pre-FIX-B: FAIL (above). Post-FIX-B (fixture `SourceText = cleanProse` + `Topic = ""` +
`TargetWords = 20` on disk): `=== RUN ... PASS` (UNVERIFIED locally; blocked by upstream
`internal/domain/remote/{complete_job_idempotency.go, idempotency.go}` build error).

## Reasoning chain (registry enrichment pattern)

1. The test fixture creates a text-only item via `makeTextOnlyItem("p0f-clean-prose", "")`,
   which seeds:
   - `Source.SourceText = ""`
   - `Source.Topic = "e2e topic"` (default from helper at
     `internal/application/scripts/usecase/generate_e2e_helpers_test.go:113`)
   - `ScriptParams.TargetWords = 10`

2. The test then overrides:
   - `item.Source.SourceText = "<overrides per FASE 13 PART 6>"` (varies by closure path)
   - `item.ScriptParams.TargetWords = 0` (then 20 in FASE 13 PART 6)

3. `GenerateOneUseCase.Execute` (in
   `internal/application/scripts/usecase/generate_one_usecase.go:174`) Phase 3 calls:
   ```go
   resolved, resolveErr = uc.registry.Resolve(ctx, item.Source, resCtx)
   ```
   where `uc.registry` was constructed in `buildUsecaseWithClipResolver` via
   `adapters.NewSourceRegistry(...)` + `reg.Register(scriptpkg.SourceText,
   NewTextSourceResolver())`. The text-source resolver enriches `resolved.SourceText`
   from `item.Source.Topic = "e2e topic"` via the canonical text-resolver pattern.

4. Phase 4 merge (in `generate_one_usecase.go:187-189`):
   ```go
   if resolved != nil {
       ...
       if resolved.SourceText != "" {
           plan.SourceText = resolved.SourceText   // OVERRIDES the item-side empty
       }
       ...
   }
   ```

   The text-resolver enriches `resolved.SourceText`, so the merge overrides
   `plan.SourceText = ""` (set in `BuildPlan` from `item.Source.SourceText`) with the
   enrichment. **Even though the test fixture intended `SourceText=""`, the merge
   produces a non-empty `plan.SourceText`.**

5. `evaluateQualityGate` (in
   `internal/application/scripts/usecase/quality_gate.go:128`) sees non-empty
   `buildSourceText(plan)`:
   - `q.SourceTextCoverage = computeSourceTextCoverage(generatedText, sourceText)` —
     "Round 1 begins with both fighters trading jabs..." (generated) shares no tokens
     with the enrichment ("e2e topic" or similar), so `coverage ~= 0.0` vs threshold
     `0.70` -> **"source_text coverage below threshold"** fires.
   - Tolerance gate (`plan.TargetWords > 0 && sourceText != ""`) is now engaged (NOT
     the FASE 13 PART 2 observational skip, which only fires when `sourceText == ""`).
     With `plan.TargetWords=0`, the lower/upper bounds are `[0, 0]`, so `actual=20 > 0`
     -> **"actual word count outside target tolerance"** fires.

## Closure paths (3 options, ranked by REGRESSION VALUE)

### Option A — Test-fix fixture overlap (applied on disk in PART 6; UNVERIFIED)

```diff
- item := makeTextOnlyItem("p0f-clean-prose", "")
- item.ScriptParams.TargetWords = 0
+ item := makeTextOnlyItem("p0f-clean-prose", cleanProse)
+ item.Source.Topic = ""              // prevent Topic-to-SourceText enrichment
+ item.ScriptParams.TargetWords = 20   // matches fakeOllamaGen{WordCount:20}
```

Blast radius: 1 file, 2 lines. Coverage = 1.0 (perfect overlap). Tolerance = 20 in
`[16, 24]` (80-120% of 20). Both gates engage cleanly WITHOUT faking availability.
**Risk**: registry's `resolved.SourceText` may STILL override `cleanProse` if the
text-resolver enriches from non-Topic sources (preset defaults, normalization
defaults). Mitigation: add log-trace emission on the `plan.SourceText` assignment
in `generate_one_usecase.go:189` for diagnostic visibility.

### Option B — Add `EmptySourceShortCircuits` sub-test (preserves FASE 13 PART 1 regression)

Adds a SECOND sub-test that exercises `SourceText="" + Plan.TargetWords=0` with
`uc.registry = nil` (or stub-resolver that returns `ResolvedSource{} == nil`). This
test exercises FASE 13 PART 1's source-text short-circuit (in `quality_gate.go:128`)
end-to-end, validating the production fix at runtime rather than relying on
type-system. Needs `buildUsecaseWithClipResolver` to accept a `uc.registry`
parameter (currently hardcoded).

### Option C — Production-seam refactor: `buildUsecaseWithClipResolver` accepts `registry` param

Largest blast radius. Refactor `buildUsecaseWithClipResolver` (and any sister
helpers) to take `uc.registry` as a parameter. Needed for Option B's
EmptySourceShortCircuits sub-test. **Out of FASE 13 scope per AGENTS.md "no
features unless explicitly requested"** — file as separate godlike/06 SSOT PR.

## Cross-reference back to architecture/issues.yaml

- Tracking entry: `PRE-EXISTING-13-USECASE-PLAINTEXT-ENRICHMENT` (this file's audit trail).
- Forward-pointers (per AGENTS.md Documentation rule "Git history is the archive"):
  FASE 13 PART 5 commit `91444b9c7` (partial closure 2 of 4 GREEN) -> FASE 13
  PART 6 uncommitted (Option A fixture overlap applied on disk) -> PRE-EXISTING-13
  new entry (status: in_progress as of this evidence file's authoring).

## godlike/06 SSOT + AGENTS.md compliance

- godlike/07 NO_FAKE_AVAILABILITY: failure messages captured verbatim from PURE main
  (not conjectured); closure path crosses reproducibility checkpoints (assertion
  failure -> registry enrichment -> merge override -> quality gate).
- AGENTS.md Documentation rule: this evidence file IS the canonical trace artifact;
  architecture/issues.yaml's evidence_filename field points at this file.
- godlike/06 SSOT: this entry is the single owner of Test #3 residual tracking;
  Option C (buildUsecaseWithClipResolver seam) is out-of-scope and explicitly flagged.
