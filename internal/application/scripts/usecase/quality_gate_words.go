// Package usecase — quality_gate_words.go
//
// Target-word tolerance rule of the editorial quality gate.
package usecase

import "strings"

// targetWordsChecker fails when the actual word count is outside the
// 80-120% target tolerance. PRE-EXISTING-7 / FASE 13 PART 2: the
// tolerance only enforces when a source anchor exists (plan.SourceText
// or clip evidence). Pure-prose free-form generation has no anchor —
// the tolerance is observational only. TargetWords belongs to the
// canonical source script; translations naturally change word count
// across languages, so the English tolerance is skipped for translated
// text to avoid false failures.
type targetWordsChecker struct{}

func (targetWordsChecker) Name() string { return "target_words_tolerance" }

func (targetWordsChecker) Check(in qualityGateInput) []string {
	// Clip narration is intentionally short and naturally varies with the
	// supplied clip. Its per-segment validator already enforces the bounded
	// clip budget; applying the documentary aggregate target here rejects
	// valid one- or two-sentence intros (especially with Gemma).
	if in.plan.ClipEvidence != nil {
		return nil
	}
	if in.plan.TargetWords > 0 && strings.TrimSpace(in.sourceText) != "" && strings.TrimSpace(in.plan.TranslateTo) == "" {
		lower := float64(in.plan.TargetWords) * minTargetWordsRatio
		upper := float64(in.plan.TargetWords) * maxTargetWordsRatio
		if float64(in.q.ActualWords) < lower || float64(in.q.ActualWords) > upper {
			return []string{"actual word count outside target tolerance"}
		}
	}
	return nil
}
