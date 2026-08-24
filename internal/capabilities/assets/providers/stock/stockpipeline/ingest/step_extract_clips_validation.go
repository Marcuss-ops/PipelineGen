package assets

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
)

// validateAndProbeSourceDuration resolves the source duration and bounds-checks
// every clip's EndSec against it. Returns the duration, a per-clip error map
// (for soft-degradation paths), and a hard error on the first out-of-range clip.
func validateAndProbeSourceDuration(ctx context.Context, runner StepRunner, sourceID, sourcePath string, staged *assets.StagedAsset, groupPlans []ClipPlan) (float64, map[int]error, error) {
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

	// Bounds-check: every clip.EndSec must be ≤ duration.
	for i, plan := range groupPlans {
		if plan.EndSec > duration {
			overrun := plan.EndSec - duration
			return duration, nil, fmt.Errorf("%w: clip[%d] %s EndSec=%.2f > duration=%.2f overrun=%.2fs",
				ErrStockClipsOutOfRange, i, plan.OutputLogicalID, plan.EndSec, duration, overrun)
		}
	}

	return duration, nil, nil
}
