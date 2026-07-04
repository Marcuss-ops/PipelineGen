// Package voiceover — stage_postprocess.go (PR-VO-STAGES-SPLIT, P0 #2 in
// VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-01).
//
// Stage 2 of the 5-stage voiceover pipeline: audio post-processing
// (silence removal) via AudioPostProcessor.Process. This is a
// forward-pointer per the thinker's godlike/07 minimal-blast-radius
// recommendation — the batch pipeline (process.go) does NOT call
// postprocessStage directly in the EXPAND phase. The canonical
// per-item pipeline (process_voiceover_item.go) continues to call
// AudioPostProcessor.Process inline as its Stage 2.
//
// Mechanical extraction from process_voiceover_item.go Stage 2
// logic. The free-function form (not a Service method) is
// intentional: the Service struct does not own an
// AudioPostProcessor field today (only the per-item use case
// does). A future BACKFILL wave will add an audioPostProcessor
// field to Service and wire postprocessStage between
// synthesizeStage and destinationStage.
//
// Compile-time lock: process_voiceover_item.go reads the same
// AudioPostProcessor / AudioPostInput types via the ports package —
// preserved verbatim. The free function here is dead code in
// the EXPAND phase (godlike/07 no-fake-availability discipline:
// it is NOT called by any production path today; it exists as a
// canonical seam for the future BACKFILL wave to wire).
package voiceover

import (
	"context"
	"fmt"
)

// postprocessStage is the forward-pointer for Stage 2 of the
// 5-stage pipeline (audio silence removal). It mirrors the
// logic from process_voiceover_item.go::Execute Stage 2.
//
// In the current EXPAND phase, the batch pipeline (process.go)
// does NOT call postprocessStage directly — the synthesize stage
// passes RemoveSilence to the TTS provider (which strips silence
// inline). The canonical per-item pipeline (process_voiceover_item.go)
// calls AudioPostProcessor.Process as a separate Stage 2.
//
// godlike/07 honest-limitation: this function is NOT wired in
// process.go. A future BACKFILL wave will add an
// audioPostProcessor field to the Service struct and wire
// postprocessStage between synthesizeStage and destinationStage.
//
// Behavior contract (mirrors process_voiceover_item.go Stage 2):
//   - No-op when removeSilence is false, processor is nil, or
//     localPath is empty (silent skip; item is returned unchanged).
//   - On error: item.fail(FailureTTS, fmt.Errorf(...)) — the
//     canonical fail() helper normalises to Status=StatusFailed
//     per the audit P0.1 contract (no substring matching).
//   - On success: item.CleanedPath is updated to postOut.CleanedPath
//     if non-empty.
func postprocessStage(
	ctx context.Context,
	processor AudioPostProcessor,
	item BatchItem,
	localPath string,
	outputDir string,
	filename string,
	removeSilence bool,
) BatchItem {
	if !removeSilence || processor == nil || localPath == "" {
		return item
	}
	postOut, err := processor.Process(ctx, AudioPostInput{
		LocalPath: localPath,
		OutputDir: outputDir,
		Filename:  filename,
	})
	if err != nil {
		return item.fail(FailureTTS,
			fmt.Errorf("%s: postprocess (AudioPostProcessor): %w", restoreIdent, err))
	}
	if postOut.CleanedPath != "" {
		item.CleanedPath = postOut.CleanedPath
	}
	return item
}
