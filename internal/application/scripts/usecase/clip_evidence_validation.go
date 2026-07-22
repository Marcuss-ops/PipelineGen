// Package usecase — clip_evidence_validation.go validates that
// caller-provided source_text for clip-based sources does not exceed
// the word budget implied by the available clip evidence duration.
//
// This file is intentionally separate from generation_prepare.go
// because the typed-carrier purity gate forbids `map[string]any`
// literals in the four pipeline-carrier files; the validation error
// payload here is the canonical exception-free surface.
package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// enforceClipEvidenceTextSupport rejects source_text that exceeds
// what the resolved clip evidence duration can support. It only
// applies to clip-based sources when the caller provided source_text
// and WordsPerSecondClipEvidence is configured.
func enforceClipEvidenceTextSupport(plan *scriptpkg.ResolvedGenerationPlan, cfg adapters.NormalizationConfig) error {
	if cfg.WordsPerSecondClipEvidence <= 0 {
		return nil
	}
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.ClipDetails) == 0 {
		return nil
	}
	if plan.SourceText == "" {
		return nil
	}
	if !scriptpkg.IsClipSourceType(scriptpkg.SourceType(plan.SourceKind)) {
		return nil
	}

	var totalSeconds float64
	for _, detail := range plan.ClipEvidence.ClipDetails {
		if detail.EndMs > detail.StartMs {
			totalSeconds += float64(detail.EndMs-detail.StartMs) / 1000.0
		}
	}
	if totalSeconds <= 0 {
		return nil
	}

	words := countWords(plan.SourceText)
	maxWords := int(totalSeconds * cfg.WordsPerSecondClipEvidence)
	if words <= maxWords {
		return nil
	}

	return &scriptpkg.PayloadValidationError{
		Code:      "SOURCE_TEXT_EXCEEDS_CLIP_EVIDENCE",
		Message:   "source_text word count exceeds what the available clip evidence duration can support",
		Stage:     "plan.validation",
		Retryable: false,
		Extra: map[string]any{
			"actual_words":     words,
			"max_words":        maxWords,
			"evidence_seconds": totalSeconds,
			"words_per_second": cfg.WordsPerSecondClipEvidence,
			"source_type":      plan.SourceKind,
		},
	}
}
