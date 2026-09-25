package stockpipeline

import (
	"context"
	"fmt"
	"path/filepath"
)

// executeCuts builds CutJobs, creates the persistent workspace, and calls
// the VideoCutter.Cut port for a single source group. When the run has no
// effects or transitions, the cutter's normalized output is already the
// canonical final artifact and receives the stock_final name used by resume
// and downstream publication.
//
// cutOffsetSec converts the group's ABSOLUTE plan timestamps into seeks inside
// the staged file: 0 when the whole source was staged, the section start when a
// sections_only run staged only its planned slice.
func executeCuts(ctx context.Context, runner StepRunner, sourceID, sourcePath string, sourceDuration float64, groupPlans []ClipPlan, sourceIdx int, noAudio bool, cutOffsetSec float64) (CutBatchResult, error) {
	cutter := runner.Cutter()
	localFS := runner.LocalFS()
	if localFS == nil {
		return CutBatchResult{}, ErrStockExtractClipsLocalFSRequired
	}
	jobs := make([]CutJob, len(groupPlans))

	workspaceDir, err := filepath.Abs(filepath.Join("data", "stock", "workspaces", runner.JobID(), "extracted"))
	if err != nil {
		return CutBatchResult{}, fmt.Errorf("orchestrator: stock.extract_clips: resolve persistent workspace: %w", err)
	}
	if err := localFS.MkdirAll(workspaceDir, 0o755); err != nil {
		return CutBatchResult{}, fmt.Errorf("orchestrator: stock.extract_clips: create persistent workspace: %w", err)
	}

	outputPrefix := "stock_cut"
	if isCanonicalFinalCut(runner.RunInput()) {
		outputPrefix = "stock_final"
	}
	for clipIdx, plan := range groupPlans {
		start := plan.StartSec - cutOffsetSec
		end := plan.EndSec - cutOffsetSec
		if start < 0 || end <= start {
			// Fail closed: a negative seek would silently publish the wrong
			// seconds. Unreachable for a section derived from these same plans
			// (sectionCutOffsetSec is the group's minimum StartSec).
			return CutBatchResult{}, fmt.Errorf("%w: clip[%d] %s window=[%.3f,%.3f] section_offset=%.3f",
				ErrStockExtractClipsBeforeSection, clipIdx, plan.OutputLogicalID, plan.StartSec, plan.EndSec, cutOffsetSec)
		}
		outputPath := filepath.Join(workspaceDir,
			fmt.Sprintf("%s_%s_%d_%d.mp4", outputPrefix, runner.JobID(), sourceIdx, clipIdx))
		jobs[clipIdx] = CutJob{
			StartSec:   start,
			EndSec:     end,
			OutputPath: outputPath,
		}
	}

	canonical := DefaultPipelineConfig()
	req := CutRequest{
		SourcePath:     sourcePath,
		SourceDuration: sourceDuration,
		Jobs:           jobs,
		// Leave encoder policy resolution to the configured infrastructure
		// cutter; empty means auto/NVENC/libx264 is resolved there.
		Codec:            "",
		Preset:           canonical.Preset,
		CRF:              canonical.CRF,
		Width:            canonical.Width,
		Height:           canonical.Height,
		FPSNum:           canonical.FPSNum,
		FPSDen:           canonical.FPSDen,
		KeyframeInterval: canonical.KeyframeInterval,
		NoAudio:          noAudio,
		Logger:           runner.Log(),
		SourceIdx:        sourceIdx,
	}

	metric := startStockPhase(ctx, runner, "stock.extract")
	result, cutErr := cutter.Cut(ctx, req)
	if metric != nil {
		successful := result.SuccessfulItems()
		metric.SetItems(int64(len(jobs)), int64(len(successful)))
		metric.SetItemsFailed(int64(len(jobs) - len(successful)))
		finishStockPhase(runner, metric, "stock.extract", cutErr)
	}
	return result, cutErr
}

// isCanonicalFinalCut reports whether cutter output can be published as the
// final canonical artifact without a second render pass. A nil input keeps
// the conservative legacy behavior and requires compose_chunks.
func isCanonicalFinalCut(input *RunInput) bool {
	return input != nil && input.NoEffects && input.NoTransitions
}

// sectionCutOffsetSec returns the seconds to subtract from a plan's absolute
// timestamps before seeking inside the STAGED file. A sections_only run stages
// only the source's section, so the staged file starts at the section start and
// the first planned clip sits at local offset 0.
//
// Returns 0 when the run stages whole sources (plan timestamps are already
// file-relative) and 0 for a group that is not one contiguous slice, matching the
// stager's decision to download that source whole (sectionWindowForPlans, in
// downloader_port.go). It lives next to executeCuts because that is the only
// consumer of the seek it produces.
func sectionCutOffsetSec(in *RunInput, plans []ClipPlan) float64 {
	if !isSectionedRun(in) {
		return 0
	}
	start, _, ok := sectionWindowForPlans(plans)
	if !ok {
		return 0
	}
	return start
}
