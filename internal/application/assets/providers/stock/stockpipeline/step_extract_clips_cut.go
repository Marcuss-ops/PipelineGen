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
func executeCuts(ctx context.Context, runner StepRunner, sourceID, sourcePath string, sourceDuration float64, groupPlans []ClipPlan, sourceIdx int, noAudio bool) (CutBatchResult, error) {
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
		outputPath := filepath.Join(workspaceDir,
			fmt.Sprintf("%s_%s_%d_%d.mp4", outputPrefix, runner.JobID(), sourceIdx, clipIdx))
		jobs[clipIdx] = CutJob{
			StartSec:   plan.StartSec,
			EndSec:     plan.EndSec,
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
		FPS:              canonical.FPS,
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
