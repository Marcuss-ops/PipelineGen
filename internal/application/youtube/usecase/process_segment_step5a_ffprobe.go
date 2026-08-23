// Package usecase — process_segment_step5a_ffprobe.go: canonical owner of
// Step 5a (ffprobe validation, audit 2026-07-03 BLOCKER #3).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE owner of the FFProbe port invocation inside Execute. The
// validateFFProbeReport helper that the call site uses lives ONLY in
// process_segment_helpers.go (5 fail-closed gates: nil report /
// non-readable container / no video stream / duration tolerance /
// invalid width-height / invalid FPS / audio-present-when-keepAudio).
//
// godlike/07 no-fake-availability: when the FFProbe port is nil, the
// validation step is SILENTLY SKIPPED (operator observability preserved
// via the upstream hash + stat fail-closed gates in Step 5). No silent
// pass-through of the optional port.
package usecase

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// step5a_FFProbeValidate runs the optional ffprobe validation. Returns
// nil when:
//   - FFProbe port is nil (skipped), OR
//   - probe executes successfully AND validateFFProbeReport returns nil
//
// Returns the typed *ExtractionError (FailureCodeFFProbeValidationFailed)
// when the report fails any of the fail-closed gates, OR when ffprobe
// execution itself errors. On typed-error path, `u.fail(...)` has
// already mutated `out.Item.Status="failed"` + `out.Item.Error` +
// `out.Error` per its canonical contract.
//
// godlike/07 no-fake-availability: non-fatal Warnings from
// `report.Warnings` are logged at Warn level so operator dashboards
// see them — but they DO NOT block pipeline progression.
func (u *ProcessYouTubeSegmentUseCase) step5a_FFProbeValidate(
	ctx context.Context,
	out *youtubetypes.ProcessSegmentResult,
	clipID string,
	localPath string,
	expectedDurationSec int,
	keepAudio bool,
) error {
	if u.media.FFProbe == nil {
		return nil
	}
	report, probeErr := u.media.FFProbe.ValidateClip(ctx, localPath, expectedDurationSec, keepAudio)
	if probeErr != nil {
		typed := NewExtractionError(FailureCodeFFProbeValidationFailed, false,
			fmt.Sprintf("ffprobe execution failed for %q: %v", localPath, probeErr),
			probeErr)
		return u.fail(out, typed)
	}
	if ffprobeErr := validateFFProbeReport(report, localPath, expectedDurationSec, keepAudio); ffprobeErr != nil {
		return u.fail(out, ffprobeErr)
	}
	// Log non-fatal warnings for operator dashboards.
	for _, w := range report.Warnings {
		u.core.Log.Warn("ffprobe: non-fatal warning",
			zap.String("clip_id", clipID),
			zap.String("local_path", localPath),
			zap.String("warning", w))
	}
	return nil
}
