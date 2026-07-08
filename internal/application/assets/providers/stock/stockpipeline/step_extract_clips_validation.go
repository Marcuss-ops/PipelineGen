// Package stockpipeline — step_extract_clips_validation.go
// (PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026).
//
// SOLE owner of the pre-cut duration validation logic for
// StockExtractClipsStep. The helper extracts the duration-probe +
// bounds-check loop that previously lived inline inside Run() so
// future validation policy drift (e.g. add sub-second epsilon
// tolerance, add additional source-duration sources, add a
// per-clip warning vs error threshold) touches ONLY this
// capability file. Lookup path preserved — same package, so
// `validateAndProbeSourceDuration(ctx, runner, sourceID, sourcePath,
// staged, groupPlans)` resolves via package-scope visibility from
// the orchestrator in step_extract_clips.go.
//
// godlike/07 fail-closed contract:
//   - staged.DurationSec > 0 → fast-path read; no subprocess
//     call; durationKnown=true.
//   - probe != nil → ffprobe-backed ProbeDurationSec; failure
//     logs Warn + durationKnown=false (fail-open per
//     PR-STOCK-SOURCE-DURATION-WIRE forward-pointer).
//   - probe == nil + DurationSec == 0 → Warn (composition-root
//     misconfiguration); durationKnown=false.
//   - durationKnown=true + any clip.EndSec > durationSec →
//     return fmt.Errorf("...: %w", ErrStockClipsOutOfRange) so
//     the orchestrator's caller can errors.Is the typed
//     sentinel regardless of which clip was the first violator.
//   - durationKnown=false → skip bounds check entirely
//     (fail-open backward-compat; same contract as nil probe).
package stockpipeline

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// validateAndProbeSourceDuration runs the canonical pre-cut
// validation for one source. Returns the probed duration (in
// seconds) and a durationKnown boolean; the orchestrator currently
// uses durationKnown as a "should I bounds-check" gate. Returns a
// non-nil error ONLY when a clip is out of range AND the duration
// is known (the typed sentinel ErrStockClipsOutOfRange is wrapped
// via fmt.Errorf so errors.Is(err, ErrStockClipsOutOfRange) succeeds
// at the orchestrator call site regardless of which clip was the
// first violator).
//
// Source of truth priority (godlike/06 SSOT):
//  1. staged.DurationSec — populated upstream by stock.stage_sources
//     when known (yt-dlp --print-duration at staging time). Fast
//     path; no subprocess call.
//  2. runner.SourceDurationProbe().ProbeDurationSec — ffprobe-backed
//     probe for legacy composition roots that don't pre-populate
//     DurationSec. Optional; nil ⇒ skip validation (godlike/07
//     fail-open backward-compat; PR-STOCK-SOURCE-DURATION-WIRE
//     is the forward-pointer for production wiring).
//  3. Neither available — log Warn + return durationKnown=false
//     (legacy unvalidated path; same backward-compat as nil probe).
//
// Boundary semantics: strict `>` (no epsilon) — EndSec ==
// sourceDurationSec is treated as in-range. This matches the user
// spec literal "fallire subito con errore leggibile" (the
// boundary-equal case is a valid full-length cut, not an
// out-of-range error). If a future PR needs sub-second tolerance
// for floating-point drift, add an epsilon constant at the top of
// this file and re-pin the test.
//
// godlike/07 minimum-blast-radius: the helper is private to the
// stockpipeline package; signature is unchanged from the inline
// logic it replaces. The single error return is the typed sentinel
// wrap (no extra error wrapping so callers can errors.Is the
// sentinel directly).
func validateAndProbeSourceDuration(
	ctx context.Context,
	runner StepRunner,
	sourceID, sourcePath string,
	staged *assets.StagedAsset,
	groupPlans []ClipPlan,
) (durationSec float64, durationKnown bool, err error) {
	if staged != nil && staged.DurationSec > 0 {
		durationSec = staged.DurationSec
		durationKnown = true
	} else if probe := runner.SourceDurationProbe(); probe != nil {
		d, probeErr := probe.ProbeDurationSec(ctx, sourcePath)
		if probeErr != nil {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: duration probe failed — skipping bounds check (godlike/07 fail-open)",
					zap.String("source_id", sourceID),
					zap.String("source_path", sourcePath),
					zap.Error(probeErr))
			}
		} else if d > 0 {
			durationSec = d
			durationKnown = true
		}
	} else if runner.Log() != nil {
		// godlike/07 fail-open but loudly: a missing probe AND
		// no DurationSec is a composition-root misconfiguration.
		// Warn (not Debug) so operators notice out-of-range clips
		// are not being caught. Forward-pointer
		// PR-STOCK-SOURCE-DURATION-WIRE closes this gap at the
		// composition root.
		runner.Log().Warn("orchestrator: stock.extract_clips: no DurationSec + no SourceDurationProbe wired — bounds check skipped (out-of-range clips will NOT be caught; compose WithSourceProbe at the composition root)",
			zap.String("source_id", sourceID),
			zap.String("source_path", sourcePath),
			zap.Int("clip_count", len(groupPlans)))
	}

	// Bounds check (godlike/07 NO-FAKE-AVAILABILITY): every clip
	// in the group must have EndSec <= source duration. The helper
	// returns the FIRST violation with the typed sentinel wrapped
	// via fmt.Errorf so callers can errors.Is(err,
	// ErrStockClipsOutOfRange) regardless of which clip was the
	// first violator. Boundary semantics: strict `>` (no epsilon) —
	// see package godoc.
	if durationKnown {
		for clipIdx, plan := range groupPlans {
			if plan.EndSec > durationSec {
				if runner.Log() != nil {
					runner.Log().Error("orchestrator: stock.extract_clips: clip EndSec exceeds source duration — aborting (no auto-clamp per user spec)",
						zap.String("source_id", sourceID),
						zap.Int("clip_index", clipIdx),
						zap.String("artifact_id", plan.OutputLogicalID),
						zap.Float64("clip_end_sec", plan.EndSec),
						zap.Float64("source_duration_sec", durationSec),
						zap.Float64("overrun_sec", plan.EndSec-durationSec))
				}
				return durationSec, true, fmt.Errorf("orchestrator: stock.extract_clips: clip[%d] (artifact=%s) EndSec=%.2f exceeds source duration=%.2f (overrun=%.2fs): %w",
					clipIdx, plan.OutputLogicalID, plan.EndSec, durationSec, plan.EndSec-durationSec, ErrStockClipsOutOfRange)
			}
		}
	}

	return durationSec, durationKnown, nil
}
