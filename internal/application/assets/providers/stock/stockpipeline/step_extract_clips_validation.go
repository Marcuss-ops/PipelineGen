package stockpipeline

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// validateAndProbeSourceDuration resolves the source duration and bounds-checks
// every clip's EndSec against it. Returns the duration, a per-clip error map
// (for soft-degradation paths), and a hard error on the first out-of-range clip.
func validateAndProbeSourceDuration(ctx context.Context, runner StepRunner, sourceID, sourcePath string, staged *assets.StagedAsset, groupPlans []ClipPlan) (float64, map[int]error, error) {
	// Tier 1: staged.DurationSec fast-path.
	duration := staged.DurationSec

	// Tier 2: ffprobe SourceDurationProbe.
	if duration <= 0 {
		probe := runner.SourceDurationProbe()
		if probe != nil {
			probed, probeErr := probe.ProbeDurationSec(ctx, sourcePath)
			if probeErr == nil && probed > 0 {
				duration = probed
			} else if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: SourceDurationProbe failed",
					zap.String("source_id", sourceID),
					zap.String("source_path", sourcePath),
					zap.Error(probeErr))
			}
		}
	}

	// Tier 3: Warn + skip validation (backward-compat).
	if duration <= 0 {
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
