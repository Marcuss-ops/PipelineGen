package render

import (
	"context"

	"go.uber.org/zap"
)

// probeSourceDuration determines the source duration for the
// timestamp-overflow filter inside Cut(). It prefers the
// CutRequest-provided duration (req.SourceDuration) to avoid a
// redundant ffprobe call when the upstream stock.extract_clips step
// has already probed the source via validateAndProbeSourceDuration.
// Falls back to a live ffmpeg.Probe call on the source path when
// missing.
//
// Returns (durationSec, nil) on success; (0, nil) on probe failure
// (the caller proceeds without pre-flight validation, matching the
// pre-Phase-9 behaviour).
//
// Phase 9 split: the original Cut prelude's source-duration
// branching (3-way if/elif/else with stock-extractor log lines) is
// extracted into this helper so the Cut body reads as a pure
// workflow-control sequence. The function logs through the supplied
// logger so the existing zap-stamped log lines remain at the
// same plumbing context (no zap.With or re-init at call sites).
func (c *FFmpegCutter) probeSourceDuration(ctx context.Context, srcPath string, requestedDuration float64, logger *zap.Logger) (float64, error) {
	if requestedDuration > 0 {
		logger.Info("stock extractor: skipping probe, using pre-flight duration",
			zap.String("source", srcPath),
			zap.Float64("duration_sec", requestedDuration),
		)
		return requestedDuration, nil
	}
	info, probeErr := c.proc.Probe(ctx, srcPath)
	if probeErr == nil && info != nil {
		logger.Info("stock extractor: source duration probed",
			zap.String("source", srcPath),
			zap.Float64("duration_sec", info.Duration.Seconds()),
		)
		return info.Duration.Seconds(), nil
	}
	logger.Warn("stock extractor: source duration probe failed — proceeding without validation",
		zap.String("source", srcPath),
		zap.Error(probeErr),
	)
	return 0, nil
}
