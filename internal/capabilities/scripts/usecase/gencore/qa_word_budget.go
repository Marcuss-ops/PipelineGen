// Package scripts — qa_word_budget.go (PR-CS-1, FASE 5, DoD #6).
//
// CheckWordBudget is the canonical post-strip word-count gate applied to
// model output. It is the SECOND canonical QA pass after
// SanitizeScriptOutput (FASE 4).
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the budget
// gate. Any caller that needs to evaluate "did the model hit its target ±
// tolerance" MUST route through CheckWordBudget.
//
// There are two distinct targets and they must never be confused:
//   - effectiveSegmentTargetWords(segment, plan): the budget of ONE scene,
//     used while generating/validating that scene;
//   - effectiveScriptTargetWords(plan): the aggregate budget of the whole
//     script, the sum of the per-scene targets (fallback plan.TargetWords).
//
// The historical helper effectiveTargetForBudgetWords returned the first
// segment's target for the WHOLE output. On the five-scene Ilary/Totti run
// (5 × 700 = 3.500 words) that compared 4.361 words with 700 and logged a
// false +523% instead of the real +24,6% (CODEX-02). The two helpers below
// replace it so the aggregate and per-scene checks can never share an
// ambiguous name again.
//
// Behaviour contract:
//   - target ≤ 0 → no gate (Pass=true, deviation=0).
//   - target > 0 → PASS when actual ∈ [target*0.75, target*1.25].
//   - DeviationPercent = (actual - target) / target * 100 (0 when target=0).
//
// Policy (PIPELINEGEN_WORD_BUDGET_POLICY, legacy VELOX_ alias):
//   - "warn" (default): the gate is observational; it logs and never blocks
//     persistence. Segment generation still performs its own bounded repair
//     (one regeneration) through validateSegmentTexts.
//   - "strict": a failed aggregate or explicit-scene gate fails the run with
//     scriptpkg.ErrWordBudgetExceeded. This is the fail-closed option for
//     operators who would rather reject an over-budget script than ship it.
package gencore

import (
	"os"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// Word-budget policy values. "warn" is the historical, non-blocking default;
// "strict" fails closed with scriptpkg.ErrWordBudgetExceeded.
const (
	WordBudgetPolicyWarn   = "warn"
	WordBudgetPolicyStrict = "strict"

	// EnvWordBudgetPolicy is the canonical operator knob.
	EnvWordBudgetPolicy = "PIPELINEGEN_WORD_BUDGET_POLICY"
	// legacyWordBudgetPolicy is accepted for deployments that still carry
	// the VELOX_ prefix.
	legacyWordBudgetPolicy = "VELOX_WORD_BUDGET_POLICY"
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

// ReportScriptBudget is the aggregate + per-scene outcome for a generated
// script. Scenes is populated only when the text splits into exactly one
// paragraph per plan segment AND the scene has an explicit budget
// (segment.TargetWords, or plan.SegmentWords for a single-scene plan).
// SceneSplit reports whether the paragraph mapping was usable, so a caller
// can distinguish "every scene passed" from "only the aggregate was checked".
type ReportScriptBudget struct {
	Aggregate  ReportBudget   `json:"aggregate"`
	Scenes     []ReportBudget `json:"scenes,omitempty"`
	SceneSplit bool           `json:"scene_split"`
	Pass       bool           `json:"pass"`
}

// CheckWordBudget is the canonical post-strip budget gate.
// Pure function: no logger, no metrics, no I/O. The caller
// is responsible for logging the report.
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

// CheckScriptWordBudget evaluates the aggregate script budget and, when the
// text can be mapped one-paragraph-per-segment, every explicit scene budget.
// It is purely observational: policy enforcement lives in the Engine so the
// pure gate remains trivially testable.
func CheckScriptWordBudget(text string, plan *scriptpkg.ResolvedGenerationPlan) ReportScriptBudget {
	aggregate := CheckWordBudget(text, effectiveScriptTargetWords(plan))
	report := ReportScriptBudget{
		Aggregate: aggregate,
		Pass:      aggregate.Pass,
	}
	if plan == nil || len(plan.Segments) == 0 {
		return report
	}
	paragraphs := splitBudgetParagraphs(text)
	if len(paragraphs) != len(plan.Segments) {
		return report
	}
	report.SceneSplit = true
	for i, segment := range plan.Segments {
		target := sceneBudgetTarget(segment, plan)
		if target <= 0 {
			continue
		}
		sceneReport := CheckWordBudget(paragraphs[i], target)
		report.Scenes = append(report.Scenes, sceneReport)
		if !sceneReport.Pass {
			report.Pass = false
		}
	}
	return report
}

// effectiveSegmentTargetWords resolves the budget of ONE segment using the
// canonical fallback chain: segment.TargetWords → plan.SegmentWords →
// plan.TargetWords → 80. It is the per-scene companion of
// effectiveScriptTargetWords; never use it to gate the whole output.
func effectiveSegmentTargetWords(segment scriptpkg.ScriptSegment, plan *scriptpkg.ResolvedGenerationPlan) int {
	if segment.TargetWords > 0 {
		return segment.TargetWords
	}
	if plan != nil {
		if plan.SegmentWords > 0 {
			return plan.SegmentWords
		}
		if plan.TargetWords > 0 {
			return plan.TargetWords
		}
	}
	return 80
}

// effectiveScriptTargetWords resolves the budget of the WHOLE script: the sum
// of the explicit per-segment TargetWords. When no segment declares a target,
// it falls back to plan.TargetWords, then SegmentWords×segments, then the
// canonical 80-word default per segment. Zero is returned only for a plan with
// no segments and no target at all (no gate configured).
func effectiveScriptTargetWords(plan *scriptpkg.ResolvedGenerationPlan) int {
	if plan == nil {
		return 0
	}
	if len(plan.Segments) == 1 {
		// A single-segment script has no distinction between the scene budget
		// and the script budget: use the segment chain so SegmentWords wins
		// over a looser plan.TargetWords.
		return effectiveSegmentTargetWords(plan.Segments[0], plan)
	}
	total := 0
	for _, segment := range plan.Segments {
		if segment.TargetWords > 0 {
			total += segment.TargetWords
		}
	}
	if total > 0 {
		return total
	}
	if plan.TargetWords > 0 {
		return plan.TargetWords
	}
	if len(plan.Segments) > 0 {
		if plan.SegmentWords > 0 {
			return plan.SegmentWords * len(plan.Segments)
		}
		return 80 * len(plan.Segments)
	}
	return 0
}

// sceneBudgetTarget returns the explicit per-scene target used for the
// per-scene report. A multi-segment plan whose only budget is plan.TargetWords
// is deliberately skipped: that number describes the whole script and would
// flag every scene as wildly under budget. The aggregate check still covers it.
func sceneBudgetTarget(segment scriptpkg.ScriptSegment, plan *scriptpkg.ResolvedGenerationPlan) int {
	if segment.TargetWords > 0 {
		return segment.TargetWords
	}
	if len(plan.Segments) == 1 {
		return effectiveSegmentTargetWords(segment, plan)
	}
	if plan.SegmentWords > 0 {
		return plan.SegmentWords
	}
	return 0
}

// splitBudgetParagraphs maps assembled plain text back to scene paragraphs.
// The canonical assembler joins sanitized scene paragraphs with a blank line,
// so the mapping is exact only when the counts match; the caller checks that.
func splitBudgetParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	parts := strings.Split(text, "\n\n")
	paragraphs := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			paragraphs = append(paragraphs, part)
		}
	}
	return paragraphs
}

// WordBudgetPolicy returns the configured policy. Unknown or empty values
// resolve to the safe historical default ("warn") instead of silently
// switching production to fail-closed.
func WordBudgetPolicy() string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(EnvWordBudgetPolicy)))
	if raw == "" {
		raw = strings.ToLower(strings.TrimSpace(os.Getenv(legacyWordBudgetPolicy)))
	}
	switch raw {
	case WordBudgetPolicyStrict, "enforce", "fail", "fail_closed":
		return WordBudgetPolicyStrict
	default:
		return WordBudgetPolicyWarn
	}
}

// enforceWordBudget logs the aggregate/scene reports and, under the strict
// policy, turns a failed gate into a typed error so the run fails closed
// instead of shipping an over-budget script.
func (e *Engine) enforceWordBudget(plan *scriptpkg.ResolvedGenerationPlan, text string) error {
	report := CheckScriptWordBudget(text, plan)
	if e != nil && e.log != nil {
		fields := []zap.Field{
			zap.Int("target_words", report.Aggregate.TargetWords),
			zap.Int("actual_words", report.Aggregate.ActualWords),
			zap.Float64("deviation_percent", report.Aggregate.DeviationPercent),
			zap.Bool("scene_split", report.SceneSplit),
			zap.Int("scene_reports", len(report.Scenes)),
		}
		for i, scene := range report.Scenes {
			if scene.Pass {
				continue
			}
			fields = append(fields,
				zap.Int("scene_index", i),
				zap.Int("scene_target_words", scene.TargetWords),
				zap.Int("scene_actual_words", scene.ActualWords),
				zap.Float64("scene_deviation_percent", scene.DeviationPercent),
			)
			break
		}
		if report.Pass {
			e.log.Info("engine: word-budget gate PASS", fields...)
		} else {
			e.log.Warn("engine: word-budget gate FAIL", fields...)
		}
	}
	if report.Pass || WordBudgetPolicy() != WordBudgetPolicyStrict {
		return nil
	}
	itemID := ""
	if plan != nil {
		itemID = plan.ID
	}
	return &scriptpkg.WordBudgetError{
		ItemID:           itemID,
		TargetWords:      report.Aggregate.TargetWords,
		ActualWords:      report.Aggregate.ActualWords,
		DeviationPercent: report.Aggregate.DeviationPercent,
		Details: []string{
			"aggregate or explicit scene word budget is outside the ±25% window",
		},
	}
}
