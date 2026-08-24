// Package usecase — process_segment_step1.go: canonical owner of
// Step 1 (deterministic clip ID + timestamp validation + SegmentPolicy
// bounds + filename with policyVersion).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE owner of the timestamp parse + order-check + SegmentPolicy gate +
// deterministic clip ID generation + filename composition. The legacy
// inline block in `Execute` (pre-PR-SPLIT-PROCESS-SEGMENT) had ALL of
// this in a 100-LOC chunk; now it is one focused method that is invoked
// exactly once from the orchestrator at process_segment.go.
//
// godlike/07 no-fake-availability: every failure path returns a typed
// `*ExtractionError` via the canonical fail / failInvalidTimestamp
// helpers in process_segment_helpers.go (nil / empty / negative
// duration branches all have dedicated typed sentinels).
package usecase

import (
	"fmt"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// step1_BuildClipID is the canonical owner of Step 1. Returns:
//
//   - startSec, endSec: parsed clip boundaries in seconds (rounded)
//   - duration: endSec - startSec (asserted > 0)
//   - clipID: deterministic fmt.Sprintf("yt_%s_%d_%d_%s", videoID, start, end, policyVer)
//   - policyVer: cmd.PolicyVersion with ProcessSegmentPolicyVersion default fallback
//   - err: nil on success; typed *ExtractionError on any validation failure,
//     OR a typed *ExtractionError via fail() / failInvalidTimestamp() already
//     recorded on `out` (callers should return `out, err` immediately).
//
// Mutates `out.ID` + `out.Item.ID` + `out.Item.StartSeconds` +
// `out.Item.EndSeconds` + `out.Item.Duration` + `out.Item.Filename` on
// success. On failure, the fail/failInvalidTimestamp helpers have
// already set `out.Item.Status="failed"` + `out.Item.Error` + `out.Error`.
// Move the typed error envelope from pre-split Execute verbatim:
// every error message + sentinel FailureCode is preserved byte-equivalent.
//
// godlike/07 minimum-blast-radius: 4 named-return values mirror the
// pre-split inline locals exactly (startSec/endSec/duration/clipID/
// policyVer) — no behavior drift on the orchestrator's variable shadowing
// after the move.
func (u *ProcessYouTubeSegmentUseCase) step1_BuildClipID(
	cmd youtubetypes.ProcessSegmentCommand,
	out *youtubetypes.ProcessSegmentResult,
) (startSec, endSec, duration int, clipID, policyVer string, err error) {
	startSec, err = textutil.ParseTimestamp(out.Item.Start)
	if err != nil {
		return 0, 0, 0, "", "", u.failInvalidTimestamp(out, "start", err)
	}
	endSec, err = textutil.ParseTimestamp(out.Item.End)
	if err != nil {
		return 0, 0, 0, "", "", u.failInvalidTimestamp(out, "end", err)
	}
	if startSec >= endSec {
		msg := fmt.Sprintf("start time (%s) must be before end time (%s)", cmd.Segment.Start, cmd.Segment.End)
		typed := NewExtractionError(FailureCodeInvalidTimestamp, false, msg, nil)
		return 0, 0, 0, "", "", u.fail(out, typed)
	}
	duration = endSec - startSec
	policyVer = cmd.PolicyVersion
	if policyVer == "" {
		policyVer = ProcessSegmentPolicyVersion
	}
	// Commit 2/6 #3: SegmentPolicy duration gate.
	if !u.core.SegmentPolicy.ValidDuration(duration) {
		policy := u.core.SegmentPolicy
		if policy.MinDuration == 0 {
			policy.MinDuration = youtubetypes.DefaultSegmentPolicy().MinDuration
		}
		if policy.MaxDuration == 0 {
			policy.MaxDuration = youtubetypes.DefaultSegmentPolicy().MaxDuration
		}
		msg := fmt.Sprintf("duration %ds out of range [%d, %d]", duration, policy.MinDuration, policy.MaxDuration)
		typed := NewExtractionError(FailureCodeDurationOutOfRange, false, msg, nil)
		return 0, 0, 0, "", "", u.fail(out, typed)
	}
	clipID = fmt.Sprintf("yt_%s_%d_%d_%s", cmd.VideoID, startSec, endSec, policyVer)
	out.ID = clipID
	out.Item.ID = clipID
	out.Item.StartSeconds = startSec
	out.Item.EndSeconds = endSec
	out.Item.Duration = duration
	// Commit 2/6 #4: filename carries the policyVersion.
	out.Item.Filename = u.core.SegmentsSvc.BuildClipFilename(
		cmd.VideoID, startSec, endSec, out.Item.Name, policyVer,
	)
	return startSec, endSec, duration, clipID, policyVer, nil
}
