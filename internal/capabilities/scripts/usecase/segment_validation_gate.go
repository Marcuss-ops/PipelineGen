package usecase

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
	if report.Valid || relaxedShortClipQuality(plan) {
		return nil
	}
	return fmt.Errorf("%w: %s", scriptpkg.ErrSegmentValidationFailed, strings.Join(report.Reasons, "; "))
}
