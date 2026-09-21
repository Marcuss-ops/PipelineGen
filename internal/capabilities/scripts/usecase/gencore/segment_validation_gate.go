package gencore

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// validateGeneratedSegments applies the aggregate segment gate after worker
// generation. Per-segment validation remains in validateSegmentTexts; this
// helper owns only the final relaxed-clip policy and error formatting.
func validateGeneratedSegments(plan *scriptpkg.ResolvedGenerationPlan, texts []string, settings segmentValidationSettings) error {
	report := validateSegmentTexts(plan, texts, settings)
	if report.Valid || relaxedShortClipQuality(plan) || relaxedStockOnlySegmentsQuality(plan, texts, settings) {
		return nil
	}
	return fmt.Errorf("%w: %s", scriptpkg.ErrSegmentValidationFailed, strings.Join(report.Reasons, "; "))
}

func relaxedStockOnlySegmentsQuality(plan *scriptpkg.ResolvedGenerationPlan, texts []string, settings segmentValidationSettings) bool {
	if plan == nil || plan.MediaMode != scriptpkg.MediaModeStockOnly || len(texts) != len(plan.Segments) {
		return false
	}
	for i, text := range texts {
		budget := segmentBudgetFor(plan, i, settings.segmentTolerancePercent)
		if !relaxedStockOnlyQuality(plan, text, budget) {
			return false
		}
	}
	return true
}
