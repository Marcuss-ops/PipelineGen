// Package scripts — qa_word_budget.go (PR-CS-1, FASE 5, DoD #6).
//
// CheckWordBudget is the canonical post-strip word-count gate
// applied to model output. It is the SECOND canonical QA pass
// after SanitizeScriptOutput (FASE 4) and runs on every engine
// result regardless of cache-hit / fresh path.
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of
// the budget gate. Any caller that needs to evaluate "did the
// model hit its target ± tolerance" MUST route through
// CheckWordBudget. The effective-target resolution
// (per-segment → plan.SegmentWords → plan.TargetWords → 80)
// is a sibling helper in the same file so the chain is visible
// at the call site of the gate.
//
// Behaviour contract:
//   - target ≤ 0 → no gate (Pass=true, deviation=0). Coexists
//     with the legacy P0.I stricter gate (0.80–1.20) which is
//     a separate measurement, NOT an override of this gate.
//   - target > 0 → PASS when min(actual, maxBound) and
//     actual ≥ minBound, where:
//     minBound = int(float64(target) * 0.75)
//     maxBound = int(float64(target) * 1.25)
//   - DeviationPercent = (actual - target) / target * 100.
//     Returns 0 when target == 0 to keep the metric stable
//     in observability dashboards.
//
// The gate is OBSERVATIONAL ONLY (DoD #6 spec): when it fails
// the engine logs WARN but does NOT block persistence. Gemma's
// word count drifts naturally; an override must be explicit
// (e.g. via cfg + a future FlagOverride field) — never implicit.
package usecase

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ReportBudget is the typed outcome of a CheckWordBudget call.
// All four fields are filled regardless of pass/fail so callers
// can drive dashboards / metrics without re-running the gate.
type ReportBudget struct {
	// TargetWords is the budget the engine attempted (after the
	// fallback chain resolved to a positive int). Zero means
	// "no budget configured" — gate short-circuits to PASS.
	TargetWords int `json:"target_words"`

	// ActualWords is the post-strip word count of the output
	// text (pkg/textutil.CountWords). Not affected by the
	// segment-count or layout — it is the raw prose word count.
	ActualWords int `json:"actual_words"`

	// Pass is true when the output fits within
	// [TargetWords*0.75, TargetWords*1.25] (inclusive) OR when
	// TargetWords is 0 (no gate is configured). False only when
	// the gate IS configured and the output strays OUTSIDE the
	// ±25% window.
	Pass bool `json:"pass"`

	// DeviationPercent is (ActualWords - TargetWords) / Target
	// Words * 100. Positive = over-budget, negative = under.
	// Zero when TargetWords == 0 so consumers don't see a
	// divide-by-zero sentinel.
	DeviationPercent float64 `json:"deviation_percent"`
}

// CheckWordBudget is the canonical post-strip budget gate.
// Pure function: no logger, no metrics, no I/O. The caller
// is responsible for logging the report.
//
// Algorithm:
//  1. If targetWords <= 0 → return
//     {TargetWords:0, ActualWords: words, Pass:true,
//     DeviationPercent: 0}.
//  2. words = pkg/textutil.CountWords(text).
//  3. minOK = int(float64(targetWords) * 0.75).
//  4. maxOK = int(float64(targetWords) * 1.25).
//  5. Pass = words >= minOK && words <= maxOK.
//  6. DeviationPercent = float64(words-targetWords) /
//     float64(targetWords) * 100.
//
// Idempotent and total — no panic paths, no error returns.
// (Engine wire explodes FAIL into a WARN log with the
// ReportBudget fields; the gate itself never returns an
// error because the failure is a metric, not a bug.)
func CheckWordBudget(text string, targetWords int) ReportBudget {
	words := textutil.CountWords(text)

	// Step 1: no budget configured → no gate.
	if targetWords <= 0 {
		return ReportBudget{
			TargetWords:      0,
			ActualWords:      words,
			Pass:             true,
			DeviationPercent: 0,
		}
	}

	// Step 3-4: tolerance bounds (int truncation is fine —
	// tighter low bound + tolerant high bound are the names of
	// the game for ±25%).
	minOK := int(float64(targetWords) * 0.75)
	maxOK := int(float64(targetWords) * 1.25)

	// Step 5: gate (inclusive).
	pass := words >= minOK && words <= maxOK

	// Step 6: deviation metric. Safe — targetWords > 0 here.
	deviation := float64(words-targetWords) / float64(targetWords) * 100.0

	return ReportBudget{
		TargetWords:      targetWords,
		ActualWords:      words,
		Pass:             pass,
		DeviationPercent: deviation,
	}
}

// effectiveTargetForBudgetWords resolves the canonical target
// for the word-budget gate. The chain matches engine_prompt.go
// (FASE 3) verbatim so the prompt-rendering budget and the
// post-strip QA budget remain consistent — there is one
// canonical definition of "what target is in effect for this
// generation".
//
// Chain (first non-zero wins):
//  1. First plan.Segments[i].TargetWords > 0
//  2. plan.SegmentWords > 0
//  3. plan.TargetWords > 0
//  4. default 80
//
// The choice of "first matching segment" over "sum of matching
// segments" is deliberate: it matches engine_prompt.go which
// also picks per-segment TargetWords for the per-block budget.
// If a future spec wants sum-of-segments semantics, update both
// this helper and the engine-prompt renderer in lockstep.
func effectiveTargetForBudgetWords(plan *scriptpkg.ResolvedGenerationPlan) int {
	if plan == nil {
		return 80
	}
	for _, s := range plan.Segments {
		if s.TargetWords > 0 {
			return s.TargetWords
		}
	}
	if plan.SegmentWords > 0 {
		return plan.SegmentWords
	}
	if plan.TargetWords > 0 {
		return plan.TargetWords
	}
	return 80
}
