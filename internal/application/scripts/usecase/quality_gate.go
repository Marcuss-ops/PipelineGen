// Package usecase — quality_gate.go implements the editorial quality
// gate for /api/script/generate.
//
// The gate runs after generation and postprocessing are complete and
// checks:
//   - detected language == requested language
//   - source_text coverage meets the configured policy threshold
//   - clip_evidence coverage == 1.00 for clips_primary
//   - unsupported claims == 0 for grounded sources (diagnostic for text)
//   - target words within 80-120% tolerance for documentary/source plans
//     (clip intros use their bounded per-segment budget instead)
//   - reject empty/generic text
//
// The result is always populated in GenerationResult.Quality. When
// the gate fails, a typed QualityGateError is returned so the caller
// can surface both the metrics and the failure reasons.
//
// Layout (refactor, August 2026): the editorial rules live in
// single-purpose checker files (quality_gate_language.go,
// quality_gate_coverage.go, quality_gate_claims.go,
// quality_gate_segments.go, quality_gate_words.go) registered in the
// ordered registry qualityGateRules (quality_gate_checkers.go). This
// file keeps the orchestrator: metrics computation + rule iteration.
package usecase

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// Quality thresholds.
const (
	// defaultMinSourceTextCoverage is the fallback minimum acceptable
	// ratio of generated content words that must be present in the
	// source text or clip evidence.
	//
	defaultMinSourceTextCoverage = 0.70

	// minTargetWordsRatio is the lower bound of the target word
	// tolerance (actual >= target * minTargetWordsRatio).
	minTargetWordsRatio = 0.80

	// maxTargetWordsRatio is the upper bound of the target word
	// tolerance (actual <= target * maxTargetWordsRatio).
	maxTargetWordsRatio = 1.20
)

// policyThresholds returns the source_text and clip_evidence coverage
// thresholds for a given grounding policy. The defaults are tuned so
// that:
//   - clips_primary: clips are the main source; source_text is only
//     support, so source_text coverage can be lower but clip binding
//     must be high.
//   - source_primary: source_text is the main source; clips are only
//     visual support, so source_text coverage must be high and clip
//     binding is not required.
//   - balanced: both sources have equal weight, so both must be
//     reasonably covered.
//
// Rationale for the numeric thresholds:
//   - clips_primary source 0.40: the script is allowed to expand
//     beyond the provided source_text because the clips carry the
//     factual burden, but some textual overlap is still required.
//   - source_primary source 0.85: the script must stay very close
//     to the provided source_text because it is the authoritative
//     source; clips are decorative.
//   - balanced source 0.60 / clip 0.50: both sources must be
//     meaningfully represented, but neither needs to dominate.
func policyThresholds(policy string) (sourceMin, clipMin float64) {
	switch policy {
	case scriptpkg.GroundingPolicyClipsPrimary:
		return 0.40, 1.00
	case scriptpkg.GroundingPolicySourcePrimary:
		return 0.85, 0.00
	case scriptpkg.GroundingPolicyBalanced:
		return 0.60, 0.50
	default:
		return defaultMinSourceTextCoverage, 0.00
	}
}

// buildSourceText assembles the canonical source text against which
// coverage and unsupported-claim checks are evaluated. For clip-based
// sources it concatenates the plan source text with the assembled clip
// evidence text.
func buildSourceText(plan scriptpkg.ResolvedGenerationPlan) string {
	parts := []string{plan.SourceText}
	if plan.ClipEvidence != nil {
		parts = append(parts, plan.ClipEvidence.ModelSourceText())
	}
	return strings.Join(parts, " ")
}

// evaluateQualityGate computes the editorial quality metrics for a
// generated result and returns a typed error when the gate fails.
// The returned Quality value is always populated, even on failure.
//
// Flow: (1) pre-metric hard stops (empty / generic text), (2) metric
// computation (language, coverage, claims), (3) policy thresholds, and
// (4) the ordered rule registry — every rule in qualityGateRules emits
// its failure reasons against the computed metrics.
func evaluateQualityGate(
	result *scriptpkg.GenerationResult,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
) (*scriptpkg.GenerationQuality, error) {
	if result == nil {
		return nil, nil
	}

	requestedLang := strings.ToLower(strings.TrimSpace(plan.Language))
	generatedText := strings.TrimSpace(result.Output.Text)

	q := &scriptpkg.GenerationQuality{
		LanguageRequested: requestedLang,
		TargetWords:       plan.TargetWords,
		ActualWords:       result.Output.WordCount,
	}

	// Reject empty text.
	if generatedText == "" {
		q.Passed = false
		return q, &scriptpkg.QualityGateError{
			ItemID:  item.ID,
			Reasons: []string{"generated text is empty"},
			Quality: *q,
		}
	}

	// Reject generic/placeholder text.
	if isGenericText(generatedText) {
		q.Passed = false
		return q, &scriptpkg.QualityGateError{
			ItemID:  item.ID,
			Reasons: []string{"generated text is generic or placeholder"},
			Quality: *q,
		}
	}

	// Language detection.
	q.LanguageDetected = detectLanguage(generatedText)

	sourceText := buildSourceText(plan)
	if strings.TrimSpace(sourceText) == "" {
		q.SourceTextCoverageStatus = "NOT_EVALUATED"
		q.SourceTextCoverage = 0.0
	} else {
		q.SourceTextCoverageStatus = "EVALUATED"
		q.SourceTextCoverage = computeSourceTextCoverage(generatedText, sourceText)
	}

	// Clip evidence coverage.
	q.ClipEvidenceCoverage = computeClipEvidenceCoverage(result, plan)

	// Unsupported claims (entity-based heuristic).
	q.UnsupportedClaims = countUnsupportedClaims(result, sourceText)

	// Evaluate thresholds per grounding policy.
	minSourceTextCov, minClipCov := policyThresholds(plan.GroundingPolicy)
	// When no clip evidence is present, the clip coverage requirement
	// is irrelevant regardless of policy.
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		minClipCov = 0.00
	}

	// Run the ordered rule registry.
	var reasons []string
	for _, rule := range qualityGateRules {
		reasons = append(reasons, rule.Check(qualityGateInput{
			result:       result,
			plan:         plan,
			q:            q,
			sourceText:   sourceText,
			minSourceCov: minSourceTextCov,
			minClipCov:   minClipCov,
		})...)
	}

	q.Passed = len(reasons) == 0
	if !q.Passed {
		return q, &scriptpkg.QualityGateError{
			ItemID:  item.ID,
			Reasons: reasons,
			Quality: *q,
		}
	}
	return q, nil
}
