package stockpipeline

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
)

// validateAndProbeSourceDuration resolves the source duration and bounds-checks
// every clip's EndSec against it. Returns the duration, a per-clip error map
// (for soft-degradation paths), and a hard error on the first out-of-range clip.
//
// cutOffsetSec converts the group's absolute plan timestamps into the staged
// file's own timeline (0 for a whole-source stage). This is what keeps the
// fail-closed contract meaningful for a sections_only run: the probed duration
// here is the SECTION's duration (~40s), so an absolute EndSec of 512s would
// otherwise look out of range on every clip. It also means a section that came
// back SHORTER than planned (provider truncated it, or the source ended inside
// the block) still fails closed instead of publishing a clipped-out gap.
func validateAndProbeSourceDuration(ctx context.Context, runner StepRunner, sourceID, sourcePath string, staged *assets.StagedAsset, groupPlans []ClipPlan, cutOffsetSec float64) (float64, map[int]error, error) {
	// Tier 1: staged.DurationSec fast-path.
	duration := staged.DurationSec

	// Tier 2: ffprobe SourceDurationProbe.
	var probeErr error
	if duration <= 0 {
		probe := runner.SourceDurationProbe()
		if probe != nil {
			probed, err := probe.ProbeDurationSec(ctx, sourcePath)
			probeErr = err
			if err == nil && probed > 0 {
				duration = probed
			} else {
				if err == nil {
					probeErr = fmt.Errorf("probe returned non-positive duration %.2f", probed)
				}
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.extract_clips: SourceDurationProbe failed",
						zap.String("source_id", sourceID),
						zap.String("source_path", sourcePath),
						zap.Error(probeErr))
				}
			}
		}
	}

	// Tier 3: production is fail-closed when the source duration is
	// still unknown. Fixture runners may intentionally leave probing
	// disabled for hermetic tests that do not exercise duration validation.
	if duration <= 0 {
		if runner.Cfg().StrictDurationValidation {
			if probeErr != nil {
				return 0, nil, fmt.Errorf("%w: source_id=%s source_path=%s: %w", ErrStockClipsUnknownDuration, sourceID, sourcePath, probeErr)
			}
			return 0, nil, fmt.Errorf("%w: source_id=%s source_path=%s", ErrStockClipsUnknownDuration, sourceID, sourcePath)
		}
		if runner.Log() != nil {
			runner.Log().Warn("orchestrator: stock.extract_clips: no source duration available — skipping bounds check",
				zap.String("source_id", sourceID))
		}
		return 0, nil, nil
	}

	// Bounds-check: every clip.EndSec must be ≤ duration, in the staged file's
	// own timeline (see cutOffsetSec in the doc comment above).
	for i, plan := range groupPlans {
		localEnd := plan.EndSec - cutOffsetSec
		if localEnd > duration {
			overrun := localEnd - duration
			return duration, nil, fmt.Errorf("%w: clip[%d] %s EndSec=%.2f > duration=%.2f overrun=%.2fs (section_offset=%.3f)",
				ErrStockClipsOutOfRange, i, plan.OutputLogicalID, localEnd, duration, overrun, cutOffsetSec)
		}
	}

	return duration, nil, nil
}
